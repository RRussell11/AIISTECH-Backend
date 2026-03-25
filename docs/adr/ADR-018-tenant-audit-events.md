# ADR-018: Tenant-aware audit trail and webhook events

## Status
Accepted

## Context
Segment 17 introduced per-tenant API key authentication, establishing tenant identity in `SiteContext.TenantID`. However, audit log entries and outbound webhook events carried no tenant information, making it impossible to attribute records or subscriptions to a specific tenant.

For a backend connected to a main system serving third parties, compliance and traceability require that every audit entry and every dispatched webhook event be labelled with the originating tenant.

## Decision
- Add `TenantID string` to `audit.Entry` (omitted from JSON when empty for backward compatibility).
- When `TenantID` is non-empty, `audit.Write` prefixes the storage key with `<tenantID>/`, so audit records are physically partitioned per tenant within the site store.
- Add `TenantID string` to `webhooks.Event` (omitted from JSON when empty).
- `AuditMiddleware` populates `TenantID` on both the `audit.Entry` it writes and the `webhooks.Event` it dispatches.
- `WorkerDispatcher.process` passes `evt.TenantID` to `Provider.ListSubscriptions` (previously hardcoded `""`), enabling tenant-scoped subscription filtering.

## Consequences
- Every audit record produced in tenant mode identifies the responsible tenant.
- Webhook subscribers can receive and filter events by tenant ID.
- Legacy (non-tenant) entries are unaffected: `TenantID` is empty and keys remain unprefixed.
- Audit log storage is now implicitly partitioned by tenant (no migration required; new records go to the new path, old records remain accessible).
