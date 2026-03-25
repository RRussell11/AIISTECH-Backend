# ADR-032: DELETE /events/{filename} Endpoint

## Status
Accepted

## Context

The events API has a symmetric gap: `POST /events` writes an event and
`GET /events/{filename}` retrieves one, but there is no way to delete a
specific event record without directly manipulating the underlying bbolt
store.  The artifact API already provides a `DELETE /artifacts/{filename}`
endpoint (added in an earlier segment), and the event API should offer the
same capability for operational consistency and data lifecycle management.

Use cases include:
- **Pruning test fixtures** written to a production-like store during
  integration testing without requiring a full database reset.
- **Compliance / data-retention workflows** where specific event records
  must be removed on request.
- **Operational correction** when a malformed or erroneous event was
  accidentally written via `POST /events`.

## Decision

Add `DELETE /sites/{site_id}/events/{filename}` — a single new endpoint
that mirrors `DELETE /artifacts/{filename}` exactly.

### Handler: `DeleteEventHandler`

```go
// internal/http/handlers.go
func DeleteEventHandler(w http.ResponseWriter, r *http.Request) { … }
```

- Validates the `{filename}` path parameter using the existing
  `site.Validate` guard (prevents path traversal).
- Calls `sc.Store.Delete(bucketEvents, tenantKey(sc.TenantID, filename))`.
- Returns **204 No Content** on success.
- Returns **404 Not Found** when the key is absent (`storage.ErrNotFound`).
- Returns **400 Bad Request** for an invalid filename.
- Logs the deletion at INFO level.

### Router wiring

```go
// internal/http/router.go
r.Delete("/events/{filename}", DeleteEventHandler)
```

Placed immediately after `GET /events/{filename}` in the site-scoped route
group, keeping the route definitions grouped by resource.

## Consequences

* **API symmetry** — the events resource now has the same CRUD surface as
  artifacts: `POST` (create), `GET` (read-one), `GET` (list), and now
  `DELETE` (remove).
* **Minimal scope** — no new packages, interfaces, or configuration.  The
  implementation is ~15 lines of handler code, a one-line router addition,
  and two tests.
* **Auth & audit unchanged** — the route sits inside the
  `SiteMiddleware` / `AuthMiddleware` / `AuditMiddleware` chain, so bearer
  token enforcement and audit-log recording apply automatically.
* **Tenant-scoped** — the `tenantKey(sc.TenantID, filename)` call ensures
  cross-tenant isolation is preserved.
