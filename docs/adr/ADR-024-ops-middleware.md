# ADR-024: Ops middleware — CORS, request-body size cap, per-IP rate limiting

## Status
Accepted

## Context
As the server becomes production-facing, three cross-cutting HTTP concerns
need to be handled at the router level rather than inside individual handlers:

1. **CORS** — browser-originated front-ends must be able to reach the API.
   Misconfigured or absent CORS headers either block legitimate browser clients
   or silently allow every origin, both of which are unacceptable.

2. **Request-body size cap** — an unguarded server will buffer arbitrarily large
   request bodies, creating a straightforward resource-exhaustion vector.

3. **Per-IP rate limiting** — without a rate limiter, a single abusive caller can
   monopolise worker goroutines and degrade quality-of-service for all other
   clients.

## Decision
Add three middleware functions to `internal/http/middleware.go` and expose them
through an `OpsConfig` struct that is passed to `NewRouter` as a variadic
argument so all call-sites (including tests) remain backward-compatible.

### `OpsConfig`

```go
type OpsConfig struct {
    CORSOrigins    string  // comma-separated allowed origins; "*" = any; "" = disabled
    MaxBodyBytes   int64   // ≤ 0 = disabled
    RateLimitRPS   float64 // ≤ 0 = disabled
    RateLimitBurst int     // ≤ 0 = max(1, int(RPS))
}
```

### `CORSMiddleware(allowedOrigins string)`

- Parses `allowedOrigins` once at construction time into a `map[string]bool`.
- On each request: if the `Origin` header matches (or `"*"` is configured),
  sets `Access-Control-Allow-Origin`, `Vary: Origin`, and the Allow-Methods /
  Allow-Headers headers.
- Handles pre-flight `OPTIONS` requests with `204 No Content` without calling
  downstream handlers.
- Does **not** trust `X-Forwarded-*` — the raw `Origin` header is used
  directly; CORS is an in-browser mechanism so host-level spoofing is not a
  concern.
- Zero config (empty `allowedOrigins`) is a transparent no-op.

### `MaxBodyMiddleware(maxBytes int64)`

- Wraps `r.Body` with `http.MaxBytesReader` so that any handler that reads
  beyond `maxBytes` bytes receives a `*http.MaxBytesError`.  chi's `Recoverer`
  middleware converts the uncaught panic (or the handler's explicit 400) into
  the appropriate HTTP error.
- `maxBytes ≤ 0` is a transparent no-op.

### `RateLimitMiddleware(rps float64, burst int)`

- Uses a `golang.org/x/time/rate.Limiter` per remote IP address stored in an
  `ipLimiters` map (mutex-protected).
- The remote IP is extracted from `r.RemoteAddr` (port stripped) without
  consulting `X-Forwarded-For` to prevent header-spoofing attacks.  Operators
  running behind a trusted reverse proxy should add a forwarding-aware
  middleware before this one if they need per-client-IP limiting behind a proxy.
- Exhausted limiters reply immediately with `429 Too Many Requests`.
- `rps ≤ 0` is a transparent no-op.

### `NewRouter` signature

```go
func NewRouter(reg, stores, disp, ops ...OpsConfig) http.Handler
```

The variadic `ops` preserves backward compatibility with all existing
call-sites that do not pass an `OpsConfig`.  At most one `OpsConfig` is used;
additional elements are ignored.

### Environment variables wired in `cmd/server/main.go`

| Variable | Middleware | Notes |
|---|---|---|
| `AIISTECH_CORS_ALLOW_ORIGINS` | CORS | comma-separated; `"*"` for any |
| `AIISTECH_MAX_BODY_BYTES` | MaxBody | positive integer; 0 or invalid = disabled |
| `AIISTECH_RATE_LIMIT_RPS` | RateLimit | positive float; 0 or invalid = disabled |
| `AIISTECH_RATE_LIMIT_BURST` | RateLimit | positive int; defaults to max(1,RPS) |

## Consequences
- All three features are **opt-in**: setting no env vars leaves the server
  behaviour unchanged (zero-value `OpsConfig` produces pass-through
  middlewares).
- New dependency: `golang.org/x/time v0.9.0` (no known vulnerabilities; works
  with Go 1.24; no indirect upgrade required).
- The `ipLimiters` map grows indefinitely with unique IPs.  For deployments
  receiving traffic from a very large number of source IPs an LRU eviction
  policy should be added (deferred to a future segment).
- CORS headers are injected **before** site authentication, so pre-flight
  requests for protected routes still receive the CORS headers without
  triggering a 401.
