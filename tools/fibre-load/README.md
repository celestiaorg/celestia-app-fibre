# Fibre Load Generator

A tool for generating throughput load on the Fibre network by submitting blob transactions at a configurable rate using the Fibre client.

## Features

- Configurable transaction submission interval
- Configurable payload size (default: 1MB)
- Configurable maximum in-flight transactions to cap memory usage
- Smart keyring management (automatically selects key with most funds or creates new key)
- OpenTelemetry distributed tracing support (optional)
- Optional Pyroscope continuous profiling with OTEL span annotations
- Prints transaction confirmations with height for monitoring
- Graceful shutdown with Ctrl+C

## Usage

```bash
go run tools/fibre-load/main.go [flags]
```

### Flags

- `--grpc-endpoint` - gRPC endpoint of the consensus node (default: `localhost:9091`)
- `--keyring-dir` - Directory containing the keyring (default: `~/.celestia-app`)
- `--validator-hosts` - Path to JSON file containing validator address to host mapping (required)
- `--chain-id` - Chain ID for the network (default: `celestia`, can also be set via `CHAIN_ID` env var)
- `--interval` - Interval between transactions as a Go duration string (default: `1s`, e.g. `500ms`, `2s`)
- `--payload-size` - Size of payload data in bytes (default: `134217728` = 128MiB)
- `--max-concurrency` - Maximum number of transactions processed concurrently (default: `1`)
- `--namespace` - Namespace for blob submission (default: `fibre`)
- `--traces-dir` - Directory to write metrics traces (default: `~/.celestia-app/data/traces`)
- `--pyroscope-url` - URL of the Pyroscope server used for continuous profiling (disabled when empty)
- `--pyroscope-trace` - Attach active spans to Pyroscope samples (requires `--pyroscope-url`)
- `--pyroscope-profile` - Repeat to select custom Pyroscope profile types (defaults to CPU, memory, goroutines, and block profiles)

### Examples

Submit a transaction every second with 1MB payloads (default):
```bash
go run tools/fibre-load/main.go
```

Submit a transaction every 0.5 seconds with 2MB payloads:
```bash
go run tools/fibre-load/main.go --interval 500ms --payload-size 2097152
```

Submit smaller 100KB blobs every 2 seconds:
```bash
go run tools/fibre-load/main.go --interval 2s --payload-size 102400
```

Use a custom gRPC endpoint:
```bash
go run tools/fibre-load/main.go --grpc-endpoint consensus.example.com:9090
```

Use a custom keyring directory:
```bash
go run tools/fibre-load/main.go --keyring-dir /path/to/keyring
```

### Controlling In-Flight Load

Large payloads require significant RAM while the Fibre client encodes and uploads them to validators. Use `--max-concurrency` to cap the number of simultaneous transactions and therefore the peak memory footprint. For example, `--max-concurrency 10 --payload-size 134217728` keeps at most ten 128MiB blobs in memory (~1.3GiB plus Fibre overhead). The default of `1` guarantees sequential submission, which is safer on smaller machines.

### Benchmarking Allocations

A synthetic benchmark mimics the per-transaction worker used by `fibre-load` and reports allocations for several payload sizes. Run it with:

```bash
go test ./tools/fibre-load -bench=LoadWorkerAllocations -benchmem -run=^$
```

Focus on the `allocs/op` and `B/op` columns to understand how payload size influences steady-state memory needs before tuning `--max-concurrency`.

## OpenTelemetry Tracing

The tool supports optional OpenTelemetry distributed tracing. To enable it, set the `OTEL_TRACING_ADDRESS` environment variable to your OTLP HTTP endpoint:

```bash
# Enable tracing to a local collector
export OTEL_TRACING_ADDRESS="localhost:4318"
go run tools/fibre-load/main.go

# Or inline
OTEL_TRACING_ADDRESS="tempo.example.com:4318" go run tools/fibre-load/main.go
```

The endpoint should be in the format `host:port` and the tool will use an insecure HTTP connection to the OTLP endpoint. Traces will include:
- Upload operations with namespace, size, and blob commitment details
- Row upload operations to individual validators
- Validator signature collection
- Error tracking and status codes

If the environment variable is not set, tracing is disabled and the tool runs normally without any trace export.

## Pyroscope Profiling

Use the new Pyroscope flags to continuously export Go profiles from `fibre-load` to an existing Pyroscope server:

```bash
go run tools/fibre-load/main.go \
  --pyroscope-url http://pyroscope.example.com:4040 \
  --pyroscope-trace \
  --pyroscope-profile cpu \
  --pyroscope-profile mem:alloc_space
```

- `--pyroscope-url` enables profiling and points to your Pyroscope server.
- `--pyroscope-trace` enriches profiles with active OpenTelemetry spans so flamegraphs link back to trace IDs.
- `--pyroscope-profile` can be repeated to control the profile set. When omitted, the Fibre client collects CPU, alloc/inuse memory (space and objects), goroutine, and blocking profiles—the same defaults as Celestia Core.

The Fibre client adds labels such as the chain ID and `component=fibre-load` so multiple load generators remain distinguishable inside Pyroscope. If the flag is omitted, no profiling goroutines are started.

## Keyring Management

The tool intelligently manages the keyring and automatically selects the best key:

1. If `--keyring-dir` is not specified, it defaults to `~/.celestia-app`
2. If the keyring directory doesn't exist, it will be created
3. The tool queries all existing keys and checks their balances
4. **Key Selection Strategy:**
   - Selects the key with the most funds (if any keys have funds)
   - Falls back to the first key if no keys have funds
   - Generates a new key named `fibre-load-key` if no keys exist
5. The tool prints the selected key name, address, and balance information

**Note:** The tool uses the test keyring backend for simplicity.

## Output

The tool prints:
- Configuration summary on startup
- Balance information for all keys in the keyring
- The selected key name and address being used
- Transaction confirmations with transaction hash, block height, and latency
- Total transactions submitted on shutdown

### Metrics Files

The tool automatically writes throughput metrics to a timestamped JSONL file in the traces directory (default: `~/.celestia-app/data/traces/`). Each line contains a JSON object with the following fields:

```json
{
  "tx_num": 1,
  "start_time": "2025-11-09T10:30:00.123456Z",
  "end_time": "2025-11-09T10:30:00.456789Z",
  "success": true,
  "tx_hash": "ABC123...",
  "height": 12345,
  "payload_size": 134217728,
  "latency_ms": 333
}
```

**Fields:**
- `tx_num`: Sequential transaction number
- `start_time`: When the transaction was initiated
- `end_time`: When the transaction completed (success or failure)
- `success`: Whether the transaction succeeded
- `tx_hash`: Transaction hash (only on success)
- `height`: Block height where transaction was included (only on success)
- `error`: Error message (only on failure)
- `payload_size`: Size of the blob payload in bytes
- `latency_ms`: End-to-end latency in milliseconds (only on success)

The metrics file is named `fibre-load-metrics-YYYYMMDD-HHMMSS.jsonl` and is automatically collected by the talis `upload-data` command when running on a talis network.

Example output:
```
Fibre Load Generator
====================
gRPC Endpoint: localhost:9091
Keyring Directory: /Users/user/.celestia-app
Chain ID: celestia
Interval: 1s
Payload Size: 134217728 bytes
Namespace: fibre
Traces Directory: /Users/user/.celestia-app/data/traces

Found 2 key(s) in keyring, checking balances...
  Key 'validator' (celestia1abc123...): 1000000000 utia
  Key 'my-key' (celestia1def456...): 500000000 utia
Selected key with most funds: validator
Using key: validator
Using address: celestia1abc123...

Setting up fibre client...
Funding escrow account with 1000000utia (enough for ~10000000 transactions)...
Escrow account funded successfully (tx: XYZ789)

Writing metrics to: /Users/user/.celestia-app/data/traces/fibre-load-metrics-20251109-103000.jsonl

Starting load generation...
Press Ctrl+C to stop

[1] Transaction ABC123... confirmed at height 12345 (latency: 333ms)
[2] Transaction DEF456... confirmed at height 12346 (latency: 301ms)
...
^C
Shutting down...

Total transactions submitted: 42
```
