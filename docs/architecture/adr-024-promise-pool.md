# ADR: Promise Pool

## Status

Proposed — 24 Oct 2025

## Context

The `promise_pool_codex` ADR proposes retaining every verified `PaymentPromise` in-memory so the application can replay those promises into the Cosmos SDK fork’s `PromiseState`. While that approach removes redundant stateless checks, it forces validators to keep a full copy of each promise locally and replay them after every block commit. We want an alternative that:

- Continues to rely on the baseapp-provided `PromiseState` to validate escrow sufficiency before validators serve Fibre blobs.
- Avoids storing every promise in the application memory; instead, operators can offload promise storage to an external “PromiseBank” service shared across validators.
- Keeps deterministic accounting so that `MsgPayForFibre` and timeout messages settle against the same reserved funds.

We therefore aggregate promise effects—escrow deductions per signer—into a compact state diff that can be re-applied after each block.

## Decision

1. Represent the delta between committed escrow balances and promise-adjusted balances as an in-memory `PromiseBalanceDiff` keyed by the escrow signer (single bonded denom `utia`).
2. Track per-promise reservations (hash, signer, amount, expiration) alongside the aggregated diff so expirations can release escrow even without on-chain timeouts.
3. When a new promise is validated, update both the diff and reservation index, then forward the payload to the `PromiseBank` implementation for persistence.
4. After each ABCI `Commit`, apply the diff to the fresh `PromiseState` and prune reservations whose retention window has elapsed.
5. When a promise finalizes on-chain (PFF or timeout) or expires, apply the opposite delta to the diff and instruct the `PromiseBank` to prune the payload.

## Implementation Sketch

### Data Model

We maintain two related structures: an aggregated per-signer diff and a reservation index keyed by promise hash. The diff allows quick lookups when validating new promises, while the reservation index records expiration timestamps so escrow can be released even if no timeout transaction is submitted.

```go
type PromiseBalanceDiff struct {
    mu           sync.RWMutex
    entries      map[string]sdk.Int              // signer -> reserved utia
    reservations map[string]ReservationMetadata  // hash -> metadata
}

type ReservationMetadata struct {
    Signer    string
    Amount    sdk.Int
    ExpiresAt time.Time
}

func (d *PromiseBalanceDiff) Add(signer string, coin sdk.Coin) {
    d.mu.Lock()
    defer d.mu.Unlock()
    if d.entries == nil {
        d.entries = make(map[string]sdk.Int)
    }
    current := d.entries[signer]
    d.entries[signer] = current.Add(coin.Amount)
}

func (d *PromiseBalanceDiff) Sub(signer string, coin sdk.Coin) {
    d.mu.Lock()
    defer d.mu.Unlock()
    if d.entries == nil {
        return
    }
    current := d.entries[signer]
    updated := current.Sub(coin.Amount)
    if updated.IsZero() {
        delete(d.entries, signer)
        return
    }
    d.entries[signer] = updated
}

func (d *PromiseBalanceDiff) Lookup(signer string) sdk.Int {
    d.mu.RLock()
    defer d.mu.RUnlock()
    if d.entries == nil {
        return sdk.ZeroInt()
    }
    return d.entries[signer]
}

func (d *PromiseBalanceDiff) TrackReservation(hash []byte, signer string, amount sdk.Int, expiresAt time.Time) {
    d.mu.Lock()
    defer d.mu.Unlock()
    if d.reservations == nil {
        d.reservations = make(map[string]ReservationMetadata)
    }
    d.reservations[hex.EncodeToString(hash)] = ReservationMetadata{Signer: signer, Amount: amount, ExpiresAt: expiresAt}
}

func (d *PromiseBalanceDiff) ReleaseReservation(hash []byte) ReservationMetadata {
    d.mu.Lock()
    defer d.mu.Unlock()
    if len(d.reservations) == 0 {
        return ReservationMetadata{}
    }
    key := hex.EncodeToString(hash)
    meta, ok := d.reservations[key]
    if ok {
        delete(d.reservations, key)
    }
    return meta
}

func (d *PromiseBalanceDiff) ExpireReservations(now time.Time) map[string]ReservationMetadata {
    d.mu.Lock()
    defer d.mu.Unlock()
    if len(d.reservations) == 0 {
        return nil
    }
    expired := make(map[string]ReservationMetadata)
    for hash, meta := range d.reservations {
        if now.After(meta.ExpiresAt) {
            expired[hash] = meta
            delete(d.reservations, hash)
            updated := d.entries[meta.Signer].Sub(meta.Amount)
            if updated.IsZero() {
                delete(d.entries, meta.Signer)
            } else {
                d.entries[meta.Signer] = updated
            }
        }
    }
    return expired
}
```

`TrackReservation`, `ReleaseReservation`, and `ExpireReservations` mutate both maps so the aggregated diff and per-promise metadata stay consistent.

### Persistence

#### Diff Store

The diff and reservation index must survive process restarts so validators do not approve duplicate promises or forget pending expirations. We persist them using the protobuf messages defined in `proto/celestia/fibre/v1/promise_state.proto`.

Persistence flow:

```go
const diffFilename = "promise_diff_snapshot.bin"

func (d *PromiseBalanceDiff) SaveToDisk(home string) error {
    snapshot := &fibretypes.PromiseDiffSnapshot{}
    d.mu.RLock()
    for signer, amount := range d.entries {
        snapshot.Balances = append(snapshot.Balances, &fibretypes.PromiseSignerBalance{
            Signer: signer,
            Amount: amount.String(),
        })
    }
    for hash, meta := range d.reservations {
        snapshot.Reservations = append(snapshot.Reservations, &fibretypes.PromiseReservation{
            Hash:          mustDecodeHash(hash),
            Signer:        meta.Signer,
            Amount:        meta.Amount.String(),
            ExpiresAtUnix: meta.ExpiresAt.Unix(),
        })
    }
    d.mu.RUnlock()
    bz, err := proto.Marshal(snapshot)
    if err != nil {
        return err
    }
    path := filepath.Join(home, diffFilename)
    return os.WriteFile(path, bz, 0o600)
}

func (d *PromiseBalanceDiff) LoadFromDisk(home string) error {
    path := filepath.Join(home, diffFilename)
    bz, err := os.ReadFile(path)
    if err != nil {
        if errors.Is(err, fs.ErrNotExist) {
            return nil
        }
        return err
    }
    var snapshot fibretypes.PromiseDiffSnapshot
    if err := proto.Unmarshal(bz, &snapshot); err != nil {
        return err
    }
    d.mu.Lock()
    defer d.mu.Unlock()
    d.entries = make(map[string]sdk.Int, len(snapshot.Balances))
    d.reservations = make(map[string]ReservationMetadata, len(snapshot.Reservations))
    for _, bal := range snapshot.Balances {
        amt, ok := sdk.NewIntFromString(bal.Amount)
        if !ok {
            return fmt.Errorf("invalid amount %q", bal.Amount)
        }
        d.entries[bal.Signer] = amt
    }
    for _, res := range snapshot.Reservations {
        amt, ok := sdk.NewIntFromString(res.Amount)
        if !ok {
            return fmt.Errorf("invalid reservation amount %q", res.Amount)
        }
        d.reservations[hex.EncodeToString(res.Hash)] = ReservationMetadata{
            Signer:    res.Signer,
            Amount:    amt,
            ExpiresAt: time.Unix(res.ExpiresAtUnix, 0).UTC(),
        }
    }
    return nil
}

func mustDecodeHash(hash string) []byte {
    bz, err := hex.DecodeString(hash)
    if err != nil {
        panic(err)
    }
    return bz
}
```

`CheckAndDiffPromise`, `OnPromiseSettled`, and any other mutations call `SaveToDisk` asynchronously (or batch with debounce) to keep the snapshot durable.

During application boot, we invoke `LoadFromDisk` before initializing the forked `PromiseState` so the diff is available for immediate re-application.

#### Promise Payload Persistence

`PromiseBank` will default to a file-backed implementation that writes each promise as a protobuf blob under the node’s `promise_store/` directory:

```go
type FilePromiseBank struct {
    root string
}

func (f *FilePromiseBank) Store(pp fibretypes.PaymentPromise) error {
    hash := fibretypes.HashPaymentPromise(pp)
    path := filepath.Join(f.root, hex.EncodeToString(hash)+".bin")
    bz, err := proto.Marshal(&pp)
    if err != nil {
        return err
    }
    return os.WriteFile(path, bz, 0o600)
}

func (f *FilePromiseBank) Remove(hash []byte) error {
    path := filepath.Join(f.root, hex.EncodeToString(hash)+".bin")
    if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
        return err
    }
    return nil
}
```

This keeps promise retention concerns out of consensus; operators can replace the implementation with distributed storage if desired.

### Application Flow

```go
func (app *CelestiaApp) CheckAndDiffPromise(pp fibretypes.PaymentPromise) error {
    ctx := app.BaseApp.PromiseState().Context()
    signer := app.FibreKeeper.SignerAddressFromPubKey(pp.SignerPublicKey)

    escrow, found := app.FibreKeeper.GetEscrowAccount(ctx, signer)
    if !found {
        return fibretypes.ErrEscrowNotFound
    }

    cost := app.FibreKeeper.CalculatePromiseCost(ctx, pp)
    // Apply current diff to get effective available balance.
    reserved := app.promiseDiff.Lookup(signer.String())
    effective := escrow.AvailableBalance.Amount.Sub(reserved)
    if effective.LT(cost.Amount) {
        return fibretypes.ErrInsufficientEscrow
    }

    app.promiseDiff.Add(signer.String(), cost)
    retention := app.FibreKeeper.PaymentPromiseRetentionWindow(ctx)
    expiresAt := pp.CreationTimestamp.AsTime().Add(retention)
    hash := fibretypes.HashPaymentPromise(pp)
    app.promiseDiff.TrackReservation(hash, signer.String(), cost.Amount, expiresAt)
    if err := app.promiseDiff.SaveToDisk(app.homeDir); err != nil {
        return err
    }
    return app.PromiseBank.Store(pp) // async or best-effort depending on implementation
}

func (app *CelestiaApp) ApplyDiffToPromiseState() {
    ctx := app.BaseApp.PromiseState().Context()
    now := ctx.BlockTime()
    expired := app.promiseDiff.ExpireReservations(now)
    for hashStr, meta := range expired {
        hashBytes, err := hex.DecodeString(hashStr)
        if err != nil {
            app.Logger().Error("failed to decode expired promise hash", "hash", hashStr, "error", err)
            continue
        }
        if err := app.PromiseBank.Remove(hashBytes); err != nil {
            app.Logger().Error("failed to prune expired promise payload", "hash", hashStr, "error", err)
            continue
        }
        app.Logger().Info("expired promise reservation released", "signer", meta.Signer, "amount", meta.Amount.String())
    }
    if len(expired) > 0 {
        if err := app.promiseDiff.SaveToDisk(app.homeDir); err != nil {
            app.Logger().Error("failed to persist expired promise diff", "error", err)
        }
    }
    app.promiseDiff.mu.RLock()
    for signer, amount := range app.promiseDiff.entries {
        escrow, found := app.FibreKeeper.GetEscrowAccount(ctx, signer)
        if !found {
            app.Logger().Error("missing escrow for diff", "signer", signer)
            continue
        }
        coin := sdk.NewCoin(app.FibreKeeper.BondDenom(ctx), amount)
        escrow.AvailableBalance = escrow.AvailableBalance.Sub(coin)
        app.FibreKeeper.SetEscrowAccount(ctx, escrow)
    }
    app.promiseDiff.mu.RUnlock()
}

func (app *CelestiaApp) OnPromiseSettled(hash []byte, signer string, cost sdk.Coin) {
    app.promiseDiff.Sub(signer, cost)
    app.promiseDiff.ReleaseReservation(hash)
    if err := app.promiseDiff.SaveToDisk(app.homeDir); err != nil {
        app.Logger().Error("failed to persist promise diff", "error", err)
    }
    app.PromiseBank.Remove(hash)
}
```

`ApplyDiffToPromiseState` must run at the end of each block using the header time supplied by baseapp. It first calls `ExpireReservations` with that timestamp—before mutating the promise state—so escrow is freed for promises older than the retention window. The same method re-applies the aggregated diff and removes payloads from the `PromiseBank`, ensuring validators reclaim escrow even when no timeout message arrives.

### Application Lifecycle

- **Promise Validation (CheckTx / RPC):** `CheckAndDiffPromise` validates escrow sufficiency using the committed state minus current diff. On success the diff increments, the reservation is recorded, and the promise is sent to the external store.
- **Commit:** After `Commit`, call `ApplyDiffToPromiseState` with the header time so expired reservations are pruned before re-applying the diff, ensuring the next block’s `PromiseState` reflects only live promises.
- **DeliverTx:** `MsgPayForFibre` and `MsgPaymentPromiseTimeout` invoke `OnPromiseSettled` with the promise hash, signer, and cost to keep the diff in sync with on-chain settlements.
- **PromiseBank Interface:**

```go
type PromiseBank interface {
    Store(pp fibretypes.PaymentPromise) error
    Remove(hash []byte) error
}
```

The default implementation can be a no-op during testing; production operators can plug in gRPC, Kafka, or any transport that shares promises among validators. `ApplyDiffToPromiseState` calls `PromiseBank.Remove` for expired reservations so the external store stays consistent with on-node state.

## Testing Strategy

1. **Diff Application:** Validate that multiple promises targeting the same signer accumulate in the diff and that `ApplyDiffToPromiseState` correctly reduces available balances.
2. **Escrow Sufficiency:** Ensure verifying a promise applies the current diff before checking funds, preventing double spending even without storing the promise.
3. **Commit Rebuild:** Simulate `Commit` followed by a fresh `PromiseState` and confirm `ApplyDiffToPromiseState` reproduces previous adjustments deterministically.
4. **Persistence Roundtrip:** Persist the diff and reservations to disk, reload them on a new app instance, and ensure balances and expirations survive restarts.
5. **Expiration Handling:** Advance time beyond the retention window, run `ApplyDiffToPromiseState`, and confirm reservations drop, escrow is restored, and the `PromiseBank` receives removal calls.
6. **Settlement Reconciliation:** Process a PFF/timeout and verify the diff decrements, allowing new promises once funds free up.
7. **External Store Interaction:** Mock the `PromiseBank` to ensure failures are surfaced and optional retry/backoff logic is exercised.
8. **Concurrency:** Stress-test simultaneous validations for the same signer to confirm locking prevents inconsistent diff updates.
9. **Missing Escrow Handling:** Ensure diff application logs but tolerates missing escrows (e.g., after account deletion) without panicking.

## Consequences

- **Positive:** Memory usage becomes proportional to the number of active signers rather than outstanding promises; replay work after Commit is constant per signer. Validators can leverage distributed promise stores without modifying the application layer.
- **Negative:** Requires precise accounting of promise costs to avoid drift between diff and on-chain settlements. Transient failures in `PromiseBank` must be handled to avoid losing promises.
- **Mitigations:** Expose metrics for diff totals, audit diff reconciliation in integration tests, and provide operator tooling to rebuild diffs from the external promise store if needed.
