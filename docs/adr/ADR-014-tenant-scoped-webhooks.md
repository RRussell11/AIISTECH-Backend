# ADR-014: Tenant-Scoped Webhook Dispatch

**Status:** Accepted  
**Date:** 2026-03-24  
**Segment:** 14

## Context

The AIISTECH Backend serves multiple sites and may serve multiple tenants within each site. Webhook subscriptions stored in PhaseMirror-HQ already carry an optional `tenant_id` field. Prior to this ADR, subscription fetching in `WorkerDispatcher` always passed an empty string for `tenantID`, meaning all subscriptions were returned regardless of tenant scope.

To enable per-tenant webhook delivery, tenant identity must be threaded from the inbound HTTP request through the middleware stack and into the dispatcher's subscription-fetching call.

## Decision

### A) Tenant identification via `X-Tenant-ID` header

Tenant identity is read from the `X-Tenant-ID` HTTP request header by `SiteMiddleware`. The extracted value is stored in `SiteContext.TenantID` and propagates through the request context for the lifetime of the request.

Rationale for using a header:
- Consistent with common multi-tenancy patterns (e.g., Stripe's `Stripe-Account`, Twilio tenant headers).
- Does not require URL path changes or query string modifications.
- Easy to set/override by upstream reverse proxies or API gateways.

### B) Default/fallback behavior

When the `X-Tenant-ID` header is absent or empty, `TenantID` defaults to `""` (empty string). Downstream components treat `""` as the global/default bucket, preserving backwards compatibility. No request is rejected solely due to a missing tenant header.

### C) Event and dispatcher propagation

`webhooks.Event` now includes a `TenantID string` field (`json:"tenant_id,omitempty"`). `AuditMiddleware` populates this from `SiteContext.TenantID`. `WorkerDispatcher.process` passes `evt.TenantID` to `Provider.ListSubscriptions`, replacing the previously hardcoded `""`.

## Security Implications

**The `X-Tenant-ID` header is trusted without cryptographic verification in this implementation.** Any client that can reach the server can claim any tenant identity.

Recommended mitigations (out of scope for this PR):
- Verify tenant identity upstream in an API gateway or reverse proxy (e.g., validate a JWT claim and rewrite/inject the `X-Tenant-ID` header before forwarding).
- Add an allowlist of valid tenant IDs to the site configuration and reject unknown values in `SiteMiddleware`.
- Treat tenant scoping as a routing hint only, not as a security boundary, until upstream verification is in place.

## Compatibility Considerations

- Existing deployments that do not set `X-Tenant-ID` are unaffected: `TenantID` defaults to `""`, and `ListSubscriptions` receives the same empty-string tenant it did before.
- The `tenant_id` field in `webhooks.Event` JSON is serialized with `omitempty`, so existing consumers that don't expect it will not see it unless a non-empty tenant ID is present.
- The `Subscription.TenantID` field was already present in `types.go` (reserved in an earlier segment); this ADR activates its use.
