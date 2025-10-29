# BeginBlock Withdrawal Processing Implementation

## Overview

This document describes the implementation of automatic withdrawal processing in the x/fibre module's BeginBlocker, as specified in the [sdk_module.md](../../../fibre-da-spec/sdk_module.md) specification.

## What Was Implemented

### 1. Dual-Index Storage (`x/fibre/types/keys.go`)

Withdrawals are stored using a dual-index pattern for efficient querying:

#### Primary Index: Withdrawals by Signer

- **Key Prefix**: `0x02` (`WithdrawalsBySignerKeyPrefix`)
- **Key Format**: `0x02/{signer}/{requested_timestamp}`
- **Purpose**: User queries - "show me my pending withdrawals"

#### Secondary Index: Withdrawals by Available Time

- **Key Prefix**: `0x05` (`WithdrawalsByAvailableKeyPrefix`)
- **Key Format**: `0x05/{available_at_timestamp}/{signer}`
- **Purpose**: BeginBlocker processing - "show me all withdrawals ready to process"

Both indexes store the complete `Withdrawal` struct to enable O(log n) lookups.

**Important Design Note**: We store the **complete `Withdrawal` struct** (not just the amount) to avoid bugs when the `WithdrawalDelay` parameter changes via governance. The `Withdrawal` struct now includes an `AvailableTimestamp` field that is set at creation time. This makes the withdrawal self-contained and immune to parameter changes - the withdrawal knows exactly when it should be processed, regardless of what the current `WithdrawalDelay` parameter value is.

### 2. Keeper Methods (`x/fibre/keeper/keeper.go`)

Modified withdrawal management methods to transparently handle dual-index updates:

- `SetWithdrawal(ctx, withdrawal)` - Stores the withdrawal in **both** indexes atomically using `withdrawal.AvailableTimestamp`
- `DeleteWithdrawal(ctx, withdrawal)` - Removes the withdrawal from **both** indexes atomically using `withdrawal.AvailableTimestamp`
- `GetWithdrawalsByAvailableIterator(ctx, upToTime)` - Returns an iterator for all withdrawals available up to the given time
- `ParseWithdrawalsByAvailableKey(key)` - Parses the availability timestamp and signer from a key

#### Design Choice

Both `SetWithdrawal` and `DeleteWithdrawal` use the `AvailableTimestamp` field from the `Withdrawal` struct itself. This makes the API clean and eliminates the possibility of using the wrong timestamp - the withdrawal is self-contained and carries its own availability time.

### 3. Updated Withdrawal Request (`x/fibre/keeper/msg_server.go`)

Modified `RequestWithdrawal` message handler to:

1. Compute `availableAt` from current `WithdrawalDelay` parameter
2. Create `Withdrawal` struct with both `RequestedTimestamp` and `AvailableTimestamp` set
3. Call `SetWithdrawal(ctx, withdrawal)` which stores to both indexes atomically

The single `SetWithdrawal` call ensures atomicity - both indexes are updated together, preventing inconsistencies:

- Primary: `withdrawals_by_signer/{signer}/{requested_timestamp}` → Full `Withdrawal` struct (for user queries)
- Secondary: `withdrawals_by_available/{available_timestamp}/{signer}` → Full `Withdrawal` struct (for BeginBlocker processing)

### 4. BeginBlocker Implementation (`x/fibre/keeper/abci.go`)

Created a new file implementing automatic state transitions:

```go
func (k Keeper) BeginBlocker(ctx sdk.Context) error {
    // Process available withdrawals first (affects escrow balances)
    if err := k.processAvailableWithdrawals(ctx); err != nil {
        return err
    }

    // TODO: Prune old processed promises (cleanup operation)
    // This will be implemented when payment promise pruning is added

    return nil
}
```

The `processAvailableWithdrawals()` function:

1. Gets the current block time
2. Iterates through the available withdrawal index up to current time
3. For each available withdrawal:
   - Deserializes the full `Withdrawal` struct from the index
   - Transfers funds from module account to user account via `SendCoinsFromModuleToAccount`
   - Decreases the escrow account's total balance
   - Removes the withdrawal from both indexes using `withdrawal.RequestedTimestamp` (not computed!)
   - Emits `EventWithdrawFromEscrowExecuted`
4. Stops when reaching withdrawals not yet available
5. Gracefully handles errors (logs and continues processing other withdrawals)

### 5. Module Wiring (`x/fibre/module.go`)

Added BeginBlock method to the AppModule:

```go
func (am AppModule) BeginBlock(ctx sdk.Context) error {
    return am.keeper.BeginBlocker(ctx)
}
```

The module is already registered in the app's BeginBlocker order in `app/modules.go` (line 111), so this method will be called automatically.

### 6. Comprehensive Tests (`x/fibre/keeper/abci_test.go`)

Created three test cases to verify BeginBlocker functionality:

1. **TestBeginBlocker_ProcessAvailableWithdrawals** - Tests basic withdrawal processing:
   - Withdrawals are NOT processed before the delay period
   - Withdrawals ARE processed after the delay period
   - Escrow balances are correctly updated
   - Withdrawals are deleted from storage

2. **TestBeginBlocker_ProcessMultipleWithdrawals** - Tests processing multiple withdrawals from different accounts in a single block

3. **TestBeginBlocker_WithdrawalStaggeredTimes** - Tests that withdrawals with different availability times are processed correctly:
   - Only withdrawals past their delay are processed
   - Future withdrawals remain pending

All tests pass successfully.

## How It Works

### Withdrawal Lifecycle

1. **Request** (via `MsgRequestWithdrawal`):
   - User submits withdrawal request at time T
   - Available balance is immediately decreased (funds locked)
   - Withdrawal is stored in both indexes:
     - `withdrawals/{signer}/{T}`
     - `available_withdrawals/{T + 24h}/{signer}`

2. **Waiting Period**:
   - BeginBlocker runs every block but finds no withdrawals with `available_at <= current_time`
   - Total balance remains in escrow account
   - Available balance remains reduced

3. **Processing** (automatic at T + 24h):
   - BeginBlocker detects withdrawal is available
   - Transfers funds from module account to user account
   - Decreases total escrow balance
   - Removes withdrawal from both indexes
   - Emits event

### Time Complexity

- **Insertion**: O(log n) - store in sorted index
- **Processing**: O(m) where m is number of available withdrawals (typically small)
- **Query**: O(k) where k is number of withdrawals for a signer

### Safety Properties

1. **Atomicity**: All operations within a withdrawal processing are atomic (within same block)
2. **Idempotency**: Withdrawals are removed after processing, preventing double-processing
3. **Error Handling**: Individual withdrawal failures don't stop processing of other withdrawals
4. **Balance Consistency**: Available balance is locked immediately, total balance updated only after delay

## Future Work

The BeginBlocker currently has a TODO comment for implementing payment promise pruning, which will be added when that feature is developed according to the spec.

## References

- Specification: [fibre-da-spec/sdk_module.md](../../../fibre-da-spec/sdk_module.md) (lines 527-544)
- Withdrawal Flow Diagram: [fibre-da-spec/sdk_module.md](../../../fibre-da-spec/sdk_module.md) (lines 127-159)
