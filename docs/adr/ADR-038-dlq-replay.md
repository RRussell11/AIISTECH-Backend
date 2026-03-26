# ADR-038 — Dead-Letter Queue (DLQ) and Replay

**Status:** Accepted  
**Date:** 2026-03-26  
**Segment:** 38/39

---

## Context

The webhook pipeline had no facility for persisting failed deliveries. When
`WorkerDispatcher.deliverWithRetry` exhausted all attempts the failure was
logged and the event was permanently lost. Operators had no way to inspect
failed deliveries, replay them manually, or rely on automatic recovery.

---

## Decision

### 1. DLQ Store (`internal/webhooks/dlq.go`)

A new `DLQStore` type wraps any `storage.Store` (typically the same bbolt
database used for local subscriptions) and persists `DLQRecord` values in the
`"webhook_dlq"` bucket.

**`DLQRecord` fields:**

| Field | Description |
|---|---|
| `ID` | Storage key (nanosecond timestamp + `.json`, assigned by `Save` if empty) |
| `SubscriptionID` / `URL` / `Secret` | Delivery target snapshot |
| `Event` | Original event payload |
| `Attempts` | Number of replay attempts (post-DLQ only; initial burst is separate) |
| `LastError` | Human-readable error string from the most recent failure |
| `FailedAt` | UTC timestamp of the most recent failed attempt |
| `NextRetryAfter` | Earliest UTC time for auto-retry; ignored by manual replay |

A record is **terminal** when `Attempts >= DLQMaxAttempts` (default 10); the
auto-retry scheduler skips terminal records. Manual replay via the HTTP endpoint
is always possible.

### 2. Dispatcher DLQ integration (`internal/webhooks/worker_dispatcher.go`)

* `deliverWithRetry` — on exhaustion, calls `saveToDLQ` which writes a
  `DLQRecord` and increments `webhook_dlq_stored_total`.
* `ReplayRecord(DLQRecord) error` — performs a single delivery attempt for the
  given record. Returns the delivery error (nil on success). Does NOT update
  the store; callers are responsible.
* `autoRetry()` — background goroutine (started when `DLQStore` is configured
  and `DLQScanInterval > 0`) that:
  * Ticks every `DLQScanInterval` (default 60 s).
  * Scans all DLQ records; skips terminal and not-yet-eligible records.
  * Replays eligible records concurrently (up to `WorkerCount` goroutines).
  * On success: deletes the record, increments `webhook_dlq_replay_success_total`.
  * On failure: updates `LastError`, increments `Attempts`, computes
    exponential back-off for `NextRetryAfter` (base `DLQCoolingOff`, doubles
    per attempt, capped at 24 h), increments `webhook_dlq_replay_failure_total`.
  * Stopped cleanly by `Close()`.

### 3. New Config fields (`internal/webhooks/dispatcher.go`)

| Field | Default | Description |
|---|---|---|
| `DLQStore` | `nil` (DLQ disabled) | Backing store for DLQ records |
| `DLQCoolingOff` | 5 minutes | Minimum wait before first auto-retry |
| `DLQScanInterval` | 60 seconds | How often the auto-retry scheduler wakes up |
| `DLQMaxAttempts` | 10 | Max replay attempts before record is terminal |

### 4. HTTP endpoints (`internal/http/dlq_handlers.go`)

Mounted at `/webhooks/dlq` only when both `dlqStore` and `dlqReplayer` are
non-nil in `NewRouter`.

| Method | Path | Description |
|---|---|---|
| `GET` | `/webhooks/dlq/` | List DLQ records (paginated, supports `?cursor=` and `?limit=`) |
| `GET` | `/webhooks/dlq/{id}` | Get a specific DLQ record |
| `DELETE` | `/webhooks/dlq/{id}` | Remove a DLQ record (cancel / discard) |
| `POST` | `/webhooks/dlq/{id}/replay` | Replay a single record; removes it on success, updates attempts on failure |
| `POST` | `/webhooks/dlq/replay-all` | Replay all records concurrently (up to 8 goroutines); returns summary |

### 5. Expvar metrics

| Key | Type | Description |
|---|---|---|
| `webhook_dlq_stored_total` | int | Total records written to the DLQ |
| `webhook_dlq_replay_success_total` | int | Total records successfully replayed |
| `webhook_dlq_replay_failure_total` | int | Total replay attempts that failed |

### 6. Server wiring (`cmd/server/main.go`)

The DLQ store is opened from the same bbolt database as local subscriptions
(`var/state/webhooks/subscriptions.db`). It is wired into the
`WorkerDispatcher` via `Config.DLQStore` and passed to `NewRouter` for HTTP
handler use.

DLQ is **only enabled** when `AIISTECH_WEBHOOK_STORE_PROVIDER=true` or when
both store-provider and remote-provider are configured (i.e., when a
subscriptions database is open). RemoteProvider-only mode does not persist a
subscriptions database and therefore has no DLQ.

---

## Consequences

* Failed deliveries are no longer silently dropped; they are persisted and
  observable via the API.
* Operators can inspect failures, trigger manual replays, and remove records
  they no longer want retried.
* The auto-retry scheduler provides automatic recovery after transient outages
  without operator intervention.
* The cooling-off period prevents thundering-herd retries immediately after a
  subscriber recovers.
* Expvar metrics give on-box visibility into DLQ health without a separate
  monitoring system.
