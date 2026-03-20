package site

import "context"

type contextKey struct{}

// SiteContext holds the resolved site information attached to a request.
type SiteContext struct {
	SiteID string
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
