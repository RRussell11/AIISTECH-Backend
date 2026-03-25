# ADR-036: StoreProvider Dispatcher — live delivery from local subscriptions

**Status:** Accepted
**Date:** 2026-03-25
**Segment:** 36

---

## Context

Segments 34–35 added a full subscription management CRUD API
(`POST / GET / PATCH / DELETE /sites/{site_id}/webhooks/subscriptions`), storing
subscriptions in a per-site bbolt bucket via `StoreProvider`.  However, those
subscriptions were never consulted by the running `WorkerDispatcher`; the
dispatcher still required `AIISTECH_WEBHOOK_BASE_URL` to point at a PhaseMirror-HQ
daemon.

The gap: operators can create subscriptions locally, but webhook events are still
routed through the remote provider.  Closing this gap is the primary goal of
Segment 36.

---

## Decision

### New type: `StoreRegistryProvider`

`internal/webhooks/store_registry_provider.go` introduces `StoreRegistryProvider`,
a `Provider` implementation backed by a `*storage.Registry`.

```
AIISTECH_WEBHOOK_STORE_PROVIDER=true
      │
      ▼
WorkerDispatcher ──► StoreRegistryProvider ──► stores.Open(siteID)
                                                      │
                                                      ▼
                                               StoreProvider.ListSubscriptions
                                               (reads webhook_subscriptions bucket)
```

On every `ListSubscriptions` call the provider:

1. Reads the site ID from context (set by `WithSiteID` in the dispatch loop).
2. Opens (or returns the cached) bbolt store for that site via `*storage.Registry`.
3. Delegates to `NewStoreProvider(st).ListSubscriptions(...)` for the actual lookup.

### Context propagation: `WithSiteID` / `siteIDFromContext`

Because `Provider.ListSubscriptions` does not carry a `siteID` parameter (changing
the interface would break all existing implementations), the site ID is threaded
through `context.Context` using an unexported key type.

`WorkerDispatcher.process` already has `evt.SiteID`; a one-liner now injects it:

```go
if evt.SiteID != "" {
    ctx = WithSiteID(ctx, evt.SiteID)
}
```

This is additive — existing providers (`RemoteProvider`, `CachingProvider`) ignore
the extra context value, so no existing behaviour changes.

### Env-var wiring in `cmd/server/main.go`

`AIISTECH_WEBHOOK_STORE_PROVIDER=true` is a new opt-in flag:

| Env var | Effect |
|---------|--------|
| `AIISTECH_WEBHOOK_STORE_PROVIDER=true` | Use `StoreRegistryProvider`; start dispatcher without a remote URL |
| `AIISTECH_WEBHOOK_BASE_URL=<url>` | Use `RemoteProvider` (unchanged) |
| Neither | No dispatcher; webhook delivery is disabled |

The two paths are mutually exclusive (`if / else if`).  `AIISTECH_WEBHOOK_STORE_PROVIDER`
takes precedence; the remote-URL path is entered only when the store-provider flag is
absent or false.

Both paths honour the existing `AIISTECH_WEBHOOK_CB_FAILURE_THRESHOLD` and
`AIISTECH_WEBHOOK_CB_OPEN_DURATION_SECONDS` circuit-breaker env vars.

---

## Consequences

### Positive

* **Subscriptions created via the management API are live** — the dispatcher consults
  them on every event without any remote call or additional configuration.
* **Zero-dependency deployment** — no PhaseMirror-HQ daemon required; a single
  AIISTECH-Backend process manages both subscription storage and delivery.
* **Backwards compatible** — the `Provider` interface is unchanged; all existing
  providers and tests are unaffected.
* **Circuit breaker, DLQ, expvar metrics** all work unchanged with the new path.

### Negative / trade-offs

* **Per-process subscription scope** — subscriptions are stored in the local bbolt
  file; a second instance of the server will not share them unless a shared store
  is mounted.
* **No cross-site fan-out** — a single dispatch event is routed to the subscriptions
  of its originating site only (`evt.SiteID`).  Cross-site delivery requires
  creating matching subscriptions on each site.

---

## Env var reference (Segment 36 additions)

| Variable | Default | Description |
|----------|---------|-------------|
| `AIISTECH_WEBHOOK_STORE_PROVIDER` | _(unset)_ | Set to `true` to activate the bbolt-backed dispatcher |

All other env vars (`AIISTECH_WEBHOOK_CB_FAILURE_THRESHOLD`, `AIISTECH_SERVICE_NAME`,
etc.) continue to apply when using the store-provider path.
