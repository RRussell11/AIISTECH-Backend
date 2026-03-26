# ADR-039 — Subscription Management HTTP API

**Status:** Accepted  
**Date:** 2026-03-26  
**Segment:** 39

---

## Context

The `StoreProvider` (Segment 37) persists webhook subscriptions in a local
bbolt database and satisfies the `Provider` interface consumed by the dispatcher.
However, there was no HTTP surface for operators or applications to create,
inspect, modify, or remove subscriptions at runtime. Operators were forced to
manipulate the bbolt database directly or restart the server to change
subscription configuration.

---

## Decision

### 1. `SubscriptionPatch` + `StoreProvider.Update`

A `SubscriptionPatch` struct is added to `store_provider.go` to express partial
updates:

| Field | Pointer type | Nil means |
|---|---|---|
| `URL` | `*string` | keep |
| `Enabled` | `*bool` | keep |
| `Events` | `[]string` | keep (via `eventsPresent` flag) |
| `Secret` | `*string` | keep |
| `TenantID` | `*string` | keep |

`Events` requires special handling: an absent `"events"` key in JSON must not
replace the existing list, while an explicit `"events": []` (empty array) must
clear the list. This is implemented with a custom `UnmarshalJSON` that sets an
unexported `eventsPresent` flag. A `SetEvents([]string)` helper allows the same
semantics to be used from Go code without JSON round-tripping.

`StoreProvider.Update(id, patch)` reads the current subscription, applies the
patch, sets `UpdatedAt`, and re-saves. It returns the updated `Subscription` so
callers get the full post-update state in a single call.

### 2. `StoreProvider.Create` signature change

`Create` is changed from a value receiver (`sub Subscription`) to a pointer
receiver (`sub *Subscription`) so that the auto-generated `ID` (and the
initialised `CreatedAt`/`UpdatedAt` timestamps) are visible to the caller after
`Create` returns. This is necessary for the HTTP handler to return the persisted
subscription (including its server-assigned ID) in the `201 Created` response.

### 3. `StoreProvider.ListPage`

A `ListPage(cursor, limit)` method is added to `StoreProvider` to support
cursor-based pagination of the subscription list. It delegates to
`storage.Store.ListPage` (which was already implemented) and decodes the
resulting JSON blobs.

### 4. HTTP endpoints (`internal/http/subscription_handlers.go`)

Mounted at `/webhooks/subscriptions` when a `*StoreProvider` is passed to
`NewRouter` (nil = routes not mounted).

| Method | Path | Description |
|---|---|---|
| `GET` | `/webhooks/subscriptions/` | List subscriptions (paginated; `?cursor=`, `?limit=`) |
| `POST` | `/webhooks/subscriptions/` | Create subscription; returns `201 Created` |
| `GET` | `/webhooks/subscriptions/{id}` | Get a single subscription |
| `PATCH` | `/webhooks/subscriptions/{id}` | Partial update; returns `200` with full updated record |
| `DELETE` | `/webhooks/subscriptions/{id}` | Delete subscription; returns `204 No Content` |

**POST request body:**

```json
{
  "service": "aiistech-backend",
  "url": "https://example.com/hook",
  "enabled": true,
  "events": ["audit.write"],
  "secret": "optional-hmac-secret",
  "tenant_id": "optional-tenant"
}
```

`service` and `url` are required. `enabled` defaults to `true` when absent.
`events` defaults to `null` (catch-all).

**PATCH request body** (all fields optional):

```json
{
  "url": "https://new.example.com/hook",
  "enabled": false,
  "events": ["audit.write", "artifact.write"],
  "secret": "new-secret"
}
```

### 5. Router change

`NewRouter` gains a `storeProvider *webhooks.StoreProvider` parameter at the
end. When nil, the `/webhooks/subscriptions` route group is not mounted.
This keeps the DLQ routes and subscription routes independently optional.

### 6. Server wiring

`main.go` saves the `*StoreProvider` to a variable and passes it to `NewRouter`
alongside the existing `dlqStore` and `dlqReplayer`.

---

## Consequences

* Operators can fully manage webhook subscriptions at runtime via standard HTTP
  calls without restarting the server or touching the database directly.
* Subscriptions are visible and modifiable via the same API surface used to
  configure the delivery pipeline.
* The PATCH semantics correctly handle the `events` list: absent = keep,
  present = replace (including with an empty slice for catch-all).
* The `Create` pointer-receiver change is a minor breaking API change for any
  callers that passed a value — all internal callers were updated.
* Routes are conditionally mounted (nil-safe), so deployments using only
  `RemoteProvider` are unaffected.
