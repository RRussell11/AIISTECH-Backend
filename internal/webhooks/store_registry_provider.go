package webhooks

import (
	"context"
	"fmt"

	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
)

// siteIDKey is the unexported context key used to carry a site ID from the
// dispatcher's process loop into a StoreRegistryProvider.ListSubscriptions
// call.  Using a private struct type avoids key collisions with other packages.
type siteIDKey struct{}

// WithSiteID returns a copy of ctx carrying siteID as a value retrievable by
// siteIDFromContext.  The dispatcher calls this in process() so that the
// StoreRegistryProvider can route to the correct per-site store.
func WithSiteID(ctx context.Context, siteID string) context.Context {
	return context.WithValue(ctx, siteIDKey{}, siteID)
}

// siteIDFromContext returns the site ID stored by WithSiteID and reports
// whether a non-empty value was found.
func siteIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(siteIDKey{}).(string)
	return id, ok && id != ""
}

// StoreRegistryProvider is a Provider backed by a *storage.Registry.
//
// ListSubscriptions reads the site ID from the supplied context (injected by
// WithSiteID in worker_dispatcher.go), opens the matching per-site bbolt store
// via the registry, and delegates to a StoreProvider for that store.
//
// This provider is used when AIISTECH_WEBHOOK_STORE_PROVIDER=true so that
// subscriptions created via the subscription management API are live — the
// dispatcher consults them on every delivery cycle without any remote call to
// the PhaseMirror-HQ daemon (ADR-036).
//
// StoreRegistryProvider is safe for concurrent use.
type StoreRegistryProvider struct {
	stores *storage.Registry
}

// NewStoreRegistryProvider returns a StoreRegistryProvider backed by stores.
func NewStoreRegistryProvider(stores *storage.Registry) *StoreRegistryProvider {
	return &StoreRegistryProvider{stores: stores}
}

// ListSubscriptions implements Provider.
//
// It reads the site ID from ctx (set by WithSiteID), opens that site's store,
// and returns the subscriptions that match the service/eventType/tenantID
// filters.  An error is returned when no site ID is present in ctx or when the
// site's store cannot be opened.
func (p *StoreRegistryProvider) ListSubscriptions(ctx context.Context, service, eventType, tenantID string) ([]Subscription, error) {
	siteID, ok := siteIDFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("store_registry_provider: no site ID in context; ensure WithSiteID was called before ListSubscriptions")
	}

	st, err := p.stores.Open(siteID)
	if err != nil {
		return nil, fmt.Errorf("store_registry_provider: open store for site %q: %w", siteID, err)
	}

	return NewStoreProvider(st).ListSubscriptions(ctx, service, eventType, tenantID)
}

// compile-time check that *StoreRegistryProvider satisfies Provider.
var _ Provider = (*StoreRegistryProvider)(nil)
