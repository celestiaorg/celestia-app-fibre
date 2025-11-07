# Fibre Load Generator

A tool for generating throughput load on the Fibre network by submitting blob transactions at a configurable rate using the Fibre client.

## Features

- Configurable transaction submission interval
- Configurable payload size (default: 1MB)
- Smart keyring management (automatically selects key with most funds or creates new key)
- Prints transaction confirmations with height for monitoring
- Graceful shutdown with Ctrl+C

## Usage

```bash
go run tools/fibre-load/main.go [flags]
```

### Flags

- `--grpc-endpoint` - gRPC endpoint of the consensus node (default: `localhost:9090`)
- `--keyring-dir` - Directory containing the keyring (default: `~/.celestia-app`)
- `--interval` - Interval between transactions in seconds (default: `1.0`)
- `--payload-size` - Size of payload data in bytes (default: `1048576` / 1MB)
- `--namespace` - Namespace for blob submission (default: `fibre`)

### Examples

Submit a transaction every second with 1MB payloads (default):
```bash
go run tools/fibre-load/main.go
```

Submit a transaction every 0.5 seconds with 2MB payloads:
```bash
go run tools/fibre-load/main.go --interval 0.5 --payload-size 2097152
```

Submit smaller 100KB blobs every 2 seconds:
```bash
go run tools/fibre-load/main.go --interval 2.0 --payload-size 102400
```

Use a custom gRPC endpoint:
```bash
go run tools/fibre-load/main.go --grpc-endpoint consensus.example.com:9090
```

Use a custom keyring directory:
```bash
go run tools/fibre-load/main.go --keyring-dir /path/to/keyring
```

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
- Transaction confirmations with transaction hash and block height
- Total transactions submitted on shutdown

Example output:
```
Fibre Load Generator
====================
gRPC Endpoint: localhost:9090
Keyring Directory: /Users/user/.celestia-app
Interval: 1.00 seconds
Payload Size: 1048576 bytes
Namespace: fibre

Found 2 key(s) in keyring, checking balances...
  Key 'validator' (celestia1abc123...): 1000000000 utia
  Key 'my-key' (celestia1def456...): 500000000 utia
Selected key with most funds: validator
Using key: validator
Using address: celestia1abc123...

Setting up fibre client...
Starting load generation...
Press Ctrl+C to stop

[1] Transaction ABC123... confirmed at height 12345
[2] Transaction DEF456... confirmed at height 12346
...
^C
Shutting down...

Total transactions submitted: 42
```
