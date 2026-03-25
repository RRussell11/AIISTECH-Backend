# ADR-019: Tenant-scoped storage key namespacing

## Status
Accepted

## Context
Following Segment 17 (per-tenant API keys) and Segment 18 (tenant-aware audit entries), the storage layer still wrote events and artifacts to a flat key space shared across all tenants of a site. A tenant could potentially read another tenant's records by guessing filenames, and a bug in a handler could cause cross-tenant data leakage.

For a backend used by third parties via a main orchestrator, physical data partitioning is required to guarantee tenant isolation at the storage layer.

## Decision
When `SiteContext.TenantID` is non-empty (tenant mode), all storage keys for events and artifacts are prefixed with `<tenantID>/` before being written to or read from the store:

- **Writes** (`POST /events`, `POST /artifacts`): the generated key is stored as `<tenantID>/<ns>.json`; the response `file` field returns the bare key without the prefix so clients can use it in subsequent `GET` requests.
- **Lists** (`GET /events`, `GET /artifacts`, `GET /audit`): a tenant prefix filter is automatically injected so only keys belonging to the requesting tenant are returned. The prefix is stripped from keys and `next_cursor` before the response is sent to the client.
- **Reads/Deletes** (`GET /events/{filename}`, `DELETE /artifacts/{filename}`, etc.): the handler prepends the tenant prefix internally before looking up the key, so the URL contract remains `{filename}` (bare key without prefix).
- **Audit entries** (Segment 18): already namespaced by tenant ID in `audit.Write`.

Legacy mode (no tenants configured): all paths are unchanged — no prefix is applied.

## Consequences
- Tenant data is physically separated within the same bbolt store bucket; tenant A cannot access tenant B's data even if key names collide.
- The client-visible API (URL paths, response `file` fields, cursor values) uses bare keys without tenant prefixes, keeping the API clean.
- Cursor-based pagination works correctly: bare cursors from responses are re-prefixed internally before storage lookup.
- No migration is required for existing data written in legacy mode.
- Tenant mode and legacy mode are safely co-operable: sites without tenant config continue to use flat key space.
