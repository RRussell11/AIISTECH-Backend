# ADR-028: Richer expvar Metrics

## Status
Accepted

## Context

The existing `/metrics` endpoint (backed by `expvar.Handler`) exposed only two
counters:

| Name                | Type  | Meaning                       |
|---------------------|-------|-------------------------------|
| `requests_total`    | Int   | Total HTTP requests processed |
| `requests_by_site`  | Map   | Per-site HTTP request counts  |

While useful for gross traffic visibility, operators had no way to distinguish
*read* traffic from *write* traffic, could not tell whether webhook deliveries
were succeeding or failing, and had no signal for the size of the dead-letter
queue backlog — all without reaching for an external metrics system.

## Decision

Add five new expvar variables to give operators actionable operational insight
with zero additional dependencies:

### HTTP handler counters (registered in `internal/http/handlers.go`)

| Name                           | Type | Incremented                                        |
|--------------------------------|------|----------------------------------------------------|
| `events_written_by_site`       | Map  | After each successful `POST /sites/{id}/events`    |
| `artifacts_written_by_site`    | Map  | After each successful `POST /sites/{id}/artifacts` |

Both are `*expvar.Map` keyed by `site_id`. The counter is only incremented on
a **successful** write (HTTP 201); validation failures and storage errors do
not increment the counter.

### Webhook delivery counters (registered in `internal/webhooks/worker_dispatcher.go`)

| Name                               | Type | Incremented                                          |
|------------------------------------|------|------------------------------------------------------|
| `webhook_deliveries_total`         | Int  | Each time a delivery attempt succeeds (2xx)          |
| `webhook_delivery_failures_total`  | Int  | Each time all retry attempts are exhausted           |
| `webhook_dlq_stored_total`         | Int  | Each time a DLQ record is successfully written       |

`webhook_dlq_stored_total` is only incremented when `cfg.DLQ.Store()` returns
`nil`; a DLQ write error leaves the counter unchanged.

All counters are monotonically increasing and survive across requests for the
lifetime of the process.  They are visible in the existing `GET /metrics`
endpoint as part of the standard `expvar` JSON output alongside the existing
`requests_total` and `requests_by_site` counters.

## Consequences

* Operators can now differentiate write load from read load per site directly
  from the `/metrics` endpoint.
* Webhook health is observable: a rising `webhook_delivery_failures_total`
  relative to `webhook_deliveries_total` signals receiver instability; a
  rising `webhook_dlq_stored_total` indicates accumulating backlog.
* No new dependencies are introduced; the standard library `expvar` package
  is already used throughout the codebase.
* Counter values reset to zero on process restart (they are in-memory only).
  For persistent metrics, operators should scrape and store the values
  externally (e.g. via Prometheus `expvar_exporter` or a log-based pipeline).
