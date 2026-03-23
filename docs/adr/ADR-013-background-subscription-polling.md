# ADR-013 — Background Subscription Polling

**Status:** Accepted  
**Date:** 2026-03-23  
**Segment:** 13

---

## Context

Segment 11C introduced `CachingProvider`, which wraps `RemoteProvider` and keeps a
per-`(service, eventType, tenantID)` in-memory cache of subscription lists for a
configurable TTL (default 30 s).

The cache uses **lazy eviction**: a fresh fetch is triggered only when a
`ListSubscriptions` call arrives after the cache entry has expired. This means:

1. **Cold-start latency** — every worker that dispatches the first event ever
   (or the first event after a TTL expiry) blocks on an outbound HTTP call to
   PhaseMirror-HQ before delivery can begin.
2. **Thundering herd on expiry** — under sustained event bursts, multiple
   concurrent dispatcher workers can all observe a stale cache entry
   simultaneously. Because `CachingProvider` holds the write lock only while
   storing, all of those workers race to call `inner.ListSubscriptions` in
   parallel, amplifying load on PhaseMirror-HQ at the exact moment the cache
   turns cold.
3. **No singleflight protection** — the current implementation does not
   coalesce concurrent misses; each caller independently issues an HTTP
   request when it finds the cache empty or expired.

For production event volumes the ideal steady-state is that the dispatch hot
path **never** blocks on a subscription fetch. Subscriptions change rarely
compared to how often events are dispatched.

---

## Decision

Extend `CachingProvider` with an **optional background refresh goroutine** that
proactively re-fetches every known cache key on a configurable poll interval.

Key design choices:

| Aspect | Choice | Rationale |
|---|---|---|
| Where the goroutine lives | Inside `CachingProvider` | Keeps the polling concern co-located with the cache; no new type needed |
| Trigger | Configurable `pollInterval`; `0` disables | Operators who don't need warm-cache guarantees don't pay for the goroutine |
| Keys to refresh | All keys that have been seen at least once (populated on first lazy miss) | Avoids polling for combinations that are never dispatched |
| Error handling | Log and continue; do **not** evict a still-valid entry on transient failure | A 503 from HQ during a poll should not disrupt active dispatch |
| Graceful stop | `Close()` method on `CachingProvider`; stops the goroutine and waits for it | Aligns with `Dispatcher.Close()` shutdown contract already in place |
| New env var | `AIISTECH_WEBHOOK_POLL_INTERVAL_SECONDS` (default: `0` = disabled) | Gives operators full control; TTL and poll interval are orthogonal |

### Alternatives considered

**A — Singleflight only**  
Use `golang.org/x/sync/singleflight` to coalesce concurrent misses so only one
goroutine calls HQ per key. Eliminates the thundering herd but does **not**
eliminate cold-start latency on the dispatch path. Chosen as a complementary
measure, not a substitute.

**B — Increase default TTL**  
A longer TTL (e.g. 5 min) reduces miss frequency without adding a goroutine.
Accepted as a user-configurable fallback but not the default because it
increases the lag between a subscription change in HQ and delivery starting to
the new URL.

**C — Push invalidation from HQ**  
HQ could send a webhook or SSE event to invalidate cache entries. Requires
protocol changes on the HQ side and a new inbound endpoint here. Deferred.

---

## Consequences

### Positive

- Dispatch hot path is **always a cache hit** once the first lazy miss has
  populated the entry and the background goroutine has taken over.
- PhaseMirror-HQ subscription API load becomes **predictable and steady**
  (`n_keys / pollInterval` requests/s) instead of bursty.
- Thundering herd on TTL expiry is eliminated.
- `Close()` on `CachingProvider` integrates cleanly with the existing shutdown
  sequence in `main.go` (dispatcher is closed before stores).

### Negative / trade-offs

- A non-zero `pollInterval` starts a long-lived goroutine per `CachingProvider`
  instance. In practice there is exactly one instance per process, so overhead
  is negligible.
- Operators must tune two related knobs (`AIISTECH_WEBHOOK_CACHE_TTL_SECONDS`
  and `AIISTECH_WEBHOOK_POLL_INTERVAL_SECONDS`). Documented defaults make this
  straightforward (`TTL=30s`, poll disabled by default).
- Adding `Close()` to `CachingProvider` means `main.go` must call it on
  shutdown **before** closing the dispatcher. The existing shutdown order
  already handles this: `disp.Close()` → `stores.CloseAll()`.

---

## Implementation plan (Segment 13)

1. **`internal/webhooks/caching_provider.go`** — add `pollInterval` field, a
   `stopCh chan struct{}` channel, and a `Close()` method. When `pollInterval > 0`,
   start a background goroutine in `NewCachingProvider` that ticks every
   `pollInterval` and calls `inner.ListSubscriptions` for every key currently in
   the cache, updating entries on success.
2. **`internal/webhooks/caching_provider_test.go`** — add tests for: background
   goroutine populates cache without a dispatch call; `Close()` stops the
   goroutine; transient provider error during poll does not evict valid entry.
3. **`cmd/server/main.go`** — read `AIISTECH_WEBHOOK_POLL_INTERVAL_SECONDS` and
   pass it to `NewCachingProvider`. Call `provider.Close()` in the shutdown
   sequence before `disp.Close()`.
4. **`README.md`** — add Segment 13 section; add env var to the table.
