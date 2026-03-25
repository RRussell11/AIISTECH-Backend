# ADR-031: Per-Subscription Webhook Circuit Breaker

## Status
Accepted

## Context

The webhook `WorkerDispatcher` (ADR-012, Segments 12–25) retries failed
deliveries up to `MaxAttempts` times with exponential back-off before
exhausting and writing a `DLQRecord`.  If a subscriber endpoint is
persistently down (e.g. days-long outage), every queued event for that
subscription will still cycle through the full retry loop, consuming worker
threads, adding latency to other subscriptions sharing those workers, and
growing the DLQ unnecessarily.

A **circuit breaker** short-circuits this waste: after a configurable
number of consecutive fully-exhausted delivery failures, the breaker
transitions to the Open state and fast-fails subsequent delivery attempts
for that subscription — no HTTP calls are made.  After a cooldown period
the breaker allows one trial delivery; on success it resets; on failure it
re-opens.

This is the classic
[three-state circuit breaker](https://martinfowler.com/bliki/CircuitBreaker.html)
pattern applied at the per-subscription level.

## Decision

### New types (internal/webhooks/circuit_breaker.go)

```go
type CircuitBreakerConfig struct {
    FailureThreshold int           // consecutive exhausted-delivery failures to trip (default: 5)
    OpenDuration     time.Duration // cooldown before half-open trial (default: 60 s)
}
```

A `circuitBreaker` (unexported) holds the state machine and is protected by
a mutex.  A `breakerRegistry` (also unexported) maps subscription ID →
`*circuitBreaker` via `sync.Map` for lock-free reads on the hot path.

### Three-state machine

| State      | Allow() | Transitions                              |
|------------|---------|------------------------------------------|
| **Closed** | true    | → Open after `FailureThreshold` failures |
| **Open**   | false   | → Half-Open when cooldown expires        |
| **Half-Open** | true (once) | → Closed on success; → Open on failure |

`Allow()` is called once per `deliverWithRetry` invocation (not per individual
HTTP attempt).  The circuit decision is at the "whole delivery with retries"
level.

### Config field

```go
// dispatcher.go
type Config struct {
    // ... existing fields ...
    CircuitBreaker *CircuitBreakerConfig // nil = disabled (existing behaviour)
}
```

`nil` preserves the previous behaviour exactly — no code path is affected
unless a non-nil `CircuitBreaker` pointer is supplied.

### Wiring in WorkerDispatcher

`deliverWithRetry` was refactored to:

1. Check `Allow()` at entry; if false → log, increment
   `webhook_delivery_failures_total`, DLQ-store with `Attempts=0`, return.
2. On success after retry loop → `RecordSuccess()`.
3. On exhausted retries → `RecordFailure()` (may trip the breaker).

A private `storeDLQ` helper was extracted to avoid duplicating the DLQ
write logic between the circuit-open path and the exhausted-retries path.

### New expvar counter

```
webhook_cb_opens_total  int64  // incremented each time any breaker trips to Open
```

### Env vars (main.go)

```sh
AIISTECH_WEBHOOK_CB_FAILURE_THRESHOLD=5    # > 0 enables the circuit breaker
AIISTECH_WEBHOOK_CB_OPEN_DURATION_SECONDS=60
```

When `AIISTECH_WEBHOOK_CB_FAILURE_THRESHOLD` is absent or ≤ 0 the circuit
breaker remains disabled.

### DLQ Attempts field for fast-failed events

When the circuit is open, `storeDLQ` is called with `Attempts=0`.  This
makes it easy to distinguish "circuit-open fast-fail" DLQ records from
"retry-exhausted" records (which carry the actual attempt count).

## Consequences

* **Unhealthy subscribers no longer monopolise workers** — after tripping, no
  HTTP calls are made until the cooldown elapses and the trial delivery
  succeeds.
* **DLQ still receives events** — circuit-open events are still persisted for
  later replay.  `Attempts=0` flags them as circuit-breaker records.
* **Fully backward-compatible** — `Config.CircuitBreaker = nil` (the zero
  value) leaves all existing behaviour unchanged.
* **Per-subscription isolation** — one slow subscriber does not affect others;
  each subscription has its own independent breaker state.
* **No new external dependencies** — only stdlib (`sync`, `expvar`, `time`).
* **Observable** — `webhook_cb_opens_total` can be scraped from `/metrics`
  (expvar JSON) and alerted on.
