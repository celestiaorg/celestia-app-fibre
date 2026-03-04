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

**Full list of validations in `ValidatePaymentPromise`** (from `keeper.go:313-374`):

1. **Timestamp not too old**: `creationTime > blockTime - WithdrawalDelay`. Needs `blockTime`, `params.WithdrawalDelay`. Could be local — cache block time per block, cache params via `EventUpdateFibreParams`.

2. **Not expired**: `blockTime < creationTime + PaymentPromiseTimeout`. Needs `blockTime`, `params.PaymentPromiseTimeout`. Could be local — same as above.

3. **Height not too far in past**: `currentHeight - promiseHeight <= PaymentPromiseHeightWindow`. Needs `blockHeight`, `params.PaymentPromiseHeightWindow`. Could be local — cache block height per block.

4. **Height not too far in future**: `promiseHeight <= currentHeight + 1`. Needs `blockHeight`. Could be local — same.

5. **Not already processed**: `IsPaymentPromiseProcessed()`. Needs processed payments KV store. Harder — need to track which promises have been included on-chain. Could read blocks or listen for `EventPayForFibre`/`EventPaymentPromiseTimeout` events.

6. **Escrow account exists**. Needs escrow accounts KV store. Could be cached, seeded on first query, invalidated on deposits/withdrawals.

7. **Sufficient available balance**: `AvailableBalance >= cost`. Needs escrow account balance. This is exactly what the escrow cache solves.

Checks 1-4 only need `blockTime`, `blockHeight`, and `params` — all refreshable once per block. Check 5 is the hardest to make local. Checks 6-7 are what the escrow cache addresses. The proposal is to query this information once per block instead of once per-payment promise.

### DD3: How does the cache get seeded with the on-chain balance?
**Answer**: On cache miss, after the `ValidatePaymentPromise` gRPC call succeeds, make a separate `EscrowAccount` gRPC query to get the current `AvailableBalance`. The `EscrowAccount` request already exists and returns the balance.

### DD4: What is the cache TTL? (addresses parameter change fragility)
**Answer**: `TTL = WithdrawalDelay - PaymentPromiseTimeout` (default: 24h - 1h = 23h). Derived dynamically from chain params, never hardcoded. Params are refreshed once per block by listening for the existing `EventUpdateFibreParams` event (already emitted in `msg_server.go:289` when `UpdateFibreParams` is called — includes the full `Params` struct). No new events need to be added.

To avoid a race condition between a parameter change and promise processing, the cache must **lock on every new block**, update `blockTime`, `blockHeight`, params (and any other per-block state), and only then unlock to resume processing promises. This ensures no promise is validated against stale state mid-block transition.

### DD5: When does the cache re-query the state machine?
Three triggers:
1. **No entry** for this account (first promise ever).
2. **TTL expired** (`time.Since(entry.fetchedAt) >= ttl`).
3. **Cached balance is zero** — re-query to pick up deposits that happened after the last fetch.

### DD6: How is inter-block synchronization handled?
**Answer**: All accepted payment promises are deducted from the cache regardless of whether they are ultimately included in a block. When the cache balance is renewed (on TTL expiry or zero-balance re-query), any pending (not-yet-included) promises must still be deducted from the refreshed balance. One approach is to read committed blocks to identify which payment promises were actually included on-chain, and maintain a set of pending promises whose deductions carry over across cache refreshes. Without this, malicious actors could benefit from free DA by submitting promises that pass the cache check but are never included on-chain, and then having the cache "forget" those deductions on refresh.

### DD7: How are promises that were not validated on-chain handled?

A payment promise accepted by the fibre server can be rejected on-chain via `MsgPayForFibre` or `MsgPaymentPromiseTimeout` for any of the following reasons:

**Stateless rejections** (from `payment_promise.go:121-171`):
1. Invalid signer key (not 33-byte secp256k1 public key)
2. Empty or oversized chain ID
3. Zero upload size
4. Zero creation timestamp
5. Invalid signature length (not 64 bytes)
6. Zero height
7. Signature verification fails

**Stateful rejections for `MsgPayForFibre`** (from `keeper.go:313-374`, `msg_server.go:124-199`):
8. Timestamp too old: `creationTime <= blockTime - WithdrawalDelay`
9. Promise expired: `blockTime >= creationTime + PaymentPromiseTimeout`
10. Height too far in past: `currentHeight - promiseHeight > PaymentPromiseHeightWindow`
11. Height too far in future: `promiseHeight > currentHeight + 1`
12. Already processed (promise hash in processed payments store)
13. Escrow account not found
14. Insufficient balance (`Balance < paymentAmount`)
15. Insufficient available balance (`AvailableBalance < paymentAmount`)
16. Invalid validator sign bytes
17. Validator signature validation fails (can't get historical validator set, invalid signatures, or < 2/3 voting power)

**Additional rejection for `MsgPaymentPromiseTimeout`** (checks 9-11 are skipped):
18. Not yet timed out: `blockTime < expirationTime` (the inverse of check 9)

**Answer**: So, if we implement the local validation of the payment promises. We can be sure that any payment promise that's submitted onchain and has enough signatures is valid. And we don't need to worry about this case. For the expired promises, that's handled in another issue.

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