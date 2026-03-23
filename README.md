# AIISTECH-Backend

A Go HTTP backend implementing the **Site as Tenant / Governance Namespace** pattern.  
All stateful operations are scoped by an explicit `site_id`.

## Table of Contents

- [Requirements](#requirements)
- [Quick Start](#quick-start)
- [Environment Variables](#environment-variables)
- [Project Structure](#project-structure)
- [Site Registry](#site-registry)
- [Per-Site Configuration](#per-site-configuration)
- [API Reference](#api-reference)
  - [Segment 1 — Core Site Scaffolding](#segment-1--core-site-scaffolding)
  - [Segment 2 — Events Vertical Slice](#segment-2--events-vertical-slice)
  - [Segment 3 — Structured Audit Trail](#segment-3--structured-audit-trail)
  - [Segment 4 — Artifacts Vertical Slice](#segment-4--artifacts-vertical-slice)
  - [Segment 5 — Configuration Contract Layer](#segment-5--configuration-contract-layer)
  - [Segment 6 — Observability & Operational Readiness](#segment-6--observability--operational-readiness)
  - [Segment 7 — Persistent Storage](#segment-7--persistent-storage)
  - [Segment 8 — Authentication & Authorisation](#segment-8--authentication--authorisation)
  - [Segment 9 — Pagination](#segment-9--pagination)
  - [Segment 10 — Docker & CI/CD](#segment-10--docker--cicd)
  - [Segment 11A — Ops Hardening](#segment-11a--ops-hardening)
  - [Segment 11B — List Filtering](#segment-11b--list-filtering)
  - [Segment 11C — Webhook Subscription Caching](#segment-11c--webhook-subscription-caching)
- [Roadmap](#roadmap)
- [Tests](#tests)

---

## Requirements

- Go 1.24+

## Quick Start

```bash
# Clone and enter the repo
git clone https://github.com/RRussell11/AIISTECH-Backend.git
cd AIISTECH-Backend

# Install dependencies
go mod download

# Run the server (defaults to :8080, loads contracts/shared/sites.yaml)
go run ./cmd/server
```

The server reads the site registry from `contracts/shared/sites.yaml` on startup and shuts down gracefully on `SIGINT`/`SIGTERM` with a 10-second drain window.

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `AIISTECH_ADDR` | `:8080` | Listen address |
| `AIISTECH_REGISTRY_PATH` | `contracts/shared/sites.yaml` | Path to site registry file |
| `AIISTECH_LOG_LEVEL` | `INFO` | Structured log verbosity (`DEBUG`, `INFO`, `WARN`, `ERROR`) |
| `AIISTECH_CORS_ALLOW_ORIGINS` | *(disabled)* | Comma-separated list of allowed CORS origins (e.g. `https://app.example.com,https://admin.example.com`). When unset, CORS headers are not written. Use `*` to allow all origins. |
| `AIISTECH_MAX_BODY_BYTES` | `1048576` | Maximum request body size in bytes for mutating requests (POST/PUT/PATCH/DELETE). Requests exceeding this limit receive `413 Request Entity Too Large`. |
| `AIISTECH_RATE_LIMIT_RPS` | `10` | Sustained rate limit (requests per second) applied per-IP to mutating requests. Requests exceeding the burst receive `429 Too Many Requests`. |
| `AIISTECH_RATE_LIMIT_BURST` | `20` | Maximum burst size for the per-IP rate limiter. |
| `AIISTECH_WEBHOOK_BASE_URL` | *(disabled)* | PhaseMirror-HQ subscription API base URL. When set, audit events are dispatched as webhooks. |
| `AIISTECH_WEBHOOK_TOKEN` | *(none)* | Bearer token for the webhook subscription API. |
| `AIISTECH_SERVICE_NAME` | `aiistech-backend` | Logical service name used when registering with PhaseMirror-HQ. |
| `AIISTECH_WEBHOOK_CACHE_TTL_SECONDS` | `30` | TTL (seconds) for the per-`(service, eventType, tenantID)` subscription cache. Reduces load on the PhaseMirror-HQ API when many events fire in quick succession. |
| `AIISTECH_WEBHOOK_POLL_INTERVAL_SECONDS` | `0` (disabled) | Background subscription poll interval in seconds. When positive, a goroutine proactively refreshes all known cache keys at this interval, keeping the cache warm so dispatch never blocks on an HTTP fetch. `0` disables background polling (lazy-only mode). |

## Project Structure

```
cmd/server/           # Server entrypoint (startup, shutdown, env config)
contracts/
  shared/             # Shared site registry (sites.yaml)
  sites/
    local/            # Per-site config contracts
    staging/
    prod/
internal/
  audit/              # Audit entry struct + Write helper
  config/             # Per-site config loader (SiteConfig, ConfigPath, Load)
  http/               # Router, middleware (Site, Audit, Metrics), handlers
  site/               # Registry loader, validator, resolver, context helpers
  state/              # Per-site filesystem path helpers
  storage/            # Store interface, BBoltStore, StoreRegistry
var/state/            # Runtime state (gitignored); layout: var/state/<site_id>/data.db
```

## Site Registry

Valid sites are defined in `contracts/shared/sites.yaml`:

```yaml
default_site_id: "local"
sites:
  - site_id: "local"
  - site_id: "staging"
  - site_id: "prod"
```

Rules:
- `default_site_id` is required and must appear in the `sites` list.
- Site IDs must be non-empty and must not contain `..`, `/`, or `\`.
- Unknown or invalid `site_id` values in API requests return `404`.

## Per-Site Configuration

Each site may have a config file at `contracts/sites/<site_id>/config.yaml`:

```yaml
site_id: local
settings:
  env: development
  log_level: debug
```

The `settings` map accepts arbitrary key/value string pairs. If the file does not exist for a site, an empty settings map is returned without error.

---

## API Reference

All site-scoped routes follow the pattern `/sites/{site_id}/...`.  
Mutating requests (`POST`, `PUT`, `PATCH`, `DELETE`) are automatically recorded to the site's audit trail.  
All responses are `application/json`.

---

### Segment 1 — Core Site Scaffolding

> Site-as-tenant foundation: registry loading, site validation, and metadata endpoints.

#### `GET /sites`
List all registered sites and the current default.

```
curl http://localhost:8080/sites
# {"default_site_id":"local","sites":["local","prod","staging"]}
```

#### `GET /sites/{site_id}`
Get site metadata including its state root path.

```
curl http://localhost:8080/sites/local
# {"site_id":"local","state_root":"var/state/local"}
```

---

### Segment 2 — Events Vertical Slice

> Append-only event log per site. Events are stored as nanosecond-timestamped JSON files under `var/state/<site_id>/events/`.

#### `POST /sites/{site_id}/events`
Write a JSON event. Body must be valid JSON (max 1 MiB). Returns the generated filename.

```
curl -X POST http://localhost:8080/sites/local/events \
  -H 'Content-Type: application/json' \
  -d '{"event":"deploy","version":"1.0.0"}'
# {"file":"1234567890.json","site_id":"local","status":"created"}
```

Response: `201 Created`

#### `GET /sites/{site_id}/events`
List all event filenames, sorted ascending. Returns an empty array if no events exist.

```
curl http://localhost:8080/sites/local/events
# {"events":["1234567890.json"],"site_id":"local"}
```

#### `GET /sites/{site_id}/events/{filename}`
Fetch the contents of a specific event file.

```
curl http://localhost:8080/sites/local/events/1234567890.json
# {"event":"deploy","version":"1.0.0"}
```

Returns `404` if the file does not exist. Returns `400` if the filename contains path traversal characters.

---

### Segment 3 — Structured Audit Trail

> Every mutating (`POST`, `PUT`, `PATCH`, `DELETE`) request on a site-scoped route is automatically recorded as a structured audit entry in `var/state/<site_id>/audit/`.

Audit entries are written by `AuditMiddleware` after the handler responds and require no opt-in from callers.

**Audit entry schema:**

```json
{
  "request_id": "abc123",
  "site_id": "local",
  "method": "POST",
  "path": "/sites/local/events",
  "status": 201,
  "timestamp": "2026-01-01T00:00:00.000000000Z"
}
```

#### `GET /sites/{site_id}/audit`
List all audit entry filenames for a site, sorted ascending.

```
curl http://localhost:8080/sites/local/audit
# {"entries":["1234567890.json"],"site_id":"local"}
```

#### `GET /sites/{site_id}/audit/{filename}`
Fetch a specific audit entry.

```
curl http://localhost:8080/sites/local/audit/1234567890.json
# {"request_id":"...","site_id":"local","method":"POST","path":"...","status":201,"timestamp":"..."}
```

---

### Segment 4 — Artifacts Vertical Slice

> Binary/JSON artifact storage per site. Artifacts are stored in `var/state/<site_id>/artifacts/`. Supports create, list, fetch, and delete.

#### `POST /sites/{site_id}/artifacts`
Store a JSON payload as an artifact. Body must be valid JSON (max 1 MiB).

```
curl -X POST http://localhost:8080/sites/local/artifacts \
  -H 'Content-Type: application/json' \
  -d '{"name":"build-output","sha":"abc123"}'
# {"file":"1234567890.json","site_id":"local","status":"created"}
```

Response: `201 Created`

#### `GET /sites/{site_id}/artifacts`
List all artifact filenames, sorted ascending.

```
curl http://localhost:8080/sites/local/artifacts
# {"artifacts":["1234567890.json"],"site_id":"local"}
```

#### `GET /sites/{site_id}/artifacts/{filename}`
Fetch a specific artifact.

```
curl http://localhost:8080/sites/local/artifacts/1234567890.json
# {"name":"build-output","sha":"abc123"}
```

#### `DELETE /sites/{site_id}/artifacts/{filename}`
Delete an artifact. Returns `204 No Content` on success, `404` if not found.

```
curl -X DELETE http://localhost:8080/sites/local/artifacts/1234567890.json
# HTTP 204
```

---

### Segment 5 — Configuration Contract Layer

> Read-only access to per-site configuration loaded from `contracts/sites/<site_id>/config.yaml`.

#### `GET /sites/{site_id}/config`
Return the parsed configuration for a site. Returns an empty `settings` map if no config file exists.

```
curl http://localhost:8080/sites/local/config
# {"site_id":"local","settings":{"env":"development","log_level":"debug"}}
```

---

### Segment 6 — Observability & Operational Readiness

> Process health probes and expvar-based request metrics.

#### `GET /healthz`
Backward-compatible liveness check (non-site-scoped).

```
curl http://localhost:8080/healthz
# {"status":"ok"}
```

#### `GET /healthz/live`
Explicit liveness probe. Returns `200 OK` as long as the process is running.

```
curl http://localhost:8080/healthz/live
# {"status":"ok"}
```

#### `GET /healthz/ready`
Readiness probe. Returns `200 OK` when the site registry is loaded and contains at least one site.

```
curl http://localhost:8080/healthz/ready
# {"sites":3,"status":"ok"}
```

#### `GET /sites/{site_id}/healthz`
Site-scoped health check. Returns `404` for unknown site IDs.

```
curl http://localhost:8080/sites/local/healthz
# {"site_id":"local","status":"ok"}
```

#### `GET /metrics`
Expvar-based request metrics (JSON). Key counters:

| Key | Type | Description |
|---|---|---|
| `requests_total` | int | Total requests handled |
| `requests_by_site` | map | Request count broken down by `site_id` |

```
curl http://localhost:8080/metrics
# {"requests_total":42,"requests_by_site":{"local":35,"staging":7},...}
```

---

### Segment 7 — Persistent Storage

> All site-scoped state (events, artifacts, audit) is persisted in a **bbolt** embedded key/value database at `var/state/<site_id>/data.db`. Each data category maps to a named bucket. Reads and writes are fully atomic, entries are sorted in ascending byte order with no extra sorting step, and concurrent access is safe.

**Storage layout:**

| Path | Contents |
|---|---|
| `var/state/<site_id>/data.db` | bbolt database for the site |
| bucket `events` | keyed events (`<nanosecond>.json` → JSON payload) |
| bucket `artifacts` | keyed artifacts (`<nanosecond>.json` → JSON payload) |
| bucket `audit` | keyed audit entries (`<nanosecond>.json` → JSON payload) |

The `StoreRegistry` opens each site's database lazily on first access and keeps it open for the lifetime of the server. All stores are closed gracefully on shutdown.

No API changes — all existing endpoints behave identically; storage is now durable across restarts.

---

### Segment 8 — Authentication & Authorisation

Each site optionally declares an `api_key` in its `contracts/sites/<site_id>/config.yaml`. When set, all state-mutating requests (POST, PUT, PATCH, DELETE) to that site's routes must carry:

```
Authorization: Bearer <api_key>
```

Read-only requests (GET, HEAD, OPTIONS) are always permitted regardless of configuration. Sites without an `api_key` remain fully open (useful for local development). The key is never exposed in the `GET /sites/{site_id}/config` JSON response.

**Behaviour summary:**

| Scenario | Result |
|---|---|
| Site has no `api_key` | All requests allowed |
| Site has `api_key`, GET request | Allowed |
| Site has `api_key`, mutating request, no `Authorization` header | `401 Unauthorized` |
| Site has `api_key`, mutating request, wrong token | `401 Unauthorized` |
| Site has `api_key`, mutating request, correct `Bearer <key>` | Allowed |

`WWW-Authenticate: Bearer realm="aiistech"` is included on every `401` response.

**Configuring a key:**

```yaml
# contracts/sites/staging/config.yaml
site_id: staging
api_key: your-secret-key-here
settings:
  env: staging
```

```bash
# Calling a protected endpoint:
curl -X POST http://localhost:8080/sites/staging/events \
  -H "Authorization: Bearer your-secret-key-here" \
  -H "Content-Type: application/json" \
  -d '{"event":"deploy"}'
```

---

---

### Segment 9 — Pagination

All three list endpoints (`/events`, `/artifacts`, `/audit`) now support cursor-based pagination via `?limit=` and `?cursor=` query parameters.

**Query parameters:**

| Parameter | Default | Max | Description |
|---|---|---|---|
| `limit` | `50` | `200` | Maximum number of keys to return per page |
| `cursor` | `""` | — | Opaque cursor returned by the previous page; omit to start from the beginning |

**Response fields added:**

| Field | Description |
|---|---|
| `next_cursor` | The cursor to pass on the next request to fetch the next page. Empty string when there are no more pages. |

**Example — walking all events in pages of 10:**

```bash
# Page 1
curl "http://localhost:8080/sites/local/events?limit=10"
# → { "events": [...], "next_cursor": "1234567890.json", ... }

# Page 2 (pass next_cursor from previous response)
curl "http://localhost:8080/sites/local/events?limit=10&cursor=1234567890.json"
# → { "events": [...], "next_cursor": "", ... }  ← empty cursor means last page
```

No breaking changes — existing callers that ignore `next_cursor` continue to work; the default limit of 50 is large enough to cover typical deployments without the need for pagination.

---

### Segment 10 — Docker & CI/CD

A multi-stage `Dockerfile` and a GitHub Actions CI workflow are provided.

#### Dockerfile

The build uses two stages:

| Stage | Image | Purpose |
|---|---|---|
| `builder` | `golang:1.24-alpine` | Compile a fully static binary (`CGO_ENABLED=0`) |
| runtime | `gcr.io/distroless/static-debian12:nonroot` | Minimal, unprivileged final image |

```bash
# Build the image
docker build -t aiistech-backend .

# Run it (mounts contracts/ and var/ from the host)
docker run -p 8080:8080 \
  -v "$PWD/contracts:/contracts:ro" \
  -v "$PWD/var:/var/state" \
  -e AIISTECH_REGISTRY_PATH=/contracts/shared/sites.yaml \
  aiistech-backend
```

#### CI workflow (`.github/workflows/ci.yml`)

Triggers on every push and pull-request to any branch. Three sequential steps must all pass before the workflow is considered green:

| Step | Command |
|---|---|
| Vet | `go vet ./...` |
| Test | `go test ./...` |
| Build | `go build ./cmd/server` |

---

### Segment 11A — Ops Hardening

Segment 11A adds production-grade operational middleware and server hardening.

#### CORS

CORS is **disabled by default**. Set `AIISTECH_CORS_ALLOW_ORIGINS` to a comma-separated list of allowed origins to enable it.

```bash
# Allow a specific origin
AIISTECH_CORS_ALLOW_ORIGINS=https://app.example.com go run ./cmd/server

# Allow all origins (development only)
AIISTECH_CORS_ALLOW_ORIGINS='*' go run ./cmd/server
```

`OPTIONS` preflight requests return `204 No Content` when the origin matches.

#### Max Request Body Size

Mutating requests (POST/PUT/PATCH/DELETE) are limited to `AIISTECH_MAX_BODY_BYTES` (default `1048576` = 1 MiB). Requests exceeding the limit receive:

```json
HTTP/1.1 413 Request Entity Too Large
request body too large
```

#### Per-IP Rate Limiting

Mutating requests are rate-limited per source IP. The rate limiter uses a token-bucket algorithm:

| Env var | Default | Description |
|---|---|---|
| `AIISTECH_RATE_LIMIT_RPS` | `10` | Sustained token replenishment rate (requests/second) |
| `AIISTECH_RATE_LIMIT_BURST` | `20` | Maximum burst (tokens available at start) |

Requests that exceed the burst receive:

```json
HTTP/1.1 429 Too Many Requests
{"error":"rate limit exceeded"}
```

GET/HEAD/OPTIONS requests are **not** rate-limited.

#### HTTP Server Timeouts

The server is configured with safe timeouts to prevent slow-client attacks:

| Timeout | Value |
|---|---|
| `ReadHeaderTimeout` | 5 s |
| `ReadTimeout` | 10 s |
| `WriteTimeout` | 30 s |
| `IdleTimeout` | 120 s |

#### `GET /version`

Returns build metadata set via `-ldflags` at build time.

```bash
curl http://localhost:8080/version
# {"build_time":"2024-06-01T12:00:00Z","commit":"abc1234","version":"v1.2.3"}
```

**Building with version info:**

```bash
go build \
  -ldflags "-X github.com/RRussell11/AIISTECH-Backend/internal/version.Version=v1.2.3 \
            -X github.com/RRussell11/AIISTECH-Backend/internal/version.Commit=$(git rev-parse --short HEAD) \
            -X github.com/RRussell11/AIISTECH-Backend/internal/version.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  ./cmd/server
```

Fields default to empty strings when built without the flags.

---

### Segment 11B — List Filtering

All three list endpoints (`/events`, `/artifacts`, `/audit`) support additional query parameters for server-side filtering. Filters are applied _before_ pagination, so `limit` and `next_cursor` always reflect the filtered result set.

**Filter query parameters:**

| Parameter | Type | Description |
|---|---|---|
| `since_ns` | int (nanoseconds) | Return only keys with a nanosecond timestamp ≥ this value. |
| `until_ns` | int (nanoseconds) | Return only keys with a nanosecond timestamp ≤ this value. |
| `prefix` | string | Return only keys whose filename starts with this string. |
| `contains` | string | Return only keys whose filename contains this string. |

All parameters are optional and combinable. Keys that do not parse as `<nanosecond>.json` are excluded when `since_ns` or `until_ns` is provided.

**Example — events written in the last hour:**

```bash
since=$(( ($(date +%s) - 3600) * 1000000000 ))
curl "http://localhost:8080/sites/local/events?since_ns=${since}"
# {"events":["...nanosecond....json"],"next_cursor":"","site_id":"local"}
```

**Example — first 10 artifacts whose filename contains "build":**

```bash
curl "http://localhost:8080/sites/local/artifacts?contains=build&limit=10"
```

**Error responses:**

| Condition | Status |
|---|---|
| `since_ns` or `until_ns` is not a non-negative integer | `400 Bad Request` |
| `since_ns` > `until_ns` | `400 Bad Request` |

---

### Segment 11C — Webhook Subscription Caching

When `AIISTECH_WEBHOOK_BASE_URL` is set, the webhook dispatcher fetches active subscriptions from PhaseMirror-HQ before each delivery. Without caching this produces one HTTP round-trip to the subscription API _per event per worker_. Under sustained load (e.g. many rapid deploys) this would hit the upstream API harder than necessary.

Segment 11C wraps `RemoteProvider` in a `CachingProvider` that retains subscription lists in memory for a configurable TTL. Subsequent events within the TTL window read from the cache without calling the API.

**Behaviour:**

- Cache key: `(service, eventType, tenantID)` — distinct event types are cached independently.
- TTL: controlled by `AIISTECH_WEBHOOK_CACHE_TTL_SECONDS` (default **30 s**).
- Errors from the upstream API are **not** cached; the next event will retry immediately.
- Eviction is lazy (on the next read after expiry); no background goroutine is added.
- The implementation is safe for concurrent use by all dispatcher workers.

**Example — reduce cache TTL for high-churn environments:**

```bash
AIISTECH_WEBHOOK_CACHE_TTL_SECONDS=10 go run ./cmd/server
```

**Example — disable effective caching (cache for 1 second) for debugging:**

```bash
AIISTECH_WEBHOOK_CACHE_TTL_SECONDS=1 go run ./cmd/server
```

---

### Segment 13 — Background Subscription Polling

`CachingProvider` (Segment 11C) uses lazy eviction: subscriptions are re-fetched
only after a cache entry expires and the next event dispatch triggers a miss.
Under sustained event bursts this produces brief dispatch-path latency spikes
every TTL interval, and multiple concurrent workers can all miss simultaneously.

Segment 13 adds an **optional background polling goroutine** inside
`CachingProvider` that proactively re-fetches every known cache key on a
configurable interval. The goroutine runs only when
`AIISTECH_WEBHOOK_POLL_INTERVAL_SECONDS > 0` and is stopped cleanly on
shutdown via `CachingProvider.Close()`.

**Behaviour:**

- Background goroutine starts at construction time when `pollInterval > 0`;
  zero means lazy-only (Segment 11C behaviour, the default).
- Only keys that have been seen at least once (populated on a first lazy miss)
  are refreshed by the poller.
- On a poll error the existing valid cache entry is **preserved** and the error
  is logged at WARN; a transient HQ outage does not disrupt active dispatch.
- `Close()` on `CachingProvider` signals the goroutine to stop and blocks until
  it exits. In `main.go` this runs before `Dispatcher.Close()` to ensure no
  in-flight deliveries encounter a torn-down provider.

**Example — enable background polling every 20 seconds:**

```bash
AIISTECH_WEBHOOK_POLL_INTERVAL_SECONDS=20 go run ./cmd/server
```

**Example — polling with a longer TTL (cache acts as a fallback, not a gate):**

```bash
AIISTECH_WEBHOOK_CACHE_TTL_SECONDS=120 AIISTECH_WEBHOOK_POLL_INTERVAL_SECONDS=60 go run ./cmd/server
```

---

## Roadmap

There are no further planned segments at this time.

---

## Tests

```bash
go test ./...
```
