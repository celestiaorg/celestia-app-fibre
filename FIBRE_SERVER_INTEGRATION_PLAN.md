# Plan: Hook up Fibre Server to celestia-appd start

## Overview
Integrate the Fibre server into the `celestia-appd start` command so that validators automatically start the Fibre server when they run consensus nodes.

## Requirements

### Behavior Rules
1. **Validator in active set**: Start Fibre server ✓
2. **Validator outside active set**: Start Fibre server ✓
3. **Consensus node not in active set (no PrivValidator)**: Do NOT start server ✓

**Simplified Logic**: If the node has a PrivValidator configured, start the Fibre server. Otherwise, don't start it.

### Constraints
- PrivValidator must support any KMS backend (tmkms, horcrux, file)
- Configuration should be via CLI flags initially (config file can be added later)
- The start command is added via Cosmos SDK, so we may need to copy/paste the relevant code

## Prerequisites
- [ ] PR #50: `feat(fibre/validator): GrpcSetGetter` - merged
- [ ] PR #30: `feat(fibre): Server with UploadRows backed by Store` - merged

## Implementation Plan

### Phase 1: Create Fibre Server Factory Function

**Location**: `cmd/celestia-appd/cmd/fibre_server.go` (new file)

**Purpose**: Create a function that initializes and returns a Fibre server instance, similar to the pattern shown in the discussion.

**Dependencies needed**:
- `PrivValidator` - from CometBFT node
- `QueryClient` - from gRPC connection to the app
- `SetGetter` - from ConsensusReactor (using `validator.NewSetGetter`)
- `Store` - created with `NewMemoryStore` or `NewBadgerStore`
- `ServerConfig` - with CLI flags support

**Key considerations**:
- Need to handle both file-based and KMS-based PrivValidators
- Store type should be configurable (memory vs badger)
- Server config should support CLI flags for:
  - ChainID
  - BlockTime
  - Store path (for badger)
  - Store type (memory vs badger)
  - Server address/port

### Phase 2: Determine Validator Status

**Location**: `cmd/celestia-appd/cmd/fibre_server.go`

**Function**: `isValidatorNode(cfg *cmtcfg.Config) bool`

**Logic**:
- Check if `PrivValidatorKeyFile()` exists and is readable
- Return `true` if PrivValidator is configured, `false` otherwise
- This determines whether to start the Fibre server

**Note**: This simple check is sufficient because:
- If PrivValidator exists → node is a validator (in or out of active set) → start server
- If PrivValidator doesn't exist → node is not a validator → don't start server

### Phase 3: Integrate with Start Command (Non-Multiplexer Build)

**Location**: `cmd/celestia-appd/cmd/modify_root_command.go`

**Approach**: Since Cosmos SDK's `server.AddCommands` doesn't provide hooks for post-start logic, we have two options:

**Option A (Recommended)**: Copy and modify Cosmos SDK's start command logic
- Copy the start command implementation from Cosmos SDK
- Add Fibre server initialization after the CometBFT node starts
- Register the Fibre server with the gRPC server

**Option B**: Use a post-start hook or modify the server context
- Less invasive but may require changes to Cosmos SDK patterns

**Implementation steps**:
1. Create a custom start command handler similar to multiplexer pattern
2. In the handler, after starting the CometBFT node:
   - Check if node is validator (`isValidatorNode`)
   - If yes, initialize Fibre server
   - Register Fibre server with gRPC server
   - Start Fibre server in a goroutine

### Phase 4: Integrate with Start Command (Multiplexer Build)

**Location**: `cmd/celestia-appd/cmd/modify_root_command_multiplexer.go` and `multiplexer/cmd/start.go`

**Approach**: Similar to Phase 3, but integrate into the multiplexer's start flow.

**Implementation steps**:
1. Modify `multiplexer/cmd/start.go` to check for validator status
2. After `multiplexer.Start()` completes and CometBFT node is running:
   - Access the CometBFT node from multiplexer
   - Check if node is validator
   - Initialize and start Fibre server
   - Register with gRPC server

**Key access points**:
- CometBFT node: `multiplexer.cmNode` (need to expose or access via method)
- PrivValidator: from `cmNode.PrivValidator()`
- BlockAPI client: Get via `cmNode.ConfigureRPC()` which returns `core.Environment`, then create `coregrpc.NewBlockAPI(env)`
- gRPC server: from multiplexer's gRPC server instance (already has BlockAPI registered)

### Phase 5: Add CLI Flags

**Location**: `cmd/celestia-appd/cmd/root.go` (addStartFlags function)

**Flags to add**:
- `--fibre.enable` - Enable Fibre server (default: true for validators)
- `--fibre.store-type` - Store type: "memory" or "badger" (default: "badger")
- `--fibre.store-path` - Path for badger store (default: `<home>/data/fibre-store`)
   - `--fibre.address` - Address for Fibre gRPC server (default: "0.0.0.0:9096", only used if separate server needed)
- `--fibre.chain-id` - Chain ID (default: from config)
- `--fibre.block-time` - Expected block time (default: 6s)

### Phase 6: Server Initialization Logic

**Location**: `cmd/celestia-appd/cmd/fibre_server.go`

**Function**: `startFibreServer(ctx context.Context, isValidator bool, ...) error`

**Steps**:
1. Get CometBFT node instance
2. Get PrivValidator from node: `node.PrivValidator()`
3. Create QueryClient:
   - Get gRPC connection from client context
   - Create `fibretypes.NewQueryClient(conn)`
4. Create SetGetter:
   - Use `validator.NewGrpcGetter(blockAPIClient)` which is already available
   - Get BlockAPI client from CometBFT's gRPC environment (similar to multiplexer)
   - OR if PR #50 adds `NewSetGetter(reactor)`, use that instead
5. Create Store:
   - Based on `--fibre.store-type` flag
   - If "badger": `fibre.NewBadgerStore(storePath, cfg)`
   - If "memory": `fibre.NewMemoryStore(cfg)`
6. Create ServerConfig:
   - ChainID from flag or config
   - BlockTime from flag or config
   - StoreConfig with defaults
7. Create Server:
   - `fibre.NewServer(privVal, queryClient, valGet, store, cfg)`
8. Register with gRPC server:
   - **Primary approach**: Register with existing gRPC server
     - Get gRPC server instance from app/multiplexer
     - `types.RegisterFibreServer(grpcServer, fibreServer)`
     - If successful, return nil (server will be served by existing gRPC server)
   - **Fallback approach** (if registration fails or separate server needed):
     - Create new gRPC server on port 9096 (from `--fibre.address` flag)
     - Register Fibre service: `types.RegisterFibreServer(grpcServer, fibreServer)`
     - Start listening in a goroutine
     - Log that separate server is being used
9. Error handling:
   - If `isValidator == true` and server initialization fails, return error (prevent startup)
   - If `isValidator == false` and server initialization fails, log warning and return nil (allow startup)

### Phase 7: Handle KMS Backends

**Consideration**: PrivValidator should already support KMS backends (tmkms, horcrux) through CometBFT's PrivValidator interface. The Fibre server just needs to use whatever PrivValidator the node provides.

**No special handling needed** - CometBFT abstracts this away.

### Phase 8: Error Handling and Logging

**Requirements**:
- Log when Fibre server starts
- **Validator nodes**: If Fibre server fails to start, prevent node startup (return error)
- **Non-validator nodes**: Log error but allow node to start (fail gracefully)
- Log validator status detection
- Graceful shutdown of Fibre server on node stop

## File Structure

```
cmd/celestia-appd/cmd/
├── fibre_server.go          # New: Fibre server initialization logic
├── modify_root_command.go   # Modify: Add Fibre flags, integrate server
└── modify_root_command_multiplexer.go  # Modify: Integrate server for multiplexer

multiplexer/cmd/
└── start.go                 # Modify: Add Fibre server startup

multiplexer/abci/
└── multiplexer.go           # Possibly expose cmNode or add method
```

## Testing Plan

1. **Unit Tests**:
   - Test `isValidatorNode()` with various configs
   - Test Fibre server initialization with mock dependencies
   - Test CLI flag parsing

2. **Integration Tests**:
   - Test validator node starts Fibre server
   - Test non-validator node doesn't start Fibre server
   - Test Fibre server works with file-based PrivValidator
   - Test Fibre server shutdown on node stop

3. **Manual Testing**:
   - Start validator node and verify Fibre server is running
   - Start non-validator consensus node and verify Fibre server is NOT running
   - Test with different KMS backends (if available)

## Dependencies to Verify

1. **PR #50**: `feat(fibre/validator): GrpcSetGetter`
   - May add `validator.NewSetGetter(reactor)` function (or similar)
   - Currently `validator.NewGrpcGetter(blockAPIClient)` exists and can be used
   - Verify if PR #50 adds reactor-based getter or if gRPC-based getter is sufficient

2. **PR #30**: `feat(fibre): Server with UploadRows backed by Store`
   - Verify `fibre.NewServer()` signature matches plan
   - Verify Store implementations are available

## Implementation Order

1. ✅ Draft plan (this document)
2. ✅ Verify prerequisites (PRs #30 and #50) - Both merged
3. ✅ Create `fibre_server.go` with helper functions
4. ✅ Add CLI flags
5. ✅ Implement non-multiplexer integration
6. ✅ Implement multiplexer integration
7. ✅ Add tests (unit tests for `isValidatorNode`, graceful shutdown implemented)
8. ⏳ Manual testing
9. ⏳ Documentation

## Resolved Questions

1. **gRPC Server Integration**:
   - **Decision**: Try using the same gRPC server as the app first (register Fibre service alongside other services)
   - **Fallback**: If that doesn't work, use a separate gRPC server on port **9096**
   - **Port Selection**: Port 9096 is chosen because:
     - 9090 is used by celestia-app (default gRPC server)
     - 9095 is used by celestia-node
     - 9098 is used by celestia-core/CometBFT (gRPC server)
     - 9096 is available and close to other gRPC ports for consistency

2. **SetGetter Implementation**:
   - Currently `validator.NewGrpcGetter(blockAPIClient)` exists and uses BlockAPI gRPC client
   - PR #50 may add `NewSetGetter(reactor)` that works directly with ConsensusReactor
   - **Fallback**: Can use `NewGrpcGetter` with BlockAPI client from CometBFT's gRPC environment
   - This is already set up in the multiplexer (`coregrpc.NewBlockAPI(coreEnv)`)

3. **Store Location**:
   - **Decision**: Use `<home>/data/fibre-store` to keep it alongside other node data

4. **Error Handling**:
   - **Decision**: If the node is a validator, Fibre server failures should prevent node startup
   - Non-validator nodes can start without Fibre server (fail gracefully)
   - This ensures validators are properly configured before participating in consensus

5. **Cosmos SDK Version**:
   - **Decision**: Use `github.com/celestiaorg/cosmos-sdk v0.51.4`
   - Note: This is the Celestia fork of Cosmos SDK, not the upstream version
   - Copy the minimal necessary code from this version to avoid maintenance burden
   - Focus on the start command implementation and server initialization code

## Notes

- The discussion mentions copying Cosmos SDK start command code to avoid maintaining a fork. This is reasonable for a targeted change.
- The PrivValidator abstraction should handle all KMS backends automatically.
- Consider adding metrics/telemetry for Fibre server operations.
- Consider adding health check endpoint for Fibre server.
