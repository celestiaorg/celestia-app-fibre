# Local Escrow Balance Cache for Fibre Server

## Context

**Problem**: When a validator receives a `PaymentPromise` via `UploadShard`, it calls `ValidatePaymentPromise` on the state machine (loopback gRPC). The state machine checks `AvailableBalance >= cost`, but this balance is only updated when a `MsgPayForFibre` is included in a block. If the same account sends N promises within a block, all N see the same stale balance and are accepted — enabling over-commitment of escrow funds.

**Goal**: Add a per-account balance cache in the fibre server that tracks local deductions across accepted promises, preventing over-acceptance within and across blocks. The 24h `WithdrawalDelay` guarantees that a queried balance is valid for ~23h (funds can't leave escrow faster than the delay), so the cache can avoid re-querying the state machine on every promise.

**Ref**: [GitHub Issue #27](https://github.com/celestiaorg/celestia-app-fibre/issues/27)

---

## Design Decisions

### DD1: Where does the cache live?
**Answer**: In the fibre server (`fibre/` package), as a field on `Server`. The state machine remains the source of truth; the cache is a local optimization.

### DD2: Do we still call `ValidatePaymentPromise` gRPC?
**Answer**: Yes. The gRPC call handles non-balance stateful checks (timestamp freshness, expiration, height window, not-already-processed) that require chain state. The cache **only** replaces the balance sufficiency check. The gRPC call happens first; the local balance deduction happens after it succeeds.

**Proposal**: We could make many validations local. For example, the timestamp could be checked against the block time. We could throw events on params/governance changes and listen for those events so that when they happen, we update immediately. The goal would be to have as much of `ValidatePaymentPromise` logic running locally as possible, reducing reliance on the gRPC round-trip for each promise. This could be a follow-up.

### DD3: How does the cache get seeded with the on-chain balance?
**Answer**: On cache miss, after the `ValidatePaymentPromise` gRPC call succeeds, make a separate `EscrowAccount` gRPC query to get the current `AvailableBalance`. The `EscrowAccount` request already exists and returns the balance.

### DD4: What is the cache TTL? (addresses parameter change fragility)
**Answer**: `TTL = WithdrawalDelay - PaymentPromiseTimeout` (default: 24h - 1h = 23h). Derived dynamically from chain params, never hardcoded. Params themselves are cached with a 10-minute TTL and re-fetched via `Params` gRPC query. Additionally, emit an event on TTL/param changes and listen for them so the cache can update immediately rather than waiting for the polling interval.

### DD5: When does the cache re-query the state machine?
Three triggers:
1. **No entry** for this account (first promise ever).
2. **TTL expired** (`time.Since(entry.fetchedAt) >= ttl`).
3. **Cached balance is zero** — re-query to pick up deposits that happened after the last fetch.

### DD6: How is inter-block synchronization handled?
**Answer**: All accepted payment promises are deducted from the cache regardless of whether they are ultimately included in a block. When the cache balance is renewed (on TTL expiry or zero-balance re-query), any pending (not-yet-included) promises must still be deducted from the refreshed balance. One approach is to read committed blocks to identify which payment promises were actually included on-chain, and maintain a set of pending promises whose deductions carry over across cache refreshes. Without this, malicious actors could benefit from free DA by submitting promises that pass the cache check but are never included on-chain, and then having the cache "forget" those deductions on refresh.

### DD7: How are promises that were not validated on-chain handled?
**Answer**: TBD.

### DD8: Thread-safety approach?
**Answer**: Single `sync.Mutex` on the cache. On cache hit: lock, deduct, unlock. On cache miss: lock to check, unlock, call gRPC (no lock held), lock to populate and deduct, unlock. Duplicate fetches on concurrent misses are harmless (both get the same block's state).

### DD9: Should we limit one promise per account per height?
**Answer**: No. This hurts UX for power users submitting multiple blobs. The balance cache is sufficient to prevent over-commitment without artificial rate limits.

### DD10: Should we require dramatically higher escrow minimums?
**Answer**: No. Same UX concern. The cache solves the problem without changing economic parameters.

### DD11: Can we skip `ValidatePaymentPromise` entirely and do all checks locally?
**Answer**: See DD2 for the decision. The long-term direction is to move as much validation logic as possible to run locally, with event-driven updates for chain state changes.

### DD12: What about deposits (balance increases) happening after cache was seeded?
**Answer**: The cache won't see new deposits until TTL expiry or zero-balance re-query. When `available` hits zero, the next promise triggers a re-fetch, which picks up any deposits. Documented as a known limitation.

---