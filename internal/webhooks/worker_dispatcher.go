package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"expvar"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Defaults applied when the corresponding Config field is zero.
const (
	defaultTimeoutSeconds = 5
	defaultMaxAttempts    = 5
	defaultWorkerCount    = 4

	// subscriptionFetchTimeoutMultiplier scales the per-delivery timeout up for
	// the subscription-listing call, which must complete before any delivery
	// can begin and therefore benefits from a slightly longer budget. The listing
	// is scoped by service name, event type, and tenant ID.
	subscriptionFetchTimeoutMultiplier = 2
)

var (
	// metricsWebhookDeliveriesTotal counts successful webhook deliveries.
	metricsWebhookDeliveriesTotal = expvar.NewInt("webhook_deliveries_total")
	// metricsWebhookDeliveryFailuresTotal counts delivery attempts abandoned after
	// all retries are exhausted (one increment per subscription that could not
	// be reached, regardless of whether the event went to the DLQ).
	metricsWebhookDeliveryFailuresTotal = expvar.NewInt("webhook_delivery_failures_total")
	// metricsWebhookDLQStoredTotal counts records successfully stored in the DLQ.
	metricsWebhookDLQStoredTotal = expvar.NewInt("webhook_dlq_stored_total")
)

// WorkerDispatcher is the concrete Dispatcher implementation. It maintains an
// internal job queue and a pool of worker goroutines that POST webhook events
// to subscriber URLs with automatic retries and exponential back-off.
//
// Create instances with NewWorkerDispatcher. The zero value is not usable.
type WorkerDispatcher struct {
	cfg      Config
	provider Provider
	jobs     chan dispatchJob
	wg       sync.WaitGroup
	client   *http.Client
	breakers *breakerRegistry // nil when circuit breaking is disabled
}

// dispatchJob is an internal unit of work placed on the queue by Dispatch.
type dispatchJob struct {
	evt Event
}

// NewWorkerDispatcher creates a WorkerDispatcher and starts its worker pool.
// Workers begin consuming the job queue immediately.
//
// cfg.RetryBackoff may be nil (uses default exponential back-off).
// Other zero-value fields fall back to package defaults.
func NewWorkerDispatcher(cfg Config, provider Provider) *WorkerDispatcher {
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = defaultTimeoutSeconds
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultMaxAttempts
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = defaultWorkerCount
	}
	if cfg.RetryBackoff == nil {
		cfg.RetryBackoff = exponentialBackoff
	}

	var breakers *breakerRegistry
	if cfg.CircuitBreaker != nil {
		breakers = newBreakerRegistry(*cfg.CircuitBreaker)
	}

	d := &WorkerDispatcher{
		cfg:      cfg,
		provider: provider,
		// Buffer 4× the worker count before Dispatch starts blocking callers.
		jobs:     make(chan dispatchJob, cfg.WorkerCount*4),
		client:   &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second},
		breakers: breakers,
	}
	for range cfg.WorkerCount {
		d.wg.Add(1)
		go d.worker()
	}
	return d
}

// Dispatch enqueues evt for asynchronous delivery and returns once the event
// has been accepted into the internal queue, or when ctx is cancelled.
func (d *WorkerDispatcher) Dispatch(ctx context.Context, evt Event) error {
	select {
	case d.jobs <- dispatchJob{evt: evt}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close shuts down the dispatcher: it closes the job queue and blocks until
// all in-flight deliveries have finished.
func (d *WorkerDispatcher) Close() error {
	close(d.jobs)
	d.wg.Wait()
	return nil
}

// worker runs in a goroutine and processes jobs until the queue is closed.
func (d *WorkerDispatcher) worker() {
	defer d.wg.Done()
	for job := range d.jobs {
		d.process(job.evt)
	}
}

// process fetches subscriptions for evt and delivers to each matching enabled one.
func (d *WorkerDispatcher) process(evt Event) {
	// Give subscription fetching its own timeout separate from per-delivery timeout.
	ctx, cancel := context.WithTimeout(
		context.Background(),
		time.Duration(d.cfg.TimeoutSeconds*subscriptionFetchTimeoutMultiplier)*time.Second,
	)
	defer cancel()

	// Thread the originating site ID so that store-backed providers can route
	// to the correct per-site bbolt store (StoreRegistryProvider, ADR-036).
	if evt.SiteID != "" {
		ctx = WithSiteID(ctx, evt.SiteID)
	}

	slog.Debug("webhooks: fetching subscriptions",
		"event_id", evt.ID,
		"event_type", evt.Type,
		"site_id", evt.SiteID,
	)
	subs, err := d.provider.ListSubscriptions(ctx, d.cfg.ServiceName, evt.Type, evt.TenantID)
	if err != nil {
		slog.Error("webhooks: failed to list subscriptions",
			"event_id", evt.ID,
			"event_type", evt.Type,
			"error", err,
		)
		return
	}

	bodyBytes, err := json.Marshal(evt)
	if err != nil {
		slog.Error("webhooks: failed to marshal event", "event_id", evt.ID, "error", err)
		return
	}

	for _, sub := range subs {
		if !sub.Enabled {
			continue
		}
		if !matchesEventType(sub, evt.Type) {
			continue
		}
		d.deliverWithRetry(sub, evt, bodyBytes)
	}
}

// matchesEventType reports whether sub has an empty Events slice (wildcard) or
// explicitly lists eventType.
func matchesEventType(sub Subscription, eventType string) bool {
	if len(sub.Events) == 0 {
		return true
	}
	for _, e := range sub.Events {
		if e == eventType {
			return true
		}
	}
	return false
}

// deliverWithRetry attempts to POST bodyBytes to sub.URL up to MaxAttempts
// times, sleeping cfg.RetryBackoff(attempt) between failures.
// When circuit breaking is enabled (cfg.CircuitBreaker != nil) and the
// breaker for sub is Open, the delivery is fast-failed without any HTTP
// attempts.
// If all attempts are exhausted (or the circuit is open) and cfg.DLQ is set,
// a DLQRecord is stored.
func (d *WorkerDispatcher) deliverWithRetry(sub Subscription, evt Event, bodyBytes []byte) {
	// --- Circuit breaker check ---
	if d.breakers != nil {
		cb := d.breakers.get(sub.ID)
		if !cb.Allow() {
			slog.Warn("webhooks: circuit open, skipping delivery",
				"subscription_id", sub.ID,
				"event_id", evt.ID,
			)
			metricsWebhookDeliveryFailuresTotal.Add(1)
			d.storeDLQ(sub, evt, bodyBytes, 0)
			return
		}
	}

	// --- Retry loop ---
	var lastErr error
	for attempt := 1; attempt <= d.cfg.MaxAttempts; attempt++ {
		if err := d.deliverOnce(sub, bodyBytes); err == nil {
			slog.Info("webhooks: delivered",
				"subscription_id", sub.ID,
				"event_id", evt.ID,
				"attempt", attempt,
			)
			metricsWebhookDeliveriesTotal.Add(1)
			if d.breakers != nil {
				d.breakers.get(sub.ID).RecordSuccess()
			}
			return
		} else {
			lastErr = err
		}

		if attempt < d.cfg.MaxAttempts {
			backoff := d.cfg.RetryBackoff(attempt)
			slog.Warn("webhooks: delivery failed, retrying",
				"subscription_id", sub.ID,
				"event_id", evt.ID,
				"attempt", attempt,
				"backoff", backoff,
				"error", lastErr,
			)
			time.Sleep(backoff)
		}
	}

	// --- All retries exhausted ---
	slog.Error("webhooks: delivery abandoned after max attempts",
		"subscription_id", sub.ID,
		"event_id", evt.ID,
		"max_attempts", d.cfg.MaxAttempts,
		"error", lastErr,
	)
	metricsWebhookDeliveryFailuresTotal.Add(1)
	if d.breakers != nil {
		d.breakers.get(sub.ID).RecordFailure()
	}
	d.storeDLQ(sub, evt, bodyBytes, d.cfg.MaxAttempts)
}

// storeDLQ writes a DLQRecord for sub/evt when a DLQ sink is configured.
// attempts is the number of delivery attempts made (0 = circuit-open fast-fail).
func (d *WorkerDispatcher) storeDLQ(sub Subscription, evt Event, bodyBytes []byte, attempts int) {
	if d.cfg.DLQ == nil {
		return
	}
	record := DLQRecord{
		SubscriptionID:  sub.ID,
		SubscriptionURL: sub.URL,
		Secret:          sub.Secret,
		EventID:         evt.ID,
		EventType:       evt.Type,
		SiteID:          evt.SiteID,
		TenantID:        evt.TenantID,
		Payload:         bodyBytes,
		Attempts:        attempts,
		FailedAt:        time.Now().UTC(),
	}
	if err := d.cfg.DLQ.Store(record); err != nil {
		slog.Error("webhooks: failed to store DLQ record",
			"subscription_id", sub.ID,
			"event_id", evt.ID,
			"error", err,
		)
	} else {
		metricsWebhookDLQStoredTotal.Add(1)
	}
}

// deliverOnce performs a single HTTP POST of bodyBytes to sub.URL.
// When sub.Secret is non-empty it adds X-Webhook-Signature and
// X-Webhook-Timestamp headers (ADR-012 signing scheme).
func (d *WorkerDispatcher) deliverOnce(sub Subscription, bodyBytes []byte) error {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	req, err := http.NewRequest(http.MethodPost, sub.URL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("building delivery request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Timestamp", timestamp)
	if sub.Secret != "" {
		req.Header.Set("X-Webhook-Signature", SignatureHeader(sub.Secret, timestamp, bodyBytes))
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP POST to %s: %w", sub.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("receiver at %s returned status %d", sub.URL, resp.StatusCode)
	}
	return nil
}

// exponentialBackoff returns the back-off duration for the given 1-based failed
// attempt number: 1 s, 2 s, 4 s, 8 s … capped at 30 s.
func exponentialBackoff(attempt int) time.Duration {
	const (
		base    = time.Second
		maxWait = 30 * time.Second
	)
	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
		if d > maxWait {
			return maxWait
		}
	}
	return d
}
