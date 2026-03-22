// Package webhooks provides the core domain types, interfaces, and concrete
// implementations for outbound webhook notification delivery (ADR-012, Segment 12).
//
// # Subscription fetching
//
// RemoteProvider implements Provider by calling the PhaseMirror-HQ daemon API:
//
//	GET /api/webhook-subscriptions?service=<s>&event_type=<t>
//
// # Delivery
//
// WorkerDispatcher implements Dispatcher using an internal worker pool.
// Events are enqueued via Dispatch and delivered asynchronously with
// exponential back-off retries and optional HMAC-SHA256 signing
// (SignatureHeader).
//
// # Out of scope (future segments)
//
// Subscription caching/polling, middleware wiring, and handler integration
// are deferred to later segments.
package webhooks

import "time"

// Subscription represents a single webhook subscription as returned by the
// PhaseMirror-HQ daemon subscription API (v1).
//
// The subscription API endpoint is:
//
//	GET /api/webhook-subscriptions?service=<service>&event_type=<type>
type Subscription struct {
	// ID is the unique identifier assigned by PhaseMirror-HQ.
	ID string `json:"id"`

	// Service identifies the backend service this subscription targets
	// (e.g. "aiistech-backend").
	Service string `json:"service"`

	// URL is the HTTPS endpoint to which webhook payloads are POSTed.
	URL string `json:"url"`

	// Enabled controls whether the subscription receives deliveries.
	// Disabled subscriptions are returned by the API but must be skipped
	// by the dispatcher.
	Enabled bool `json:"enabled"`

	// Events is the list of event types this subscription receives
	// (e.g. ["audit.write"]).
	Events []string `json:"events"`

	// Secret is an optional HMAC-SHA256 signing secret. When non-empty the
	// dispatcher must include a signature header computed by SignatureHeader.
	Secret string `json:"secret,omitempty"`

	// TenantID is an optional tenant scoping field, reserved for Segment 14
	// multi-tenancy support.
	TenantID string `json:"tenant_id,omitempty"`

	// CreatedAt is the UTC timestamp when the subscription was created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is the UTC timestamp of the last modification.
	UpdatedAt time.Time `json:"updated_at"`
}

// Event is the outbound webhook payload envelope delivered to subscriber URLs.
type Event struct {
	// ID is a unique identifier for this event delivery (e.g. a UUID or
	// nanosecond-timestamped key matching the originating audit entry).
	ID string `json:"id"`

	// Type is the event type string (e.g. "audit.write").
	Type string `json:"type"`

	// CreatedAt is the UTC time at which the originating action occurred.
	CreatedAt time.Time `json:"created_at"`

	// Data carries the event-specific payload. The shape depends on Type;
	// for example, an "audit.write" event carries an audit.Entry value.
	// Callers should document the concrete type for each event type rather
	// than relying on runtime type assertions at the delivery site.
	Data any `json:"data"`
}
