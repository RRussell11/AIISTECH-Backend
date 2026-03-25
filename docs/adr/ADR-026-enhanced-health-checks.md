# ADR-026: Enhanced Health Check Endpoints

## Status
Accepted

## Context
The application already exposed three health check URLs:

| Route | Purpose |
|-------|---------|
| `GET /healthz` | Backward-compatible liveness (always 200) |
| `GET /healthz/live` | Kubernetes liveness probe |
| `GET /healthz/ready` | Kubernetes readiness probe |

However both `/healthz/live` and `/healthz/ready` were too shallow to be
operationally useful:

- `/healthz/live` returned `{"status":"ok"}` with no process metadata —
  operators could not tell if the response was from a freshly-started or
  a long-running (possibly stuck) process.
- `/healthz/ready` returned `{"status":"ok","sites":N}` even if the underlying
  bbolt stores were inaccessible; a Kubernetes scheduler would mark the pod
  "Ready" despite broken storage.

## Decision
Enhance both endpoints without breaking backward compatibility:

### `/healthz/live` — process uptime

A package-level `serverStartTime` variable (`var serverStartTime = time.Now()`)
is initialised when the `internal/http` package is first loaded (effectively at
process start).

`LivezHandler` now returns:

```json
{
  "status": "ok",
  "uptime_seconds": 42
}
```

`uptime_seconds` is `int(time.Since(serverStartTime).Seconds())`. No
configuration or main.go changes are required.

### `/healthz/ready` — per-site store probing

`ReadyzHandler` now accepts a `*storage.Registry` argument in addition to
`*site.Registry`. For each site ID returned by `reg.SiteIDs()`, it calls
`stores.Open(id)`:

- If all opens succeed → `200 OK`
- If any open fails → `503 Service Unavailable`

The response body carries a per-site `stores` map so operators can pinpoint
which stores are degraded:

```json
{
  "status": "ok",
  "sites": 2,
  "stores": {
    "local":   "ok",
    "staging": "ok"
  }
}
```

On degradation:

```json
{
  "status": "degraded",
  "sites": 2,
  "stores": {
    "local":   "ok",
    "staging": "error: opening bbolt db \"/data/staging/db\": open /data/staging/db: permission denied"
  }
}
```

`NewRouter` already receives `stores *storage.Registry`, so the call site
change is a single-line update (`ReadyzHandler(reg, stores)`).

### `storage.Registry.Open` as a health probe

`Registry.Open` lazily opens the bbolt database file, creating parent
directories as needed. A failure from `Open` means the underlying filesystem
path is inaccessible (wrong permissions, corrupt file, missing mount, etc.),
which is exactly the failure class a readiness probe needs to surface. No new
`Ping` method is required on the `Store` interface.

## Consequences

- Kubernetes liveness probes can now detect stuck or very-new pods via
  `uptime_seconds`.
- Kubernetes readiness probes now surface storage failures, preventing traffic
  from being routed to pods with broken stores.
- Existing clients that parse only `status` and `sites` from `/healthz/ready`
  remain unaffected (additive change).
- Adding a new site to the registry automatically adds it to the readiness
  probe; no code changes are needed.
- `serverStartTime` is set at package initialisation, not at HTTP server start.
  For typical deployments the difference is negligible (< 1 s); for test suites
  the value may be several seconds old, which is acceptable.
