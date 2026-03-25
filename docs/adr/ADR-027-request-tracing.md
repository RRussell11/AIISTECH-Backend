# ADR-027: Request Tracing via Structured Log Enrichment

## Status
Accepted

## Context

All HTTP log lines were written via the global `slog.Default()` without any
request-scoped fields. Correlating log entries for a single request required
cross-referencing by timestamp or URL path alone, making debugging difficult
in production and in tests.

Chi's `middleware.RequestID` (wired since the initial router setup) already
injects a unique `X-Request-Id` header into every request and the
`AuditMiddleware` captures it in audit entries. However, this trace ID was
not propagated to the structured log output emitted by other middleware or
handlers, so most log lines had no way to be linked to a specific request.

## Decision

Add `TraceMiddleware` to the global middleware chain immediately after
`middleware.RequestID`. For every request it:

1. Reads the request ID set by chi via `chimiddleware.GetReqID(r.Context())`.
2. Constructs a `*slog.Logger` with `"trace_id"` pre-baked using
   `slog.Default().With("trace_id", traceID)`.
3. Stores the enriched logger in the request context under an unexported
   `loggerKey{}`.
4. Wraps the `http.ResponseWriter` with the existing `statusRecorder` to
   capture the response status code.
5. On handler return, emits one structured access-log line:

```
level=INFO msg=request trace_id=<id> method=GET path=/sites/local/events status=200 latency_ms=3
```

A companion exported helper `LoggerFromContext(ctx context.Context) *slog.Logger`
retrieves the stored logger. It falls back to `slog.Default()` for contexts
that have not passed through the middleware (e.g. unit tests).

### Middleware chain order (after this ADR)

| # | Middleware           | Purpose                                   |
|---|----------------------|-------------------------------------------|
| 1 | `Recoverer`          | Panic recovery                            |
| 2 | `RequestID`          | Injects `X-Request-Id`                    |
| 3 | `TraceMiddleware`    | Context logger + access log ← **new**     |
| 4 | `MetricsMiddleware`  | expvar request counters                   |
| 5 | `CORSMiddleware`     | CORS headers (optional)                   |
| 6 | `MaxBodyMiddleware`  | Body size cap (optional)                  |
| 7 | `RateLimitMiddleware`| Per-IP rate limit (optional)              |

## Consequences

* Every request produces exactly one access-log line that includes
  `trace_id`, `method`, `path`, HTTP `status`, and `latency_ms`.
* Handlers and other middleware can call `LoggerFromContext(r.Context())` to
  emit additional log lines that share the same `trace_id`, enabling full
  request correlation across a single lifecycle.
* The `X-Request-Id` header is returned to clients by chi's `RequestID`
  middleware, so callers can supply their own ID for end-to-end correlation.
* No new dependencies are introduced; the existing `log/slog` standard
  library and chi packages are sufficient.
* `statusRecorder` (already used by `AuditMiddleware`) is reused inside
  `TraceMiddleware`; both middleware layers create their own recorder
  instance, which is safe because `WriteHeader` propagates through the chain.
