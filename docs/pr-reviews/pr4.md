# PR #4 review — Mvp2/impl2

2026-06-18, mode: fix

(internal/transaction/store.go) Store.Put never purges expired entries from the map. Because entries are only removed when their own token is Taken, a workload that continuously stages mutations without executing/undoing (or with many abandoned tokens) will cause unbounded memory growth even though the tokens are no longer usable after TTL. Consider opportunistically deleting expired entries during Put to keep the store's memory bounded.

**fixed**
`Put` now opportunistically purges expired entries before inserting (added `purgeExpiredLocked`, called under the existing lock), so a workload that stages many tokens and never executes/undoes them no longer grows the store without bound. Added `TestStore_PutPurgesExpiredEntries`. Commit `c8cca69`.

---

(internal/transaction/store_test.go) These tests ignore Put errors. If crypto/rand fails (rare but possible), the test will behave oddly (empty tokens, misleading failure) instead of reporting the real problem. Please assert err is nil for clarity and to avoid masking failures.

**fixed**
`TestStore_UniqueTokens`, `TestStore_OneShotConsume`, and `TestStore_Expiry` now assert `err == nil` on every `Put` instead of discarding it. Commit `c8cca69`.

---

(internal/evals/anthropic.go) Anthropic API calls have no timeout/deadline. Because cmd/runevals uses context.Background(), a stalled network connection can hang the harness indefinitely. Consider wrapping ctx with a reasonable per-request timeout inside sendMessage so a run fails fast instead of hanging.

**fixed**
`sendMessage` now wraps the incoming ctx with a 60s `context.WithTimeout` before issuing the request, so a stalled connection fails that one call instead of hanging the whole run (`cmd/runevals` drives everything from `context.Background()`, which has no deadline of its own). Commit `c8cca69`.
