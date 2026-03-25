# ADR-030: SIGHUP Site-Registry Hot-Reload

## Status
Accepted

## Context

When a new site needs to be added to (or removed from) the platform, the
operator must update `contracts/shared/sites.yaml` (or the file pointed to by
`AIISTECH_REGISTRY_PATH`).  Prior to this change the only way to make the
server recognise the new registry was to restart the process.  A restart:

* drains the webhook dispatcher queue (deliveries in-flight are dropped or
  re-queued to the DLQ depending on timing),
* closes all open bbolt store file descriptors (brief interruption),
* briefly takes the server offline.

All three consequences are undesirable in a production environment where
downtime must be minimised and webhook delivery guarantees must be honoured.

The Go standard library provides `signal.Notify` for receiving OS signals;
`sync/atomic.Pointer` (Go ≥ 1.19) provides a lock-free, safe pointer swap.
Combining these two primitives allows the registry to be replaced atomically
while the server continues serving requests.

## Decision

### AtomicRegistry

A new type `site.AtomicRegistry` is added to `internal/site/registry.go`:

```go
type AtomicRegistry struct {
    p atomic.Pointer[Registry]
}
func NewAtomicRegistry(r *Registry) *AtomicRegistry
func (ar *AtomicRegistry) Load() *Registry
func (ar *AtomicRegistry) Store(r *Registry)
func (ar *AtomicRegistry) Contains(siteID string) bool
func (ar *AtomicRegistry) SiteIDs() []string
func (ar *AtomicRegistry) DefaultSiteID() string
```

All middleware and handler functions that previously accepted `*Registry` now
accept `*AtomicRegistry`.  Inside `SiteMiddleware` the current registry is
snapshotted once per request via `reg.Load()` and passed to `site.Resolve`;
in-flight requests always complete against a consistent snapshot even if a
concurrent swap occurs.

`site.Resolve` continues to accept `*Registry` (the raw snapshot), keeping its
unit tests unchanged.

### SIGHUP handler in main.go

```
signal.Notify(sighupCh, syscall.SIGHUP)
go func() {
    for range sighupCh {
        newReg, err := site.LoadRegistry(registryPath)
        if err != nil {
            slog.Error("hot-reload failed: keeping existing registry", ...)
            continue
        }
        atomicReg.Store(newReg)
        slog.Info("site registry hot-reloaded", ...)
    }
}()
```

On a failed reload (e.g. the file is temporarily malformed) the existing
registry is kept; the error is logged and the goroutine continues listening for
further SIGHUP signals.

### Per-site config is already hot-reloaded

`config.Load(siteID, path)` is called on every request inside `SiteMiddleware`,
so individual `contracts/sites/<id>/config.yaml` files are already re-read
per-request.  No additional mechanism is needed for per-site config.

### Operator workflow

```sh
# 1. Edit contracts/shared/sites.yaml (add / remove a site)
# 2. Send SIGHUP to the running process
kill -HUP $(pgrep aiistech-backend)
# The next request already sees the new registry.
```

## Consequences

* **No downtime** for site-registry updates — the process keeps accepting
  requests throughout the reload.
* **No queue drain** — the webhook dispatcher is not touched.
* **Backward-compatible** — the `*Registry` type and its methods are unchanged;
  only the function signatures that cross package boundaries have been updated
  to accept `*AtomicRegistry`.
* **Failure-safe** — a malformed sites.yaml during hot-reload is rejected and
  the running server continues serving the previous registry.
* **No new external dependencies** — only `sync/atomic` from the standard
  library is used.
* `SIGHUP` is not available on Windows, but the codebase targets Linux
  containers (see Dockerfile) so this is not a practical limitation.
