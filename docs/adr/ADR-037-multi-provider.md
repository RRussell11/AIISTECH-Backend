# ADR-037 — MultiProvider, Event-Type Filtering & StoreProvider

**Status:** Accepted  
**Date:** 2026-03-26  
**Segment:** 37 (MultiProvider) / 37-38 (Event-type filtering)

---

## Context

The webhook pipeline previously supported exactly one subscription source at a
time, chosen at startup via env vars:

- `AIISTECH_WEBHOOK_BASE_URL` set → use `RemoteProvider` (PhaseMirror-HQ API).
- Neither set → webhooks disabled.

Two gaps existed:

1. **Single-source limitation.** Operators could not combine locally-stored
   subscriptions (useful for air-gapped or self-managed deployments) with
   remotely-managed ones from PhaseMirror-HQ.
2. **Missing `"*"` wildcard.** The `Events` field on a `Subscription` already
   supported an empty slice as a catch-all, but an explicit `"*"` entry was not
   recognised.

---

## Decision

### 1. StoreProvider (`internal/webhooks/store_provider.go`)

A new `Provider` implementation backed by a local bbolt `Store`.  Subscriptions
are persisted as JSON values in the `"webhook_subscriptions"` bucket, keyed by
`Subscription.ID`.

`StoreProvider` exposes three management helpers beyond the `Provider` interface:
`Create`, `Get`, and `Delete`.  These allow programmatic seeding and future HTTP
management endpoints.

`ListSubscriptions` applies `service` and `tenantID` filters in-process; the
`eventType` parameter is intentionally **not** applied at the store layer so
that the shared `matchesEventType` logic in the dispatcher handles it uniformly
for all providers.

Enabled when:
- `AIISTECH_WEBHOOK_STORE_PROVIDER=true`
- or as part of a `MultiProvider` when both flags are set.

Database path:
- Configured via `AIISTECH_WEBHOOK_SUBSCRIPTIONS_DB`.
- Default: `var/state/webhooks/subscriptions.db`.

### 2. MultiProvider (`internal/webhooks/multi_provider.go`)

A `Provider` that composes an arbitrary list of child providers and returns a
deduplicated union of all their subscriptions.

**Deduplication key** (deterministic, applied in provider order):
1. `"id:" + Subscription.ID` when the ID field is non-empty.
2. `"url:" + Subscription.URL + "|tenant:" + Subscription.TenantID` otherwise.

The **first** occurrence wins (earlier providers in the list take precedence).

**Fault tolerance:** If a child provider returns an error it is logged at WARN
level and skipped.  The remaining providers are still queried.  `MultiProvider`
itself never returns a non-nil error; it returns an empty slice when all
providers fail.

### 3. Server wiring (`cmd/server/main.go`)

Provider selection at startup:

| `AIISTECH_WEBHOOK_BASE_URL` | `AIISTECH_WEBHOOK_STORE_PROVIDER` | Provider used         |
|-----------------------------|-----------------------------------|-----------------------|
| set                         | `true`                            | `MultiProvider`       |
| set                         | unset / `false`                   | `RemoteProvider`      |
| unset                       | `true`                            | `StoreProvider`       |
| unset                       | unset / `false`                   | *(webhooks disabled)* |

**Backward compatibility:** Existing deployments that only set
`AIISTECH_WEBHOOK_BASE_URL` continue to use `RemoteProvider` exactly as before.

### 4. Event-type filtering wildcard (`internal/webhooks/worker_dispatcher.go`)

`matchesEventType` now recognises `"*"` as an explicit wildcard element in
addition to the existing empty-slice behaviour.  Comparisons are
**case-sensitive**; callers are responsible for normalising event type strings.

Rules (evaluated in order):
1. `len(sub.Events) == 0` → matches all.
2. Any element `== "*"` → matches all.
3. Any element `== eventType` → matches.
4. Otherwise → no match.

---

## New Environment Variables

| Variable | Default | Description |
|---|---|---|
| `AIISTECH_WEBHOOK_STORE_PROVIDER` | *(unset)* | Set to `true` to enable the local bbolt `StoreProvider` |
| `AIISTECH_WEBHOOK_SUBSCRIPTIONS_DB` | `var/state/webhooks/subscriptions.db` | Path to the bbolt database for local subscriptions |

---

## Consequences

- Operators can now run in **fully local mode** (no PhaseMirror-HQ dependency)
  by setting only `AIISTECH_WEBHOOK_STORE_PROVIDER=true`.
- Hybrid deployments can enable both providers and receive webhooks from all
  active subscriptions regardless of origin.
- If the remote provider is temporarily unavailable, local subscriptions
  continue to be served (and vice versa).
- The `"*"` wildcard element provides a portable way for subscription authors to
  receive every event type without knowing the full list ahead of time.
