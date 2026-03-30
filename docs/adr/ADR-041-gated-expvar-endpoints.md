# ADR-041 — Gated Expvar Endpoints (`/metrics` and `/debug/vars`)

## Status
Accepted

## Context

The server has long exposed `GET /metrics` unconditionally, serving `expvar.Handler()` JSON. This endpoint reveals process internals (request counters, goroutine counts, memory stats, and any expvar published by application code), which is potentially sensitive information that should not be publicly readable.

ADR-040 introduced `AdminAuthMiddleware` and `AIISTECH_ADMIN_API_KEY` to protect the DLQ and subscription management endpoints. The same mechanism can protect the expvar endpoints without any new infrastructure.

There is also a conventional Go path for expvar, `/debug/vars`, that operators and tooling may expect. Both paths should serve the same expvar JSON for compatibility.

## Decision

1. **Mount `/metrics` and `/debug/vars` only when `AIISTECH_ADMIN_API_KEY` is set.**
   When the environment variable is absent (e.g. local development without explicit intent to expose metrics), both routes are simply not registered and return `404`.

2. **When mounted, both endpoints are protected by `AdminAuthMiddleware(adminAPIKey)`.**
   Every request (regardless of HTTP method) must supply `Authorization: Bearer <key>`.
   This is the same token used to protect `/webhooks/dlq/*` and `/webhooks/subscriptions/*`.

3. **Both paths serve the same handler (`MetricsHandler` → `expvar.Handler()`).**
   `/metrics` preserves backward compatibility; `/debug/vars` matches the canonical Go convention and tooling expectations.

## Consequences

- **Security improvement:** process internals are no longer publicly readable. An attacker who can reach the server port cannot enumerate expvar counters without knowing the admin key.
- **Operational change:** deployments that scrape `/metrics` without a token will start receiving `404` (if no key is set) or `401` (if a key is set). Operators must either set `AIISTECH_ADMIN_API_KEY` and pass the token to their scraper, or rely on alternative observability tooling.
- **Key management:** `AIISTECH_ADMIN_API_KEY` must be treated as a secret. It must **not** be committed to source control. Use a secrets manager, CI/CD secrets (e.g. GitHub Actions secrets), or Docker/Kubernetes secrets to inject it at runtime. See `.env.example` for generation guidance.
- **No key material is logged or stored** in this codebase. The `AdminAuthMiddleware` logs only the path and method on auth failure, never the presented token.

## Alternatives Considered

- **Always mount but gate with `AdminAuthMiddleware`:** rejected because when `AIISTECH_ADMIN_API_KEY` is unset the middleware is a no-op, so the endpoint would still be publicly readable without a key configured.
- **Separate key for metrics:** adds operational complexity for no meaningful security gain given that the existing admin key is already a strong shared secret.
- **Prometheus `/metrics`:** out of scope for this ADR. If Prometheus is adopted in the future, `/metrics` can be reassigned and expvar can live exclusively at `/debug/vars`.
