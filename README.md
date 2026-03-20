# AIISTECH-Backend

A Go HTTP backend implementing the **Site as Tenant / Governance Namespace** pattern.  
All stateful operations are scoped by an explicit `site_id`.

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

The server reads the site registry from `contracts/shared/sites.yaml` on startup.

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `AIISTECH_ADDR` | `:8080` | Listen address |
| `AIISTECH_REGISTRY_PATH` | `contracts/shared/sites.yaml` | Path to site registry file |
| `AIISTECH_SITE_ID` | _(registry default)_ | Override site selection for non-scoped operations |

## API

### `GET /healthz`
Non-site-scoped health check.

```
curl http://localhost:8080/healthz
# {"status":"ok"}
```

### `GET /sites`
List all registered sites and the default site.

```
curl http://localhost:8080/sites
# {"default_site_id":"local","sites":["local","prod","staging"]}
```

### `GET /sites/{site_id}`
Get site metadata including its state root path.

```
curl http://localhost:8080/sites/local/
# {"site_id":"local","state_root":"var/state/local"}
```

### `GET /sites/{site_id}/healthz`
Site-scoped health check.

```
curl http://localhost:8080/sites/local/healthz
# {"site_id":"local","status":"ok"}
```

### `POST /sites/{site_id}/events`
Write a JSON event to `var/state/<site_id>/events/<timestamp>.json`.

```
curl -X POST http://localhost:8080/sites/local/events \
  -H 'Content-Type: application/json' \
  -d '{"event":"deploy","version":"1.0.0"}'
# {"file":"...json","site_id":"local","status":"created"}
```

### `GET /sites/{site_id}/events`
List all event filenames for a site (sorted ascending).

```
curl http://localhost:8080/sites/local/events
# {"events":["1234.json"],"site_id":"local"}
```

### `GET /sites/{site_id}/events/{filename}`
Fetch the contents of a specific event file.

```
curl http://localhost:8080/sites/local/events/1234.json
# {"event":"deploy","version":"1.0.0"}
```

Unknown or invalid `site_id` values return a `404` with a clear error message.

## Site Registry

Valid sites are defined in `contracts/shared/sites.yaml`:

```yaml
default_site_id: "local"
sites:
  - site_id: "local"
  - site_id: "staging"
  - site_id: "prod"
```

Site IDs must be non-empty and must not contain `..`, `/`, or `\`.

## Project Structure

```
cmd/server/         # Server entrypoint
contracts/shared/   # Contract-owned site registry (sites.yaml)
internal/
  http/             # Router, middleware, handlers (+ integration tests)
  site/             # Registry loader, validator, resolver, context helpers
  state/            # Per-site state path helpers (events, artifacts, audit)
var/state/          # Runtime state (gitignored); layout: var/state/<site_id>/...
```

## Tests

```bash
go test ./...
```
