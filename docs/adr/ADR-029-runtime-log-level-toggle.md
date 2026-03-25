# ADR-029: Runtime Log-Level Toggle API

## Status
Accepted

## Context

Diagnosing production issues often requires more verbose log output (e.g.
`DEBUG`) than is safe to run continuously. Previously, changing the log level
required:

1. Setting `AIISTECH_LOG_LEVEL` in the deployment configuration, and
2. Restarting the process.

A restart drains the webhook dispatcher queue, closes all bbolt stores, and
briefly interrupts request handling — all of which are undesirable during an
active incident.

The existing startup code already creates a `*slog.LevelVar` (an
`slog.LevelVar` is specifically designed for runtime mutation) and passes it to
the `slog.TextHandler`. The only missing piece was exposing that variable
through the HTTP API.

## Decision

Add two new endpoints under the `/debug/` prefix:

| Method | Path               | Description                                |
|--------|--------------------|--------------------------------------------|
| `GET`  | `/debug/log-level` | Returns current level and managed flag     |
| `PUT`  | `/debug/log-level` | Updates the level; body `{"level":"DEBUG"}`|

### GET /debug/log-level — response body

```json
{"level":"INFO","managed":true}
```

| Field     | Type    | Meaning                                                      |
|-----------|---------|--------------------------------------------------------------|
| `level`   | string  | Current slog level: `"DEBUG"`, `"INFO"`, `"WARN"`, `"ERROR"`|
| `managed` | boolean | `true` when `OpsConfig.LogLevel` is wired; `false` otherwise |

### PUT /debug/log-level — request body

```json
{"level":"DEBUG"}
```

Level names are case-insensitive (`debug`, `Debug`, `DEBUG` are all accepted).
Returns the same JSON shape as GET on success (200 OK).

### Error responses

| Condition                     | Status | Body                                           |
|-------------------------------|--------|------------------------------------------------|
| `LogLevel` not wired          | 501    | plain text: "log level is not runtime-configurable" |
| Invalid JSON body             | 400    | plain text: "invalid JSON body"               |
| Unknown level name            | 400    | plain text: "unknown log level …"             |

### Wiring

`OpsConfig` gains a `LogLevel *slog.LevelVar` field. In `cmd/server/main.go`
this is set to the same `*slog.LevelVar` used to configure `slog.SetDefault`,
so a PUT takes effect for all subsequent log lines immediately — without
restarting.

When `LogLevel` is `nil` (e.g. in tests using the bare `newRouter` helper),
GET returns `{"level":"INFO","managed":false}` and PUT returns 501.

## Consequences

* Operators can increase log verbosity to DEBUG during an incident and restore
  it to INFO when done, all without a process restart or queue drain.
* The change is process-wide and immediate; there is no per-request or
  per-site granularity.
* The `/debug/log-level` endpoints are not behind any authentication (same as
  `/metrics`, `/version`, and `/healthz`). Operators who need to restrict
  access should place those routes behind a network-level control (firewall,
  reverse proxy) or add an auth middleware to the `/debug/` sub-router.
* No new dependencies are introduced; the standard library `log/slog` package
  already provides `slog.LevelVar` and `slog.Level.UnmarshalText`.
