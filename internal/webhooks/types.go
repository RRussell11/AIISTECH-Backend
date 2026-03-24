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

// DLQSink receives failed webhook delivery records. Implementations must be
// safe for concurrent use from multiple goroutines.
//
// A nil DLQSink is a valid no-op value; callers must guard with a nil check
// before calling WriteDLQ (see WorkerDispatcher).
type DLQSink interface {
	// WriteDLQ persists record for later inspection or replay. Errors are
	// logged by the caller; they do not affect the delivery retry logic.
	WriteDLQ(record DLQRecord) error
}

// DLQRecord captures all information needed to inspect or replay a webhook
// delivery that exhausted all retry attempts (ADR-015, Segment 15).
type DLQRecord struct {
	// EventID is the ID of the originating webhooks.Event.
	EventID string `json:"event_id"`

	// EventType is the type string of the originating event (e.g. "audit.write").
	EventType string `json:"event_type"`

	// SiteID is the site that produced the event.
	SiteID string `json:"site_id,omitempty"`

	// TenantID is the tenant scope, sourced from the originating event.
	TenantID string `json:"tenant_id,omitempty"`

	// SubscriptionID is the ID of the subscription that could not be delivered.
	SubscriptionID string `json:"subscription_id"`

	// URL is the subscriber endpoint that rejected or was unreachable.
	URL string `json:"url"`

	// Payload is the JSON-serialised event body that was attempted.
	Payload []byte `json:"payload"`

	// AttemptCount is the total number of delivery attempts made.
	AttemptCount int `json:"attempt_count"`

	// LastError is the string representation of the final delivery error.
	LastError string `json:"last_error"`

	// FailedAt is the UTC time at which the last attempt was abandoned.
	FailedAt time.Time `json:"failed_at"`
}

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

	// TenantID is the optional tenant scope for this event, sourced from the
	// X-Tenant-ID request header. Empty means no tenant scope (default bucket).
	TenantID string `json:"tenant_id,omitempty"`

	// SiteID is the site that produced the event, sourced from SiteContext.SiteID
	// by AuditMiddleware. Used by StoreDLQSink to route failed deliveries to the
	// correct site store (ADR-015, Segment 15).
	SiteID string `json:"site_id,omitempty"`

	// CreatedAt is the UTC time at which the originating action occurred.
	CreatedAt time.Time `json:"created_at"`

	// Data carries the event-specific payload. The shape depends on Type;
	// for example, an "audit.write" event carries an audit.Entry value.
	// Callers should document the concrete type for each event type rather
	// than relying on runtime type assertions at the delivery site.
	Data any `json:"data"`
}
