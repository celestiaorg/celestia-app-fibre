# Plan: Add Fibre Client Support to Talis

## Context

Talis (`./tools/talis`) is a performance testing CLI for Celestia networks. It currently supports spamming the network with standard PayForBlob transactions via its `txsim` command. With the new fibre DA layer, we need equivalent tooling to:
1. Pre-fund fibre escrow accounts at genesis so all txsim accounts can send PayForFibre immediately
2. Pre-populate validator fibre host addresses in `x/valaddr` at genesis (required before fibre works)
3. Spam the network with fibre blob uploads via `fibre.Client.Put()` calls
4. Measure fibre throughput: total blob bytes committed per second, derived from on-chain `MsgPayForFibre` transactions per block divided by block time

## Port Layout in Talis

- App gRPC (Cosmos SDK): `0.0.0.0:9091` -- fibre server is registered here
- CometBFT gRPC: `0.0.0.0:9090`
- CometBFT RPC: `0.0.0.0:26657`

The fibre client connects to the app gRPC (9091) for tx submission and valaddr queries. Shard uploads go to each validator's registered fibre host (also port 9091).

---

## Proto Changes

### 1. `proto/celestia/valaddr/v1/genesis.proto` -- Extend GenesisState

The current proto is empty (`message GenesisState {}`). Add a `providers` field reusing the `FibreProvider` type already defined in `query.proto` (same package):

```proto
syntax = "proto3";
package celestia.valaddr.v1;

import "gogoproto/gogo.proto";
import "celestia/valaddr/v1/query.proto";

option go_package = "github.com/celestiaorg/celestia-app-fibre/x/valaddr/types";

// GenesisState defines the valaddr module's genesis state.
message GenesisState {
  repeated FibreProvider providers = 1 [(gogoproto.nullable) = false];
}
```

After editing, regenerate protobuf: `make proto-gen` (updates `x/valaddr/types/genesis.pb.go`).

---

## Module Changes

### 2. `x/valaddr/genesis.go` -- Implement InitGenesis/ExportGenesis

Currently all no-ops. Update to persist/restore provider entries:

```go
func DefaultGenesisState() *types.GenesisState {
    return &types.GenesisState{
        Providers: []types.FibreProvider{},
    }
}

func ValidateGenesis(data *types.GenesisState) error {
    if data == nil {
        return fmt.Errorf("genesis state cannot be nil")
    }
    seen := make(map[string]bool)
    for _, p := range data.Providers {
        if p.ValidatorConsensusAddress == "" {
            return fmt.Errorf("provider has empty consensus address")
        }
        if seen[p.ValidatorConsensusAddress] {
            return fmt.Errorf("duplicate provider for %s", p.ValidatorConsensusAddress)
        }
        seen[p.ValidatorConsensusAddress] = true
        if p.Info.Host == "" {
            return fmt.Errorf("provider %s has empty host", p.ValidatorConsensusAddress)
        }
    }
    return nil
}

func InitGenesis(ctx sdk.Context, k keeper.Keeper, data *types.GenesisState) {
    for _, p := range data.Providers {
        consAddr, err := sdk.ConsAddressFromBech32(p.ValidatorConsensusAddress)
        if err != nil {
            panic(fmt.Errorf("invalid cons address in genesis: %w", err))
        }
        if err := k.SetFibreProviderInfo(ctx, consAddr, p.Info); err != nil {
            panic(err)
        }
    }
}

func ExportGenesis(ctx sdk.Context, k keeper.Keeper) *types.GenesisState {
    gs := &types.GenesisState{}
    _ = k.IterateFibreProviderInfo(ctx, func(consAddr sdk.ConsAddress, info types.FibreProviderInfo) bool {
        gs.Providers = append(gs.Providers, types.FibreProvider{
            ValidatorConsensusAddress: consAddr.String(),
            Info:                     info,
        })
        return false
    })
    return gs
}
```

### 3. `x/valaddr/keeper/genesis_test.go` -- Update Tests

Update the existing tests to cover the new providers field in genesis state. The "init genesis" test should set providers and verify they can be retrieved. The "export genesis" test should verify providers are exported.

---

## Talis Changes

### 4. `tools/talis/network.go` -- Genesis Modifiers for Fibre Escrow + ValAddr

**Changes:**

a) Add `txsimAddresses []string` field to `Network` struct.

b) In `AddValidator()`, after creating the txsim key and getting its address (`addr`), append to the new field:
```go
n.txsimAddresses = append(n.txsimAddresses, addr.String())
```

c) Add `AddFibreGenesisModifiers()` method on `Network` that registers BOTH genesis modifiers (escrow + valaddr). This must be called **after** all `AddValidator()` calls since modifiers are closures that capture the network state and read it lazily during `Export()`:
```go
func (n *Network) AddFibreGenesisModifiers(escrowPerAccount int64, fibrePort int) {
    n.genesis.WithModifiers(
        setFibreEscrows(n.ecfg.Codec, n.txsimAddresses, escrowPerAccount),
        setValAddrProviders(n, fibrePort),
    )
}
```

d) Add `setFibreEscrows` function (genesis modifier for fibre escrow):
```go
func setFibreEscrows(codec codec.Codec, signerAddresses []string, amountPerAccount int64) genesis.Modifier {
    return func(state map[string]json.RawMessage) map[string]json.RawMessage {
        // 1. Set fibre genesis state with escrow accounts
        fibreGenState := fibretypes.DefaultGenesis()
        coin := sdk.NewInt64Coin(appconsts.BondDenom, amountPerAccount)
        for _, addr := range signerAddresses {
            fibreGenState.EscrowAccounts = append(fibreGenState.EscrowAccounts, fibretypes.EscrowAccount{
                Signer:           addr,
                Balance:          coin,
                AvailableBalance: coin,
            })
        }
        state[fibretypes.ModuleName] = codec.MustMarshalJSON(fibreGenState)

        // 2. Fund the fibre module account in the bank genesis
        totalAmount := sdk.NewInt64Coin(appconsts.BondDenom, amountPerAccount * int64(len(signerAddresses)))
        moduleAddr := authtypes.NewModuleAddress(fibretypes.ModuleName)

        var bankGenState banktypes.GenesisState
        codec.MustUnmarshalJSON(state[banktypes.ModuleName], &bankGenState)
        bankGenState.Balances = append(bankGenState.Balances, banktypes.Balance{
            Address: moduleAddr.String(),
            Coins:   sdk.NewCoins(totalAmount),
        })
        bankGenState.Supply = bankGenState.Supply.Add(totalAmount)
        state[banktypes.ModuleName] = codec.MustMarshalJSON(&bankGenState)

        return state
    }
}
```

e) Add `setValAddrProviders` function (genesis modifier for validator fibre host registration). This closure captures `n` and reads `n.genesis.Validators()` + `n.validators` map lazily when the modifier runs during `Export()`:
```go
func setValAddrProviders(n *Network, fibrePort int) genesis.Modifier {
    return func(state map[string]json.RawMessage) map[string]json.RawMessage {
        vals := n.genesis.Validators()
        var providers []valaddrtypes.FibreProvider
        for _, v := range vals {
            ninfo, ok := n.validators[v.Name]
            if !ok || ninfo.IP == "" {
                continue
            }
            consAddr := sdk.ConsAddress(v.ConsensusKey.PubKey().Address())
            providers = append(providers, valaddrtypes.FibreProvider{
                ValidatorConsensusAddress: consAddr.String(),
                Info: valaddrtypes.FibreProviderInfo{
                    Host: fmt.Sprintf("%s:%d", ninfo.IP, fibrePort),
                },
            })
        }
        genState := &valaddrtypes.GenesisState{Providers: providers}
        state[valaddrtypes.ModuleName] = n.ecfg.Codec.MustMarshalJSON(genState)
        return state
    }
}
```

### 5. `tools/talis/genesis.go` -- Wire Up Modifiers in `createPayload`

In `createPayload()`, after the validator loop and before `n.InitNodes()`, add:
```go
n.AddFibreGenesisModifiers(4_999_999_999_999_999, 9091) // ~half the txsim balance, fibre port
```

This pre-funds each txsim account's escrow and registers all validators' fibre hosts at genesis. No runtime setup needed.

### 6. `tools/talis/main.go`

Add two new commands to `rootCmd.AddCommand(...)`:
```go
fibreTxsimCmd(),
throughputCmd(),
```

---

## New Files

### 7. `tools/talis/fibre_txsim.go` -- Fibre Spammer Command

New cobra command `fibre-txsim` that runs **locally**, connecting to a node's gRPC.

**Flags:**
- `-d / --directory` (string, default `.`) -- root dir with `config.json`
- `--grpc-endpoint` (string) -- gRPC endpoint; defaults to first validator IP:9091
- `--keyring-dir` (string) -- defaults to `<rootDir>/payload/<first-validator-name>`
- `--key-name` (string, default `txsim`) -- key name in keyring (must match escrow signer)
- `--blob-size` (int, default `1000000`) -- raw blob data size in bytes
- `--concurrency` (int, default `1`) -- number of parallel `Put()` goroutines
- `--duration` (duration, default `0`) -- run duration (0 = until Ctrl+C)
- `--namespace` (string, default `fibre-test`) -- blob namespace
- `--csv` (string) -- optional CSV output path

**RunE logic:**
1. Load config, resolve gRPC endpoint from config (first validator IP:9091) or flag
2. Set up encoding config via `encoding.MakeConfig(app.ModuleEncodingRegisters...)`
3. Open keyring via `keyring.New(app.Name, keyring.BackendTest, keyringDir, nil, encCfg.Codec)`
4. Create gRPC connection (pattern from `tools/submit-fibre-blob/main.go`)
5. Create `user.TxClient` via `user.SetupTxClient(ctx, kr, grpcConn, encCfg, user.WithDefaultAccount(keyName))`
6. Create fibre dependencies:
    - `fibregrpc.NewSetGetter(coregrpc.NewBlockAPIClient(grpcConn))`
    - `fibregrpc.NewHostRegistry(valaddrtypes.NewQueryClient(grpcConn))`
7. Create `fibre.Client` with `DefaultClientConfig()`, setting `ChainID` and `DefaultKeyName = keyName`
8. **No escrow deposit needed** -- already pre-funded at genesis
9. **Spam loop**: bounded concurrency via semaphore, fire-at-will
10. On Ctrl+C / timeout: print summary stats, optionally write CSV

**Spam loop design** (fire-at-will, not rate-limited):
```
sem = make(chan struct{}, concurrency)
loop:
  acquire sem (or ctx.Done)
  go func:
    defer release sem
    data = crypto/rand bytes of blobSize
    start = time.Now()
    result, err = client.Put(ctx, ns, data)
    record BlobResult{latency, txHash, height, err}
```

### 8. `tools/talis/throughput.go` -- Throughput Monitor Command

New cobra command `throughput` that polls blocks and measures fibre throughput.

**Flags:**
- `-d / --directory` (string, default `.`) -- root dir with config.json
- `--rpc-endpoint` (string) -- CometBFT RPC endpoint; defaults to first validator IP:26657
- `--duration` (duration, default `0`) -- monitor duration
- `--csv` (string) -- optional CSV output path

**Logic:**
1. Load config, resolve RPC endpoint (first validator IP:26657)
2. Create CometBFT HTTP RPC client (pattern from `status.go`)
3. Create encoding config + tx decoder via `encCfg.TxConfig.TxDecoder()`
4. Get current block height as starting point
5. Poll every 2 seconds for new blocks
6. For each new block:
    - Fetch block via `client.Block(ctx, &height)`
    - Decode all txs, filter for `*fibretypes.MsgPayForFibre`
    - Sum `msg.PaymentPromise.BlobSize` for all matching messages
    - Block time = `block.Header.Time - previousBlock.Header.Time`
    - Throughput = `totalBlobBytes / blockTime.Seconds()`
    - Print per-block: height, fibre tx count, blob bytes, throughput (bytes/sec and MB/sec), block time
7. On exit: print summary (avg throughput, total bytes, total duration), optionally write CSV

**Transaction decoding:**
```go
func decodeFibreMessages(txDecoder sdk.TxDecoder, rawTxs [][]byte) []*fibretypes.MsgPayForFibre {
    for _, rawTx := range rawTxs {
        tx, err := txDecoder(rawTx)
        if err != nil { continue }
        for _, msg := range tx.GetMsgs() {
            if pff, ok := msg.(*fibretypes.MsgPayForFibre); ok {
                result = append(result, pff)
            }
        }
    }
    return result
}
```

### 9. `tools/talis/fibre_helpers.go` -- Shared Types and Utilities

**Types:**
```go
type BlobResult struct {
    StartTime  time.Time
    EndTime    time.Time
    Latency    time.Duration
    TxHash     string
    Height     uint64
    BlobSize   int
    Error      error
}

type BlockThroughput struct {
    Height         int64
    Time           time.Time
    BlockDuration  time.Duration
    FibreBlobBytes uint64
    Throughput     float64  // bytes/sec
    TxCount        int
}
```

**Helper functions:**
- `setupFibreClient(ctx, grpcEndpoint, keyringDir, keyName, chainID, encCfg) (*fibre.Client, *user.TxClient, *grpc.ClientConn, error)` -- consolidates fibre client setup boilerplate from `tools/submit-fibre-blob/main.go`
- `decodeFibreMessages(txDecoder, rawTxs) []*fibretypes.MsgPayForFibre`
- `writeSpamResultsCSV(filename, []BlobResult) error`
- `writeThroughputCSV(filename, []BlockThroughput) error`

---

## Key Reference Files
- `tools/submit-fibre-blob/main.go` -- fibre client setup pattern
- `tools/latency-monitor/main.go` -- local spammer pattern with signal handling, CSV output
- `tools/talis/txsim.go` -- cobra command pattern
- `tools/talis/network.go` -- genesis/validator setup, `AddValidator()`, `Network` struct
- `tools/talis/genesis.go` -- `createPayload()` flow
- `test/util/genesis/modifier.go` -- `FundAccounts()` pattern for genesis bank balances
- `fibre/client.go` + `fibre/client_put.go` -- `NewClient()` and `Put()` API
- `x/fibre/types/genesis.go` -- `GenesisState` with `EscrowAccounts` field
- `x/fibre/keeper/genesis.go` -- `InitGenesis()` sets escrow accounts from genesis
- `x/valaddr/genesis.go` -- valaddr genesis (currently no-ops, to be updated)
- `x/valaddr/keeper/keeper.go` -- `SetFibreProviderInfo()`, `IterateFibreProviderInfo()`
- `proto/celestia/valaddr/v1/query.proto` -- `FibreProvider` and `FibreProviderInfo` types
- `test/util/genesis/accounts.go` -- `Validator` struct with `ConsensusKey` field

## No New Dependencies
All imports are already in `go.mod`.

## Workflow

```
1. talis init            -> Create directory structure + configs
2. talis add             -> Add validators to config
3. talis up              -> Spin up cloud instances
4. talis genesis         -> Generate genesis + payload (escrow + valaddr pre-populated)
5. talis deploy          -> Upload payload + start nodes
6. talis fibre-txsim     -> Spam fibre blobs (NEW, runs locally)
7. talis throughput      -> Monitor throughput (NEW, separate terminal)
8. talis download        -> Collect traces/logs
9. talis down            -> Tear down instances
```

## Verification
1. Build: `cd tools/talis && go build .`
