# ADR-015: Webhook Dead-Letter Queue

**Status:** Accepted  
**Date:** 2026-03-24  
**Segment:** 15

## Context

Prior to this ADR, webhook deliveries that exhausted all retry attempts were logged at `ERROR` level and silently dropped. There was no durable record of the failure, no way to inspect what was dropped, and no mechanism to replay a delivery once the downstream receiver became available again.

## Decision

### A) Persist failed deliveries to a site-scoped "webhook_dlq" bucket

When `WorkerDispatcher.deliverWithRetry` exhausts all attempts, it writes a `DLQRecord` to the site's bbolt store under the `webhook_dlq` bucket via an optional `DLQSink` interface.

A `DLQRecord` captures:
- Original event metadata (`event_id`, `event_type`, `site_id`, `tenant_id`)
- Subscription details (`subscription_id`, `url`)
- The full serialised payload (for replay)
- Delivery context (`attempt_count`, `last_error`, `failed_at`)

### B) DLQSink is an interface; StoreDLQSink is the concrete implementation

`DLQSink` is a single-method interface (`WriteDLQ(DLQRecord) error`) defined in the `webhooks` package. The concrete `StoreDLQSink` uses a `*storage.Registry` to resolve the correct site store and write the record. This keeps the dispatcher independent of storage internals.

If `Config.DLQ` is nil (the default), no DLQ writes occur and existing behaviour is preserved.

### C) DLQ bucket key format

Keys follow the same nanosecond-timestamp pattern used by the audit bucket:
`<failed_at_unix_nano>-<monotonic_seq>.json`

This gives time-sorted keys and unique entries even under concurrent failures.

### D) Read/purge via HTTP endpoints

Three site-scoped endpoints are added:

| Method | Path | Description |
|--------|------|-------------|
| `GET`  | `/sites/{site_id}/webhooks/dlq` | List DLQ entry keys (paginated) |
| `GET`  | `/sites/{site_id}/webhooks/dlq/{id}` | Retrieve full DLQ record |
| `DELETE` | `/sites/{site_id}/webhooks/dlq/{id}` | Purge an entry after replay |

`DELETE` is protected by the existing `AuthMiddleware` (requires Bearer token when `api_key` is configured for the site).

## Replay Semantics

Replay is **manual**: an operator retrieves the payload from the DLQ entry and re-POSTs it to the subscription URL directly, then deletes the entry. Automated replay is deferred to a future segment.

## Compatibility Considerations

- `Config.DLQ` defaults to `nil`; existing deployments that do not configure a DLQ see no behaviour change.
- `webhooks.Event` gains a new `site_id` field (`json:"site_id,omitempty"`); existing consumers that do not expect it are unaffected.
- The `webhook_dlq` bucket is created lazily on first write (standard bbolt behaviour).
