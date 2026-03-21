package webhooks

import (
	"context"
	"time"
)

// Dispatcher is the interface for asynchronous webhook delivery.
//
// Implementations enqueue events for delivery to all matching, enabled
// subscriptions and return immediately. Callers must call Close when the
// dispatcher is no longer needed to drain in-flight deliveries and release
// resources.
type Dispatcher interface {
	// Dispatch enqueues evt for asynchronous delivery and returns as soon
	// as the event has been accepted into the internal queue. The supplied
	// context governs only this enqueue operation (e.g. it may be used to
	// respect a deadline while the queue is full); it does NOT control the
	// lifetime of the actual HTTP delivery attempts, which continue
	// independently after Dispatch returns.
	Dispatch(ctx context.Context, evt Event) error

	// Close signals the dispatcher to finish in-flight deliveries and shut
	// down its worker pool. It blocks until all workers have stopped.
	Close() error
}

// Config holds the configuration values needed to construct a Dispatcher
// implementation. All fields are intended to be populated from environment
// variables or the application config at startup.
type Config struct {
	// ServiceName is the logical name of this service as registered with
	// PhaseMirror-HQ (e.g. "aiistech-backend"). Used as the `service` query
	// parameter when fetching subscriptions.
	ServiceName string

	// SubscriptionsBaseURL is the base URL of the PhaseMirror-HQ daemon
	// subscription API (e.g. "https://phasemirror-hq.example.com").
	// The path /api/webhook-subscriptions is appended by the provider.
	SubscriptionsBaseURL string

	// SubscriptionsToken is the Bearer token used to authenticate requests
	// to the PhaseMirror-HQ subscription API.
	SubscriptionsToken string

	// TimeoutSeconds is the per-delivery HTTP request timeout in seconds.
	// A zero or negative value should be treated as a sensible default
	// (e.g. 5 seconds) by the implementation.
	TimeoutSeconds int

	// MaxAttempts is the maximum number of delivery attempts (including the
	// first) before a subscription delivery is abandoned.
	// A zero or negative value should be treated as a sensible default
	// (e.g. 5 attempts) by the implementation.
	MaxAttempts int

	// WorkerCount is the number of concurrent delivery goroutines.
	// A zero or negative value should be treated as a sensible default
	// (e.g. 4 workers) by the implementation.
	WorkerCount int

	// RetryBackoff is an optional function that returns the wait duration
	// before the next delivery attempt, given the 1-based index of the
	// attempt that just failed. If nil, the implementation uses the default
	// exponential back-off (1 s, 2 s, 4 s … capped at 30 s).
	// Override in tests to avoid real sleeps.
	RetryBackoff func(attempt int) time.Duration
}
