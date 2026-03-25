# ADR-021: Webhook subscription caching (CachingProvider)

## Status
Accepted

## Context
`WorkerDispatcher.process` calls `Provider.ListSubscriptions` on every dispatched event. Under even modest load (tens of audit-write events per second) this produces a continuous stream of outbound HTTP calls to the PhaseMirror-HQ subscription API, which:

1. Adds latency to every dispatch cycle.
2. Places unnecessary load on the upstream API.
3. Makes the dispatcher fragile — a temporary upstream outage drops all webhook deliveries even when the subscription list has not changed.

Subscription data is inherently low-velocity (subscriptions are created or modified infrequently) and is the same for all concurrent workers for a given `(service, eventType, tenantID)` triplet.

## Decision
Add a `CachingProvider` in `internal/webhooks/caching_provider.go` that wraps any `Provider` with a per-key TTL cache.

### Design
- **Cache key**: `service + "\x00" + eventType + "\x00" + tenantID` — the three filter dimensions used by `ListSubscriptions`.
- **TTL**: configurable at construction time; defaults to 30 seconds when ≤ 0.
- **Errors are never cached**: a transient upstream failure does not poison future calls.
- **Singleflight coalescing**: concurrent cache misses for the same key are collapsed into one outbound fetch via `golang.org/x/sync/singleflight`. All waiters share the result. The inner fetch uses a detached background context with a 15-second timeout so that cancellation of the triggering request does not abort the shared fetch.
- **Invalidate**: the `Invalidate(service, eventType, tenantID)` method removes a specific cache entry on demand (useful for admin tooling or forced refresh).

### Wiring
`cmd/server/main.go` wraps `RemoteProvider` with `CachingProvider` when `AIISTECH_WEBHOOK_CACHE_TTL_SECONDS` is set to a positive integer. When the env var is absent or invalid, no caching layer is added and the original direct-fetch behaviour is preserved.

```
AIISTECH_WEBHOOK_CACHE_TTL_SECONDS=60   # cache subscriptions for 60 s
```

## Consequences
- Subscription API call rate drops from O(events/s) to O(1 / TTL) per unique key.
- A stale cache entry may serve outdated subscriptions for up to one TTL period after a subscription change at the upstream API. Operators should tune the TTL to balance freshness vs. call volume.
- `CachingProvider` satisfies the `Provider` interface, so it is transparent to `WorkerDispatcher` and can be composed with any future provider wrapper.
- `golang.org/x/sync v0.10.0` is now a direct module dependency (previously indirect via bbolt).
