# ADR-025: Webhook Dead Letter Queue (DLQ)

## Status
Accepted

## Context
The `WorkerDispatcher` (Segment 12) already applies exponential back-off retry
logic for failed webhook deliveries. Once all retry attempts are exhausted the
failure was silently discarded: the event was lost and the operator had no way
to inspect what went wrong or trigger a re-delivery.

Three operational problems this creates:

1. **Lost events** — transient receiver downtime during the retry window
   permanently loses the notification.
2. **No observability** — there is no record that a delivery was attempted and
   failed, making debugging painful.
3. **No recovery path** — operators cannot manually re-drive failed deliveries
   after the receiver is restored.

## Decision
Add a **Dead Letter Queue** that captures every exhausted-retry delivery
failure and exposes it through site-scoped REST endpoints.

### Data model — `webhooks.DLQRecord`

```go
type DLQRecord struct {
    ID              string    // storage key (e.g. "00001748000000000000-1.json")
    SubscriptionID  string
    SubscriptionURL string
    Secret          string    // omitempty; preserved for HMAC re-signing on replay
    EventID         string
    EventType       string
    SiteID          string
    TenantID        string    // omitempty
    Payload         []byte    // exact JSON body attempted during delivery
    Attempts        int
    FailedAt        time.Time
}
```

### Interface — `webhooks.DLQSink`

```go
type DLQSink interface {
    Store(record DLQRecord) error
}
```

Simple write-only interface used by `WorkerDispatcher`. Implementations must be
safe for concurrent use.

### Implementation — `webhooks.StoreDLQSink`

A bbolt-backed implementation that routes each record to the site-specific
`storage.Store` obtained from a `*storage.Registry`. The storage bucket is
`"webhook_dlq"` (`webhooks.DLQBucket`). Keys are zero-padded 20-digit Unix
nanosecond timestamps with a monotonic sequence suffix
(`%020d-%d.json`) to guarantee uniqueness and lexicographic time ordering.

### `webhooks.Config` change

A new `DLQ DLQSink` field is added to `Config`. When non-nil, the dispatcher
stores a `DLQRecord` after exhausting all retry attempts. A nil DLQ silently
discards failures (preserving the existing behaviour for callers that do not
opt in).

### `webhooks.Event` change

A `SiteID string` field is added to `Event` so that `WorkerDispatcher` can
route the DLQ record to the correct per-site store. `AuditMiddleware` now
populates `evt.SiteID = sc.SiteID`.

### HTTP endpoints

All endpoints are site-scoped and subject to the existing `SiteMiddleware` +
`AuthMiddleware` chain.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/sites/{site_id}/webhooks/dlq` | Paginated list of DLQ entries |
| `GET` | `/sites/{site_id}/webhooks/dlq/{id}` | Single DLQ entry |
| `DELETE` | `/sites/{site_id}/webhooks/dlq/{id}` | Discard a DLQ entry |
| `POST` | `/sites/{site_id}/webhooks/dlq/{id}/replay` | Re-drive a DLQ entry |

The `{id}` parameter is the storage key returned in the list/get responses.

#### Replay behaviour

1. Read the DLQ record by `{id}` from the site store.
2. Build an `http.Request` — POST `record.Payload` to `record.SubscriptionURL`
   with a fresh `X-Webhook-Timestamp` and, when `record.Secret` is non-empty,
   a freshly-computed `X-Webhook-Signature` (ADR-012 signing scheme).
3. On a `2xx` response: delete the entry and return
   `200 {"status":"ok","entry_deleted":true}`.
4. On any other response or transport error: return `502 Bad Gateway` and
   preserve the entry.

The replay HTTP client is configurable via `OpsConfig.ReplayClient`; a default
`10 s` timeout client is used when nil.

### Wiring

`OpsConfig` gains two new fields:

```go
DLQ          webhooks.DLQSink // nil = routes still registered but empty
ReplayClient *http.Client     // nil = default 10 s client
```

The four DLQ routes are registered unconditionally inside the site-scoped
route group, so they return empty results even when no deliveries have
ever failed.

`cmd/server/main.go` creates a `StoreDLQSink` at startup (before the
dispatcher block) and wires it into both `Config.DLQ` (for the dispatcher) and
`OpsConfig.DLQ` (for the HTTP router).

## Consequences

- **No data loss after retry exhaustion** — failed deliveries are durably
  persisted in the same per-site bbolt store used by events/artifacts/audit.
- **Operator recovery path** — the replay endpoint allows re-driving
  individual entries without application restart.
- **Backward compatible** — `DLQ nil` in `Config` is a no-op; all existing
  tests and call-sites work unchanged.
- **Secret preserved** — storing the subscription secret in `DLQRecord` means
  replayed requests are correctly HMAC-signed. Operators should be aware that
  secrets are stored at rest; the existing per-site bbolt file permissions
  (0600) mitigate this.
- **No LRU eviction** — DLQ entries accumulate indefinitely. Operators are
  expected to delete or replay entries; automated TTL eviction is deferred to
  a future segment.
- **No webhook event for DLQ writes** — DLQ storage does not itself dispatch a
  webhook to avoid potential infinite loops. An observability hook can be added
  in a future segment if needed.
