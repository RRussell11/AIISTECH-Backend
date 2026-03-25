# ADR-017: Tenant-scoped authentication (per-tenant API keys)

## Status
Accepted

## Context
This service supports multi-tenancy via a tenant identifier (`X-Tenant-ID`). Because the backend is integrated with a main system that serves third parties/business partners, tenant isolation must be enforced to prevent cross-tenant access and tenant spoofing.

## Decision
Add per-site tenant configuration in `contracts/sites/<site_id>/config.yaml`:

- `tenants: [{ tenant_id, api_key }]`

When `tenants` is non-empty ("tenant mode"):

- Every request to `/sites/{site_id}/...` MUST include:
  - `X-Tenant-ID: <tenant_id>`
  - `Authorization: Bearer <api_key>`
- Requests are rejected when:
  - `X-Tenant-ID` is missing/empty or unknown for the site → `400 Bad Request`
  - Authorization is missing/invalid or does not match the configured tenant key → `401 Unauthorized`
- The resolved `TenantID` is attached to `site.SiteContext` for downstream handlers.

When `tenants` is empty/missing ("legacy mode"), existing site-level authentication behavior remains unchanged.

## Consequences
- Tenant identity is bound to credentials (no header-only tenant spoofing).
- Enables safe third-party integration and tenant-level auditing/partitioning.
- Requires updating site config files to enable tenant mode.
