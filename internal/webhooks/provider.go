package webhooks

import "context"

// Provider is the read-only interface for fetching webhook subscriptions from
// the PhaseMirror-HQ daemon API.
//
// Implementations must be safe for concurrent use.
type Provider interface {
	// ListSubscriptions returns all enabled-or-disabled subscriptions for the
	// given service that match the supplied eventType and tenantID filters.
	// An empty eventType or tenantID means "no filter on that field".
	ListSubscriptions(ctx context.Context, service string, eventType string, tenantID string) ([]Subscription, error)
}

// ListResponse is the JSON envelope returned by the PhaseMirror-HQ
// subscription list endpoint:
//
//	GET /api/webhook-subscriptions?service=<s>&event_type=<t>
//
// Response body:
//
//	{ "data": [ ...Subscription... ] }
type ListResponse struct {
	Data []Subscription `json:"data"`
}
