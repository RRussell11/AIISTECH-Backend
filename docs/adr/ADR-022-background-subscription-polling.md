# ADR-022: Background subscription polling (CachingProvider poll goroutine)

## Status
Accepted

## Context
ADR-021 introduced `CachingProvider`, a lazy TTL cache around the subscription
fetch path. Lazy eviction works well under steady load but has a known
weakness: when a cache entry expires under continuous traffic, the next caller
suffers a cold-start fetch latency spike (even with singleflight coalescing,
every 30-second boundary briefly touches the upstream API on the hot path).

For operators running the server at high event rates or with strict latency
budgets, proactively refreshing entries *before* they expire eliminates
these periodic spikes entirely.

## Decision
Extend `CachingProvider` with an optional background poll goroutine (ADR-022,
Segment 22).

### Design

**New `NewCachingProvider` signature**

```go
func NewCachingProvider(inner Provider, ttl time.Duration, pollInterval time.Duration) *CachingProvider
```

- `pollInterval > 0` — a background goroutine is started that ticks at
  `pollInterval` and calls `refreshAll()` on each tick.
- `pollInterval ≤ 0` — no goroutine is started; the provider behaves exactly
  as it did in ADR-021 (lazy TTL eviction only).

**`refreshAll()`**

Iterates over all keys currently present in the cache, splits each key on the
`\x00` separator to recover `(service, eventType, tenantID)`, and calls the
inner provider with a fresh `context.WithTimeout` (15 s budget). On error the
existing cache entry is preserved (stale-on-error); the refresh error is
silently discarded so that a transient upstream outage does not degrade the
served result.

**`Close()`**

Signals the poll goroutine via `stopCh` and blocks on `doneCh` until the
goroutine acknowledges the stop. `Close()` on a provider created with
`pollInterval ≤ 0` is a no-op (neither channel is initialised).

### Wiring

`cmd/server/main.go` reads `AIISTECH_WEBHOOK_POLL_INTERVAL_SECONDS`:

```
AIISTECH_WEBHOOK_CACHE_TTL_SECONDS=60    # cache TTL
AIISTECH_WEBHOOK_POLL_INTERVAL_SECONDS=30 # refresh every 30 s (< TTL)
```

A concrete `*webhooks.CachingProvider` reference is retained alongside the
`webhooks.Dispatcher` so that `cachingProvider.Close()` can be called on
graceful shutdown *before* `disp.Close()`. This ordering guarantees that the
poll goroutine stops issuing inner fetches before the dispatcher (and
ultimately the HTTP server) tears down.

## Consequences
- **No TTL spikes**: under continuous load, cache entries are refreshed before
  they expire, so every `ListSubscriptions` call is a cache hit.
- **Subscription changes propagate faster**: with a 30-second poll interval
  (vs. a 60-second TTL) subscription additions or removals are visible within
  one poll cycle instead of waiting for full TTL expiry.
- **No new dependencies**: the goroutine uses only the Go standard library
  (`time.Ticker`, channels, `context`).
- **Backward-compatible**: `pollInterval=0` preserves the exact ADR-021
  behaviour; `AIISTECH_WEBHOOK_POLL_INTERVAL_SECONDS` defaults to 0 when
  absent or invalid.
- **Stale-on-error**: a transient upstream failure during a background refresh
  does not evict the existing entry. This is consistent with ADR-021's
  "errors are never cached" rule: we err on the side of stale data rather
  than no data.
