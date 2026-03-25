# ADR-035: Subscription Update Endpoint (PATCH)

## Status
Accepted

## Context

Segment 34 (ADR-034) introduced local CRUD management for webhook
subscriptions, but only exposed Create (`POST`), Read (`GET`), and Delete
(`DELETE`).  Updating a subscription required deleting it and recreating
it — which changes the ID and breaks any consumer that stored the old ID.

Common update needs include:

- Rotating a signing **secret** without recreating the subscription.
- Toggling **enabled** to pause delivery without losing subscription data.
- Updating the **URL** when a receiver endpoint is migrated.
- Adding or removing **event types** a subscription listens to.

## Decision

Add a partial-update (`PATCH`) endpoint and the corresponding `Update`
method on `StoreProvider`.

### `StoreProvider.Update`

```go
// internal/webhooks/store_provider.go
type SubscriptionPatch struct {
	URL      *string  // nil = keep existing
	Service  *string  // nil = keep existing
	Events   []string // nil = keep existing; non-nil replaces
	Secret   *string  // nil = keep existing
	TenantID *string  // nil = keep existing
	Enabled  *bool    // nil = keep existing
}

func (p *StoreProvider) Update(ctx context.Context, id string, patch SubscriptionPatch) (Subscription, error)
```

**Semantics:**

1. Load the existing subscription by `id` (wraps `storage.ErrNotFound` on
   miss).
2. For each non-nil pointer field in `patch`, overwrite the corresponding
   field in the existing subscription.
3. For `Events`: a non-nil slice (even `[]string{}`) replaces the existing
   events list; a nil slice leaves it unchanged.  This is the standard
   JSON merge-patch behaviour for slices.
4. `ID` and `CreatedAt` are **always preserved** — the record retains its
   identity and creation timestamp across updates.
5. `UpdatedAt` is always bumped to the current UTC time.
6. The record is written back to the same bbolt key, overwriting the
   previous bytes atomically (bbolt bucket `Put` is transactional).

### HTTP route

```
PATCH /sites/{site_id}/webhooks/subscriptions/{id}
```

| Field       | JSON type | Notes                                           |
|-------------|-----------|-------------------------------------------------|
| `url`       | string    | Optional; replaces `url` when present           |
| `service`   | string    | Optional; replaces `service` when present       |
| `events`    | array     | Optional; replaces entire events list           |
| `secret`    | string    | Optional; replaces `secret` when present        |
| `tenant_id` | string    | Optional; replaces `tenant_id` when present     |
| `enabled`   | bool      | Optional; toggles delivery without data loss    |

All fields are optional.  An empty JSON body `{}` is valid (returns 200
with no field changes other than `updated_at`).

**Responses:**

| Condition                        | Status |
|----------------------------------|--------|
| Success                          | 200 + updated subscription JSON |
| Non-JSON body                    | 400 Bad Request |
| Subscription not found           | 404 Not Found |
| Storage error                    | 500 Internal Server Error |

The 200 body is the full `Subscription` object (same shape as `POST` 201
and `GET` 200 responses) so callers always receive the canonical state
after the update.

### Why PATCH and not PUT?

A `PUT` endpoint would require the caller to supply all fields and would
reset any omitted fields to their zero values.  This makes secret rotation
unnecessarily risky (caller must know the current secret to re-send it)
and makes toggling `enabled` require knowledge of all other fields.
`PATCH` with pointer semantics gives fine-grained control with no
unintended side effects.

### `SubscriptionPatch` vs a generic `map[string]any` approach

A typed struct provides compile-time safety and avoids partial-update
ambiguity (is a missing field "no change" or "set to zero"?).  The
handler decodes into `subscriptionPatchInput` (same shape, HTTP-layer
type) and maps it to `SubscriptionPatch` before calling `StoreProvider`.

## Consequences

* **Full CRUD** — the subscription management API is now complete:
  `POST` (create), `GET` (read), `PATCH` (update), `DELETE` (remove),
  `GET` list (paginated).
* **No new dependencies** — the implementation uses only existing
  packages.
* **ID stability** — consumers can cache subscription IDs long-term; an
  update never changes the ID.
* **Audit trail** — `AuditMiddleware` records every `PATCH` request and
  emits an `"audit.write"` webhook event when the dispatcher is active.
* **Auth enforcement** — `AuthMiddleware` enforces the site API key on
  `PATCH` exactly as for all other mutating routes.
