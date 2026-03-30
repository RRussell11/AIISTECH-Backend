# AIISTECH-Backend

A Go HTTP backend implementing the **Site as Tenant / Governance Namespace** pattern.  
All stateful operations are scoped by an explicit `site_id`.

## Table of Contents

- [Requirements](#requirements)
- [Quick Start](#quick-start)
- [Docker Compose (Production)](#docker-compose-production)
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
- [Roadmap](#roadmap)
- [Tests](#tests)
- [Deployment (GitHub Actions)](#deployment-github-actions)

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

## Docker Compose (Production)

Use Docker Compose to run the backend in production, with the required environment variables wired in via a `.env` file.

```bash
# 1. Copy the example env file and fill in your values
cp .env.example .env

# 2. Set the required variables in .env
#    AIISTECH_WEBHOOK_BASE_URL=https://your-public-url   (no trailing slash)
#    AIISTECH_WEBHOOK_TOKEN=<openssl rand -hex 32>

# 3. Build and start the service in the background
docker compose up -d --build
```

The service listens on port **8080** inside the container, mapped to `8080` on the host.  
`AIISTECH_WEBHOOK_BASE_URL` and `AIISTECH_WEBHOOK_TOKEN` are required when the backend is linked to PhaseMirror-HQ / the remote provider.

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `AIISTECH_ADDR` | `:8080` | Listen address |
| `AIISTECH_REGISTRY_PATH` | `contracts/shared/sites.yaml` | Path to site registry file |
| `AIISTECH_LOG_LEVEL` | `INFO` | Structured log verbosity (`DEBUG`, `INFO`, `WARN`, `ERROR`) |
| `AIISTECH_SERVICE_NAME` | `aiistech-backend` | Logical service name sent to the subscription API |
| `AIISTECH_WEBHOOK_BASE_URL` | *(unset)* | PhaseMirror-HQ base URL — enables `RemoteProvider` |
| `AIISTECH_WEBHOOK_TOKEN` | *(unset)* | Bearer token for the PhaseMirror-HQ subscription API |
| `AIISTECH_WEBHOOK_STORE_PROVIDER` | *(unset)* | Set to `true` to enable `StoreProvider` (local bbolt subscriptions) |
| `AIISTECH_WEBHOOK_SUBSCRIPTIONS_DB` | `var/state/webhooks/subscriptions.db` | bbolt database path for local subscriptions and DLQ |
| `AIISTECH_ADMIN_API_KEY` | *(unset)* | Bearer token protecting `/webhooks/dlq/*` and `/webhooks/subscriptions/*`; when unset those routes are unauthenticated (a startup warning is logged) |

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

## Roadmap

All planned segments are complete. The project is production-ready.

---

### Segment 40 — Security Hardening

> Harden the server's security posture before final release.

- **Timing-safe token comparison** — `AuthMiddleware` now uses
  `crypto/subtle.ConstantTimeCompare` for all Bearer-token checks, eliminating
  the timing side-channel present in plain string equality.

- **Admin API key** (`AIISTECH_ADMIN_API_KEY`) — the DLQ management and
  subscription management endpoints are now gated by a dedicated Bearer key.
  Unlike site auth, **every method** (including `GET`) requires authentication
  because listing subscriptions and DLQ records is equally sensitive.  When the
  variable is unset the routes remain accessible and a `WARN` is logged at
  startup.

  ```bash
  export AIISTECH_ADMIN_API_KEY=changeme
  # All /webhooks/dlq/* and /webhooks/subscriptions/* requests now require:
  #   Authorization: Bearer changeme
  ```

- **Security response headers** — `SecurityHeadersMiddleware` is applied
  globally and sets `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
  `X-XSS-Protection: 0`, and `Referrer-Policy: strict-origin-when-cross-origin`
  on every response.

- **Request body size cap** — `MaxBytesMiddleware` caps incoming request bodies
  at **1 MiB** using `http.MaxBytesReader`, preventing resource exhaustion from
  oversized payloads.

---

### Segment 37 — MultiProvider & Event-Type Filtering

> Unify local and remote webhook subscriptions. Add `"*"` wildcard event-type filter.

- **`AIISTECH_WEBHOOK_STORE_PROVIDER=true`** enables a local bbolt-backed subscription store.
- **`AIISTECH_WEBHOOK_BASE_URL`** + **`AIISTECH_WEBHOOK_STORE_PROVIDER=true`** enables `MultiProvider` (both sources merged and deduplicated).
- `Events: ["*"]` or empty `Events` in a subscription matches all event types (case-sensitive matching).

---

### Segment 38 — Dead-Letter Queue (DLQ) & Replay

> Failed webhook deliveries are persisted in a DLQ and can be replayed manually or automatically.

The DLQ is enabled automatically when `AIISTECH_WEBHOOK_STORE_PROVIDER=true` (or `MultiProvider`).
All endpoints are mounted at `/webhooks/dlq/`.

#### `GET /webhooks/dlq/`
List all DLQ records. Supports `?cursor=` and `?limit=` pagination.

```
curl http://localhost:8080/webhooks/dlq/
# {"records":[...],"next_cursor":""}
```

#### `GET /webhooks/dlq/{id}`
Get a specific DLQ record.

```
curl http://localhost:8080/webhooks/dlq/1234567890.json
# {"id":"...","subscription_url":"...","event":{"id":"...","type":"audit.write",...},...}
```

#### `DELETE /webhooks/dlq/{id}`
Remove a DLQ record (cancel / discard). Returns `204 No Content`.

```
curl -X DELETE http://localhost:8080/webhooks/dlq/1234567890.json
# HTTP 204
```

#### `POST /webhooks/dlq/{id}/replay`
Replay a single DLQ record. On success the record is deleted and `200` is returned.
On failure `502 Bad Gateway` is returned and the record is updated with the new error.

```
curl -X POST http://localhost:8080/webhooks/dlq/1234567890.json/replay
# {"id":"1234567890.json","status":"delivered"}
# or on failure:
# {"error":"...","id":"1234567890.json","status":"failed"}
```

#### `POST /webhooks/dlq/replay-all`
Replay all DLQ records concurrently (up to 8 goroutines). Returns a summary.

```
curl -X POST http://localhost:8080/webhooks/dlq/replay-all
# {"failed":0,"results":[...],"succeeded":2,"total":2}
```

**Expvar metrics for DLQ** (visible at `GET /metrics`):

| Key | Description |
|---|---|
| `webhook_dlq_stored_total` | Total records written to the DLQ |
| `webhook_dlq_replay_success_total` | Total records successfully replayed |
| `webhook_dlq_replay_failure_total` | Total replay attempts that failed |

**Auto-retry scheduler:** When the DLQ store is configured, a background goroutine
scans every 60 seconds for eligible records (`NextRetryAfter ≤ now`) and replays
them automatically with exponential back-off (base 5 min, doubles per attempt, capped
at 24 h). Records that exceed 10 replay attempts are marked terminal and skipped by
the scheduler (manual replay is still possible).

---

### Segment 39 — Subscription Management HTTP API

> Manage webhook subscriptions at runtime via HTTP — no server restart required.

Subscription routes are mounted at `/webhooks/subscriptions/` only when
`AIISTECH_WEBHOOK_STORE_PROVIDER=true`.

#### `GET /webhooks/subscriptions/`
List all subscriptions with cursor pagination.

```
curl http://localhost:8080/webhooks/subscriptions/
# {"subscriptions":[...],"next_cursor":""}
```

#### `POST /webhooks/subscriptions/`
Create a new subscription. `service` and `url` are required.

```
curl -X POST http://localhost:8080/webhooks/subscriptions/ \
  -H 'Content-Type: application/json' \
  -d '{"service":"aiistech-backend","url":"https://example.com/hook","events":["audit.write"]}'
# HTTP 201
# {"id":"sub_<ns>","service":"aiistech-backend","url":"...","enabled":true,...}
```

#### `GET /webhooks/subscriptions/{id}`
Get a specific subscription.

```
curl http://localhost:8080/webhooks/subscriptions/sub_12345
```

#### `PATCH /webhooks/subscriptions/{id}`
Partially update a subscription. Only provided fields are changed.

```
curl -X PATCH http://localhost:8080/webhooks/subscriptions/sub_12345 \
  -H 'Content-Type: application/json' \
  -d '{"enabled":false,"events":["audit.write","artifact.write"]}'
# HTTP 200 — returns full updated subscription
```

#### `DELETE /webhooks/subscriptions/{id}`
Delete a subscription. Returns `204 No Content`.

```
curl -X DELETE http://localhost:8080/webhooks/subscriptions/sub_12345
# HTTP 204
```

---

## Tests

```bash
go test ./...
```

---

## Deployment (GitHub Actions)

Two deployment workflows are provided under `.github/workflows/`. Both trigger on **push to `main`** and gate the deploy job behind the `production` GitHub Environment (supporting required reviewers and admin bypass).

### GitHub Environment setup

1. In your repository go to **Settings → Environments → New environment** and name it `production`.
2. Add **Required reviewers** (at least 1) and, if desired, enable **"Allow administrators to bypass configured protection rules"**.
3. Add the secrets listed below **to the environment** (preferred) or as repository secrets.

### Required secrets

| Secret | Description |
|---|---|
| `PROD_SSH_HOST` | Public IP or DNS name of the production VM |
| `PROD_SSH_USER` | SSH login user (e.g. `ubuntu`) |
| `PROD_SSH_KEY` | Private SSH key used to authenticate (PEM format, full contents) |
| `PROD_DEPLOY_PATH` | Absolute path on the VM where the repo / compose file lives (e.g. `/opt/aiistech-backend`) |
| `AIISTECH_ADMIN_API_KEY` | Bearer token protecting `/webhooks/*` and `/metrics` — **never commit this value** |

> ⚠️ **Never commit `.env` to the repository.** Never add `set -x` or `env` to any deploy script — doing so would print secrets to the workflow log.

---

### Option 1 — Build on the VM (`deploy-vm-build.yml`)

Use this when you want the simplest possible pipeline with no container registry.

**Jobs:**

1. `ci` — runs `go vet`, `go test`, `go build`.
2. `deploy-vm-build` (`needs: ci`, `environment: production`) — SSHes into the VM and runs:

```bash
cd "$DEPLOY_PATH"
git fetch --all
git checkout main
git pull --ff-only
docker compose up -d --build
```

**Minimal server setup:**

```bash
# Install Docker (Ubuntu 22.04)
sudo apt-get update
sudo apt-get install -y ca-certificates curl
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | sudo tee /etc/apt/sources.list.d/docker.list
sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

# Add deploy user to docker group (replace 'ubuntu' as needed)
sudo usermod -aG docker ubuntu

# Clone the repo
sudo git clone https://github.com/RRussell11/AIISTECH-Backend.git /opt/aiistech-backend
sudo chown -R ubuntu:ubuntu /opt/aiistech-backend

# Create the .env file — never commit this
cat > /opt/aiistech-backend/.env <<'EOF'
AIISTECH_ADMIN_API_KEY=<your-secret-key>
AIISTECH_WEBHOOK_BASE_URL=https://your-public-url
AIISTECH_WEBHOOK_TOKEN=<openssl rand -hex 32>
EOF
chmod 600 /opt/aiistech-backend/.env
```

The existing `docker-compose.yml` (with `build:`) is used as-is for this option.

---

### Option 2 — Build in GitHub Actions, push to GHCR, VM pulls (`deploy-ghcr.yml`)

Use this for immutable, reproducible images and faster server deploys.

**Jobs:**

1. `ci` — runs `go vet`, `go test`, `go build`.
2. `build-and-push-image` (`needs: ci`) — builds the Docker image, logs into GHCR with `GITHUB_TOKEN`, and pushes two tags:
   - `ghcr.io/rrussell11/aiistech-backend:main`
   - `ghcr.io/rrussell11/aiistech-backend:sha-<shortsha>`
3. `deploy-vm-pull` (`needs: build-and-push-image`, `environment: production`) — SSHes into the VM and runs:

```bash
cd "$DEPLOY_PATH"
docker compose pull
docker compose up -d
```

**Server-side `docker-compose.yml` for this option** — replace the `build:` block with an `image:` reference:

```yaml
services:
  aiistech-backend:
    image: ghcr.io/rrussell11/aiistech-backend:main
    env_file:
      - .env
    environment:
      AIISTECH_WEBHOOK_BASE_URL: ${AIISTECH_WEBHOOK_BASE_URL}
      AIISTECH_WEBHOOK_TOKEN: ${AIISTECH_WEBHOOK_TOKEN}
    ports:
      - "8080:8080"
    restart: unless-stopped
```

**Additional server-side step — authenticate to GHCR** (one-time, so the VM can `docker pull` the image):

```bash
# On the VM, log in with a GitHub Personal Access Token (read:packages scope)
echo "$GHCR_PAT" | docker login ghcr.io -u <github-username> --password-stdin
```

The `.env` file setup is the same as Option 1 above.

