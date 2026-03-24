# ADR-016: DLQ Replay Endpoint

**Status:** Accepted  
**Date:** 2026-03-24  
**Segment:** 16

## Context

ADR-015 (Segment 15) introduced a dead-letter queue that durably persists failed webhook deliveries. The ADR explicitly deferred automated replay to a future segment, requiring operators to manually reconstruct and re-send the payload. This ADR closes that gap by providing a single HTTP endpoint that performs the re-delivery.

## Decision

### A) Endpoint: `POST /sites/{site_id}/webhooks/dlq/{id}/replay`

A single idempotent-by-intent POST endpoint triggers an immediate synchronous re-delivery attempt for the named DLQ entry.

Behaviour:

| Condition | HTTP status | Side-effect |
|-----------|-------------|-------------|
| Entry not found | 404 | none |
| Invalid `{id}` | 400 | none |
| Re-delivery succeeds (2xx from receiver) | 200 | DLQ entry deleted |
| Re-delivery fails (non-2xx or transport error) | 502 | DLQ entry **preserved** for future retry |

The entry is only deleted on a confirmed 2xx response so operators can trigger the endpoint again (or wait for conditions to improve) without data loss.

The endpoint is protected by `AuthMiddleware` (it is a POST, so it requires a Bearer token when the site has an `api_key` configured).

### B) HMAC re-signing

The original delivery was optionally signed with `X-Webhook-Signature` / `X-Webhook-Timestamp` headers using the subscription's secret. To replay faithfully, the subscription secret is persisted in `DLQRecord.Secret` (populated at the time of the original delivery failure by `WorkerDispatcher.deliverWithRetry`).

On replay, the handler applies the same `webhooks.SignatureHeader` logic with a fresh timestamp, exactly as `WorkerDispatcher.deliverOnce` does for normal deliveries.

### C) HTTP client injection

`ReplayDLQEntryHandler` accepts an `*http.Client` parameter (factory pattern, consistent with `AuditMiddleware(d Dispatcher)`). `NewRouter` now accepts an optional `replayClient *http.Client`; a `nil` value creates a default client with a 10-second timeout. This allows tests to inject an `httptest.Server`-backed client without altering the production call path.

## Compatibility Considerations

- `DLQRecord` gains a new `Secret` field (`json:"secret,omitempty"`). Records written before this segment lack the field; replay without a secret simply omits the signature header (same as a subscription with no secret).
- `NewRouter` gains a `replayClient *http.Client` parameter; passing `nil` preserves previous behaviour exactly.
- No new dependencies.
