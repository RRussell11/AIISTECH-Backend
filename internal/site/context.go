package site

import (
	"context"

	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
)

type contextKey struct{}

// SiteContext holds the resolved site information attached to a request.
type SiteContext struct {
	SiteID string
	Store  storage.Store
	// APIKey is the bearer token required for mutating requests to this site.
	// Empty means authentication is disabled for the site.
	APIKey string
	// TenantID is an optional tenant scoping identifier for the request.
	// It is typically sourced from the X-Tenant-ID header by SiteMiddleware.
	// Empty means "no tenant scope" (the default bucket).
	TenantID string
}

// NewContext returns a new context with sc attached.
func NewContext(ctx context.Context, sc SiteContext) context.Context {
	return context.WithValue(ctx, contextKey{}, sc)
}

// FromContext retrieves the SiteContext from ctx.
// ok is false if no SiteContext has been attached.
func FromContext(ctx context.Context) (SiteContext, bool) {
	sc, ok := ctx.Value(contextKey{}).(SiteContext)
	return sc, ok
}