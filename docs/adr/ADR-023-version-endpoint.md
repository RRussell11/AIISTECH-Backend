# ADR-023: Build-time version endpoint (GET /version)

## Status
Accepted

## Context
Operators and tooling need a lightweight way to confirm which exact build is
running in a given environment — without having to inspect the process list,
read image labels, or shell into the container.  The `/healthz` endpoint
already provides a liveness signal, but it carries no release identity
information.

## Decision
Add a `GET /version` endpoint (Segment 23) and an `internal/version` package
that exposes three build-time variables.

### `internal/version` package

```go
var Version   = "dev"   // human-readable tag, e.g. "v1.2.3"
var Commit    = "none"  // short Git SHA
var BuildTime = ""      // RFC3339 timestamp of the build
```

The variables default to safe, descriptive values for development and CI builds
where `-ldflags` are not supplied.

### `GET /version` handler

Registered as a non-site-scoped, unauthenticated route (alongside `/healthz`
and `/metrics`) at the global router level.  No middleware wrapping is needed.

Response (HTTP 200):

```json
{
  "version":    "v1.2.3",
  "commit":     "abc1234",
  "build_time": "2026-03-25T16:04:55Z"
}
```

### Dockerfile injection

Three `ARG` declarations (`VERSION`, `COMMIT`, `BUILD_TIME`) are added to the
builder stage.  The `go build` command passes them into the binary via
`-ldflags`:

```dockerfile
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=""
RUN ... -ldflags="... \
  -X .../internal/version.Version=${VERSION} \
  -X .../internal/version.Commit=${COMMIT} \
  -X .../internal/version.BuildTime=${BUILD_TIME}" ...
```

Docker build invocation for a release:

```bash
docker build \
  --build-arg VERSION=v1.2.3 \
  --build-arg COMMIT=$(git rev-parse --short HEAD) \
  --build-arg BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
  -t aiistech-backend:v1.2.3 .
```

When `ARG` values are omitted (local development, CI) the defaults produce a
`"dev"` build, which is safe and unambiguous.

## Consequences
- Any deployment toolchain (Kubernetes health checks, smoke tests, dashboards)
  can confirm the running version with a single HTTP GET.
- No new runtime dependencies are introduced; `internal/version` is a pure Go
  package with no imports.
- The endpoint is intentionally unauthenticated — version strings are not
  sensitive and operators must be able to query it before authentication is
  configured.
