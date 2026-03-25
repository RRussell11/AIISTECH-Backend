# ADR-033: Cursor-Based Pagination for List Endpoints

## Status
Accepted

## Context

The three list endpoints — `GET /events`, `GET /artifacts`, and `GET /audit` —
historically returned every key from the underlying bbolt bucket in a single
response.  As bucket sizes grow this approach:

- Allocates memory proportional to the total number of stored keys, regardless
  of how many the client needs.
- Produces responses whose size is unbounded and unpredictable.
- Gives clients no mechanism to stream or page through results incrementally.

Separately, the events and artifacts endpoints were already enriched with
server-side filtering (`?since_ns=`, `?until_ns=`, `?prefix=`, `?contains=`)
which further motivates a pagination contract so clients can page through
large filtered result sets.

## Decision

Introduce uniform **cursor-based pagination** across all three list endpoints
using two optional query parameters: `?cursor=` and `?limit=`.

### Query parameters

| Parameter | Type    | Default | Max | Semantics |
|-----------|---------|---------|-----|-----------|
| `cursor`  | string  | `""`    | —   | Opaque key of the last item seen on the previous page.  Omit or pass `""` to start from the beginning. |
| `limit`   | integer | 50      | 200 | Maximum number of items to return.  Values above 200 are silently clamped.  Non-integer or non-positive values return **400 Bad Request**. |

Constants in `handlers.go`:

```go
const (
    defaultPageLimit = 50
    maxPageLimit     = 200
)
```

### Response shape

Every list handler returns a `next_cursor` string field alongside its
resource array.  An empty string means no further pages exist; a non-empty
string must be forwarded as `?cursor=` in the next request.

```json
{
  "site_id": "…",
  "events": ["…", "…"],
  "next_cursor": "1712345678901234567.json"
}
```

### Helper: `parsePaginationParams`

```go
// internal/http/handlers.go
func parsePaginationParams(w http.ResponseWriter, r *http.Request) (cursor string, limit int, ok bool)
```

Reads `?cursor=` and `?limit=`, applies the default and clamp, and writes a
400 error (returning `ok=false`) for bad limit values.

### Helper: `listFilteredPage`

`GET /events`, `GET /artifacts`, and `GET /audit` support in-memory key
filters in addition to pagination.  Because filters must be applied before the
cursor offset is calculated, a two-phase helper is used:

1. `store.List(bucket)` — retrieve all keys from the bucket in ascending
   byte order (this is the same as chronological order for nanosecond-timestamp
   keys).
2. Walk the full key list, skipping keys that do not pass the active filters and
   keys that fall on or before the cursor position.
3. Collect up to `limit` matching keys.
4. Compute `nextCursor` as the last collected key.  If no further filtered key
   exists beyond that point, `nextCursor` is set to `""`.

```go
// internal/http/handlers.go
func listFilteredPage(store storage.Store, bucket, cursor string, limit int, f listFilter) (keys []string, nextCursor string, err error)
```

### Storage-level `ListPage`

For endpoints that do **not** require in-memory filtering (currently
`GET /webhooks/dlq`), the more efficient storage-layer method is used instead:

```go
// internal/storage/store.go
ListPage(bucket, cursor string, limit int) (keys []string, nextCursor string, err error)
```

The bbolt implementation seeks the cursor key and reads at most `limit`
subsequent keys in a single read transaction, avoiding a full bucket scan.

### Filter query parameters

| Parameter   | Type   | Semantics |
|-------------|--------|-----------|
| `since_ns`  | int64  | Include only keys whose embedded nanosecond timestamp ≥ `since_ns`. |
| `until_ns`  | int64  | Include only keys whose embedded nanosecond timestamp ≤ `until_ns`. |
| `prefix`    | string | Include only keys whose client-visible name starts with `prefix`. |
| `contains`  | string | Include only keys whose client-visible name contains the substring. |

Filters are validated by `parseFilterParams`; invalid values return 400.
`since_ns > until_ns` is also rejected with 400.

### Tenant awareness

When a request carries a non-empty `TenantID`:

- `applyTenantFilter` prepends `"{tenantID}/"` to the cursor (so the
  storage-level seek lands correctly) and to the `Prefix` filter (to restrict
  results to the current tenant's keys).
- `stripTenantPrefix` removes `"{tenantID}/"` from every returned key and
  from `nextCursor` before the response is encoded, so clients always see bare
  filenames with no tenant namespace.

```go
func applyTenantFilter(tenantID, cursor string, f listFilter) (adjustedCursor, tenantPrefix string, adjustedFilter listFilter)
func stripTenantPrefix(tenantPrefix string, keys []string, nextCursor string) ([]string, string)
```

## Consequences

- **Bounded responses** — handler allocations are proportional to `limit`
  (max 200) rather than total bucket size.
- **O(n) scan for filtered endpoints** — `listFilteredPage` still calls
  `store.List` and walks all keys to apply filters in memory.  The per-request
  work therefore scales with total bucket size even when the page is small.
  For future optimisation, filtered pagination could be pushed into the storage
  layer once per-bucket secondary indexes are introduced; this is deferred
  until benchmarks justify the complexity.
- **Stable cursor semantics** — the cursor is an opaque string key.  Deleting
  the key that was used as a cursor does not break subsequent pages: bbolt
  seeks to the first key lexicographically greater than the cursor value,
  skipping the (now absent) key transparently.
- **Uniform API surface** — all three list endpoints expose identical
  `?cursor=` / `?limit=` parameters and identical `next_cursor` response
  fields, simplifying client code.
- **No breaking changes** — both query parameters are optional with sensible
  defaults, so existing callers that omit them continue to receive a single
  page of up to 50 results.
