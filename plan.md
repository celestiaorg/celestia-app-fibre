# Plan: Add Fibre Client Support to Talis

## Context

Talis (`./tools/talis`) needs fibre DA tooling equivalent to its existing `txsim` command. Three new commands:
1. `setup-fibre` -- post-deploy step: SSH into each validator to register its fibre host address + fund its escrow account
2. `fibre-txsim` -- run locally: spam the network with `fibre.Client.Put()` calls
3. `fibre-throughput` -- run locally: monitor blocks in real-time, decode `MsgPayForFibre` txs, print throughput per block

No proto changes, no module changes, no genesis modifiers. Registration and escrow funding happen at runtime via SSH after deploy.

## Port Layout

- App gRPC: `0.0.0.0:9091` (fibre server + tx submission + valaddr queries)
- CometBFT RPC: `0.0.0.0:26657` (block queries)

---

## New Files

### 1. `tools/talis/setup_fibre.go` -- Post-deploy Fibre Setup

New `setup-fibre` command. SSHes into each validator and runs two txs: register fibre host + fund escrow. Uses `runScriptInTMux` (same pattern as `talis txsim`).

**Flags:**
- `-d / --directory` (string, default `.`)
- `-k / --ssh-key-path` (string)
- `--escrow-amount` (string, default `4999999999999999utia`)
- `--fibre-port` (int, default `9091`)
- `--fees` (string, default `5000utia`)

**Script per validator:**
```bash
sleep 5 && \
celestia-appd tx valaddr set-host <public_ip>:<fibre_port> \
  --from validator --keyring-backend=test --home .celestia-app \
  --chain-id <chain_id> --fees <fees> --yes && \
sleep 5 && \
celestia-appd tx fibre deposit-to-escrow <escrow_amount> \
  --from validator --keyring-backend=test --home .celestia-app \
  --chain-id <chain_id> --fees <fees> --yes
```

Each validator runs its own script via `runScriptInTMux` with session name `"setup-fibre"`.

### 2. `tools/talis/fibre_txsim.go` -- Fibre Blob Spammer

New `fibre-txsim` command. Runs locally, connects to a validator's gRPC endpoint.

**Flags:**
- `-d / --directory` (string, default `.`)
- `--grpc-endpoint` (string) -- defaults to first validator IP:9091
- `--keyring-dir` (string) -- defaults to `<rootDir>/payload/<first-validator-name>`
- `--key-name` (string, default `txsim`)
- `--blob-size` (int, default `1000000`)
- `--concurrency` (int, default `1`)
- `--interval` (duration, default `0` = no delay) -- delay between blob submissions
- `--duration` (duration, default `0` = until Ctrl+C)

**Logic:**
1. Load config, resolve gRPC endpoint (first validator IP:9091)
2. Set up fibre client (same pattern as `tools/submit-fibre-blob/main.go`):
   - `encoding.MakeConfig(app.ModuleEncodingRegisters...)`
   - `keyring.New(app.Name, keyring.BackendTest, keyringDir, nil, encCfg.Codec)`
   - `grpc.NewClient(endpoint, insecure, maxMsgSize)`
   - `fibregrpc.NewSetGetter(coregrpc.NewBlockAPIClient(grpcConn))`
   - `fibregrpc.NewHostRegistry(valaddrtypes.NewQueryClient(grpcConn))`
   - `user.SetupTxClient(ctx, kr, grpcConn, encCfg, user.WithDefaultAccount(keyName))`
   - `fibre.NewClient(txClient, kr, valGet, hostReg, clientCfg)`
3. Spam loop: bounded concurrency via semaphore, optional ticker-based pacing via `--interval`, random blob data
4. Print each result to stdout as it completes (height, tx hash, latency, error)
5. On Ctrl+C / timeout: print summary (total sent, successes, failures, avg latency)

### 3. `tools/talis/throughput.go` -- Real-time Throughput Monitor

New `fibre-throughput` command. Runs locally, polls blocks from a validator's RPC.

**Flags:**
- `-d / --directory` (string, default `.`)
- `--rpc-endpoint` (string) -- defaults to first validator IP:26657
- `--duration` (duration, default `0` = until Ctrl+C)

**Logic:**
1. Load config, resolve RPC endpoint (first validator IP:26657)
2. Create CometBFT HTTP client + tx decoder via `encCfg.TxConfig.TxDecoder()`
3. Poll for new blocks (2s interval)
4. For each block: decode all txs, filter `*fibretypes.MsgPayForFibre`, sum `PaymentPromise.BlobSize`
5. Print one line per block: `height=%d txs=%d blob_bytes=%d block_time=%.2fs throughput=%.2f MB/s`
6. On exit: print summary (avg throughput, total bytes, total blocks)

`MsgPayForFibre.PaymentPromise.BlobSize` (uint32) gives the blob size directly.

---

## Modified Files

### 4. `tools/talis/main.go`

Add three new commands:
```go
setupFibreCmd(),
fibreTxsimCmd(),
fibreThroughputCmd(),
```

---

## Key Reference Files
- `tools/submit-fibre-blob/main.go` -- fibre client setup pattern (grpc, keyring, fibre.NewClient, Put)
- `tools/talis/txsim.go` -- cobra command pattern + `runScriptInTMux` usage
- `tools/talis/execution.go` -- `runScriptInTMux()` implementation
- `tools/talis/status.go` -- CometBFT RPC client pattern
- `scripts/single-node-fibre.sh` -- `set-host` + sleep pattern
- `fibre/client.go` + `fibre/client_put.go` -- `NewClient()` and `Put()` API
- `x/fibre/types/tx.pb.go` -- `MsgPayForFibre` with `PaymentPromise.BlobSize`

## Workflow

```
1. talis init
2. talis add
3. talis up
4. talis genesis
5. talis deploy
6. talis setup-fibre         (NEW -- registers hosts + funds escrow)
7. talis fibre-txsim         (NEW -- spams fibre blobs)
8. talis fibre-throughput    (NEW -- monitors throughput, separate terminal)
9. talis download
10. talis down
```

## Verification
1. Build: `cd tools/talis && go build .`
