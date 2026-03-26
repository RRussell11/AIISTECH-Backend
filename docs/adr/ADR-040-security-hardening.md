# ADR-040 — Security Hardening

## Status

Accepted

## Context

As of Segment 39 the project is functionally complete. Before shipping, a
security review identified four gaps:

1. **Timing attack on API-key comparison** — `AuthMiddleware` compared the
   caller's Bearer token with the configured site API key using the `!=`
   operator. String comparison in Go short-circuits on the first differing byte,
   leaking information about the shared prefix of the expected key through
   response-time differences.

2. **Admin routes unauthenticated** — The DLQ management endpoints
   (`/webhooks/dlq/*`) and subscription management endpoints
   (`/webhooks/subscriptions/*`) were mounted without any authentication gate.
   Any client with network access could read, modify, or replay webhook records
   and create or delete subscriptions.

3. **No security response headers** — Responses carried no OWASP-recommended
   security headers (`X-Content-Type-Options`, `X-Frame-Options`, etc.), leaving
   browsers open to MIME-sniffing and framing attacks.

4. **Unbounded request bodies** — There was no cap on incoming request-body
   size, enabling a trivial resource-exhaustion attack by posting extremely large
   bodies.

## Decision

### 1 — Constant-time token comparison

`AuthMiddleware` now uses `crypto/subtle.ConstantTimeCompare` when checking the
caller's Bearer token against the site API key. The comparison is O(n) in the
length of the key regardless of where a mismatch occurs, eliminating the timing
side-channel.

### 2 — Admin API key (`AdminAuthMiddleware`)

A new `AdminAuthMiddleware(apiKey string)` is applied to all routes under
`/webhooks/dlq` and `/webhooks/subscriptions`. Unlike `AuthMiddleware` (which
only gates mutating methods), `AdminAuthMiddleware` gates **every** HTTP method
because listing subscriptions and DLQ records is equally sensitive.

The key is read from `AIISTECH_ADMIN_API_KEY`. When the variable is unset the
middleware is a transparent no-op (all requests pass through), preserving
backward compatibility for local development and deployments that have not yet
set the key. The server logs a `WARN` at startup when the key is absent and
admin routes are active, so operators are aware.

Authentication uses the same `Authorization: Bearer <token>` header convention
already used by `AuthMiddleware`. Token comparison uses `subtle.ConstantTimeCompare`.

### 3 — Security response headers (`SecurityHeadersMiddleware`)

A new `SecurityHeadersMiddleware` is applied globally (before site routing). It
sets the following headers on every response:

| Header | Value | Rationale |
|---|---|---|
| `X-Content-Type-Options` | `nosniff` | Prevent MIME-type sniffing in browsers |
| `X-Frame-Options` | `DENY` | Prevent clickjacking via iframes |
| `X-XSS-Protection` | `0` | Disable the broken legacy XSS auditor; modern browsers rely on CSP |
| `Referrer-Policy` | `strict-origin-when-cross-origin` | Limit referrer leakage to cross-origin requests |

### 4 — Request body size limit (`MaxBytesMiddleware`)

A new `MaxBytesMiddleware(n int64)` wraps each request's body with
`http.MaxBytesReader`. The default limit is **1 MiB** (`maxRequestBodyBytes =
1 << 20`). When a handler reads the body beyond this limit (e.g., via
`json.Decoder`) the read returns an error and the handler responds with 400 Bad
Request, preventing the server from buffering arbitrary amounts of attacker-
controlled data.

## Wiring

`NewRouter` gains an `adminAPIKey string` parameter. The global middleware chain
is now:

```
Recoverer → RequestID → MetricsMiddleware → SecurityHeadersMiddleware → MaxBytesMiddleware(1 MiB)
```

The DLQ and subscription sub-routers each prepend:

```
AdminAuthMiddleware(adminAPIKey)
```

`main.go` reads `AIISTECH_ADMIN_API_KEY` and passes it to `NewRouter`.

## Consequences

- **Positive:** The attack surface of the public-facing server is materially
  reduced. The most critical gap (unauthenticated admin routes) is closed with a
  simple, auditable mechanism consistent with existing auth conventions.
- **Positive:** Timing attacks on the site API key check are eliminated at no
  measurable performance cost.
- **Positive:** OWASP-recommended headers are emitted without any per-handler
  changes.
- **Positive:** Resource-exhaustion via oversized bodies is mitigated.
- **Neutral:** When `AIISTECH_ADMIN_API_KEY` is unset the admin routes behave
  exactly as before (open); operators must opt in to the protection by setting
  the variable. A startup warning makes the open state visible.
- **Neutral:** `NewRouter` gains one additional parameter; all four existing
  test helper functions are updated accordingly.
