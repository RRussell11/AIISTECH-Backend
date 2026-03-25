# ADR-034: Webhook Subscription Management HTTP API

## Status
Accepted

## Context

The webhook delivery pipeline (ADR-012 through ADR-031) relies on the
external PhaseMirror-HQ daemon to supply subscription records via the
`RemoteProvider`.  Operators who want to manage subscriptions without
deploying or connecting to PhaseMirror-HQ have no way to register, inspect,
or remove subscriptions through the AIISTECH-Backend API itself.

Use cases include:

- **Development and testing** — register a local receiver URL without
  standing up the full PhaseMirror-HQ stack.
- **Edge / air-gapped deployments** — manage subscriptions entirely within
  the site, with no outbound dependency.
- **Operational scripting** — create or delete subscriptions via
  `curl` / CI pipelines without a separate service call.

## Decision

Add a local subscription store backed by each site's bbolt database and
expose four HTTP endpoints for CRUD management.

### `StoreProvider`

```go
// internal/webhooks/store_provider.go
type StoreProvider struct { /* … */ }

func NewStoreProvider(store storage.Store) *StoreProvider
func (p *StoreProvider) ListSubscriptions(ctx, service, eventType, tenantID) ([]Subscription, error) // Provider interface
func (p *StoreProvider) Create(ctx, sub Subscription) (Subscription, error)
func (p *StoreProvider) Get(ctx, id string) (Subscription, error)
func (p *StoreProvider) Delete(ctx, id string) error
```

`StoreProvider` satisfies the existing read-only `Provider` interface, which
means it can be passed directly to `WorkerDispatcher` as a drop-in replacement
for (or complement to) `RemoteProvider`.  The `Provider` interface methods are
deliberately kept read-only; Create/Get/Delete are extra methods on the
concrete type used only by the HTTP handlers.

**Storage bucket:** `"webhook_subscriptions"` (exported constant
`SubscriptionsBucket`).

**Key format:** `<20-digit-zero-padded-nanoseconds>-<monotonic-seq>.json`
— identical to DLQ record keys — so that lexicographic order tracks
insertion order and `store.ListPage` produces correct pagination.

**Filtering in `ListSubscriptions`:** the full key list is loaded and
decoded in-memory; matching against `service`, `eventType`, and `tenantID`
is then applied before returning.  This is consistent with how filtered list
helpers work elsewhere (ADR-033) and defers the question of per-field indexes
until benchmarks justify the complexity.

### HTTP Routes

All four routes are registered unconditionally inside the site-scoped route
group and therefore benefit from `SiteMiddleware`, `AuthMiddleware`, and
`AuditMiddleware` automatically.

| Method   | Path                                         | Handler                     | Success |
|----------|----------------------------------------------|-----------------------------|---------|
| `GET`    | `/sites/{site_id}/webhooks/subscriptions`    | `ListSubscriptionsHandler`  | 200     |
| `POST`   | `/sites/{site_id}/webhooks/subscriptions`    | `CreateSubscriptionHandler` | 201     |
| `GET`    | `/sites/{site_id}/webhooks/subscriptions/{id}` | `GetSubscriptionHandler`  | 200     |
| `DELETE` | `/sites/{site_id}/webhooks/subscriptions/{id}` | `DeleteSubscriptionHandler`| 204    |

### `POST /webhooks/subscriptions` — request body

| Field       | Type       | Required | Notes                                              |
|-------------|------------|----------|----------------------------------------------------|
| `url`       | string     | yes      | Delivery endpoint URL                              |
| `service`   | string     | yes      | Logical service name (used to filter on dispatch)  |
| `events`    | `[]string` | yes      | Must be non-empty; e.g. `["audit.write"]`          |
| `secret`    | string     | no       | HMAC-SHA256 signing secret; omitted when empty     |
| `tenant_id` | string     | no       | Tenant scope; empty = global subscription          |
| `enabled`   | bool       | no       | Defaults to **`true`** when omitted                |

Missing or empty required fields return **400 Bad Request** listing the
absent field names.

### `GET /webhooks/subscriptions` — pagination

Supports the same `?cursor=` / `?limit=` parameters as all other list
endpoints (ADR-033).  `next_cursor` is empty when no further pages exist.

```json
{
  "site_id": "…",
  "subscriptions": [ …Subscription… ],
  "next_cursor": "…"
}
```

### `IsNotFound` helper

```go
// internal/webhooks/store_provider.go
func IsNotFound(err error) bool
```

Reports whether `err` wraps `storage.ErrNotFound`.  Used by the HTTP
handlers to map storage-level not-found errors to 404 responses without
importing the `storage` package in the handler layer.

## Consequences

* **No new dependencies** — the implementation uses only packages already
  present in the module (`bbolt`, `chi`, `slog`, standard library).
* **Per-site isolation** — each site's subscriptions live in its own bbolt
  database under the `"webhook_subscriptions"` bucket, preventing
  cross-site leakage.
* **Composable with RemoteProvider** — `StoreProvider` satisfies `Provider`,
  so it can be wrapped by `CachingProvider` or composed with a
  `RemoteProvider` (e.g. by merging results from both) in a future segment.
* **Audit trail** — all mutating requests (`POST`, `DELETE`) are recorded by
  `AuditMiddleware` and dispatch an `"audit.write"` webhook event when the
  webhook dispatcher is configured.
* **Auth enforcement** — `AuthMiddleware` enforces the site API key on
  `POST` and `DELETE` requests exactly as for all other mutating routes.
* **O(n) list scan** — `ListSubscriptions` loads all keys before filtering.
  For the typical subscription counts expected (tens to hundreds per site)
  this is negligible; a secondary index can be added later if needed.
