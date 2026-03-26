package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
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
	// can begin and therefore benefits from a slightly longer budget.
	subscriptionFetchTimeoutMultiplier = 2

	defaultDLQCoolingOff   = 5 * time.Minute
	defaultDLQScanInterval = 60 * time.Second
	defaultDLQMaxAttempts  = 10
)

// WorkerDispatcher is the concrete Dispatcher implementation. It maintains an
// internal job queue and a pool of worker goroutines that POST webhook events
// to subscriber URLs with automatic retries and exponential back-off.
//
// Create instances with NewWorkerDispatcher. The zero value is not usable.
type WorkerDispatcher struct {
	cfg           Config
	provider      Provider
	jobs          chan dispatchJob
	wg            sync.WaitGroup
	client        *http.Client
	dlq           *DLQStore
	stopAutoRetry chan struct{}
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
	if cfg.DLQCoolingOff <= 0 {
		cfg.DLQCoolingOff = defaultDLQCoolingOff
	}
	if cfg.DLQScanInterval <= 0 {
		cfg.DLQScanInterval = defaultDLQScanInterval
	}
	if cfg.DLQMaxAttempts <= 0 {
		cfg.DLQMaxAttempts = defaultDLQMaxAttempts
	}

	d := &WorkerDispatcher{
		cfg:      cfg,
		provider: provider,
		// Buffer 4× the worker count before Dispatch starts blocking callers.
		jobs:   make(chan dispatchJob, cfg.WorkerCount*4),
		client: &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second},
		dlq:    cfg.DLQStore,
	}
	for range cfg.WorkerCount {
		d.wg.Add(1)
		go d.worker()
	}

	// Start the auto-retry scheduler only when a DLQ store is configured.
	if d.dlq != nil {
		d.stopAutoRetry = make(chan struct{})
		go d.autoRetry()
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
// all in-flight deliveries have finished. It also stops the auto-retry
// scheduler if one is running.
func (d *WorkerDispatcher) Close() error {
	if d.stopAutoRetry != nil {
		close(d.stopAutoRetry)
	}
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

	subs, err := d.provider.ListSubscriptions(ctx, d.cfg.ServiceName, evt.Type, "")
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

// matchesEventType reports whether sub should receive an event of eventType.
//
// Rules (evaluated in order):
//  1. Empty Events slice — subscribe to all event types (catch-all).
//  2. Any element equals "*" — explicit wildcard; subscribes to all types.
//  3. Any element equals eventType (case-sensitive) — explicit match.
//
// Comparisons are case-sensitive; callers are responsible for normalising
// event type strings before calling Dispatch.
func matchesEventType(sub Subscription, eventType string) bool {
	if len(sub.Events) == 0 {
		return true
	}
	for _, e := range sub.Events {
		if e == "*" || e == eventType {
			return true
		}
	}
	return false
}

// deliverWithRetry attempts to POST bodyBytes to sub.URL up to MaxAttempts
// times, sleeping cfg.RetryBackoff(attempt) between failures.
// On exhaustion it stores a DLQ record when a DLQStore is configured.
func (d *WorkerDispatcher) deliverWithRetry(sub Subscription, evt Event, bodyBytes []byte) {
	var lastErr error
	for attempt := 1; attempt <= d.cfg.MaxAttempts; attempt++ {
		if err := d.deliverOnce(sub, bodyBytes); err == nil {
			slog.Info("webhooks: delivered",
				"subscription_id", sub.ID,
				"event_id", evt.ID,
				"attempt", attempt,
			)
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

	slog.Error("webhooks: delivery abandoned after max attempts",
		"subscription_id", sub.ID,
		"event_id", evt.ID,
		"max_attempts", d.cfg.MaxAttempts,
		"error", lastErr,
	)

	if d.dlq != nil {
		d.saveToDLQ(sub, evt, lastErr)
	}
}

// saveToDLQ writes a new DLQRecord for the failed delivery.
func (d *WorkerDispatcher) saveToDLQ(sub Subscription, evt Event, deliveryErr error) {
	now := time.Now().UTC()
	rec := &DLQRecord{
		SubscriptionID:     sub.ID,
		SubscriptionURL:    sub.URL,
		SubscriptionSecret: sub.Secret,
		Event:              evt,
		Attempts:           0,
		LastError:          deliveryErr.Error(),
		FailedAt:           now,
		NextRetryAfter:     now.Add(d.cfg.DLQCoolingOff),
	}
	if err := d.dlq.Save(rec); err != nil {
		slog.Error("webhooks: failed to save DLQ record",
			"event_id", evt.ID,
			"subscription_id", sub.ID,
			"error", err,
		)
		return
	}
	metricDLQStoredTotal.Add(1)
	slog.Warn("webhooks: delivery stored in DLQ",
		"dlq_id", rec.ID,
		"event_id", evt.ID,
		"subscription_id", sub.ID,
		"next_retry_after", rec.NextRetryAfter,
	)
}

// ReplayRecord performs a single delivery attempt for the event in rec to the
// subscription recorded in rec. It does NOT update the DLQ store — callers are
// responsible for updating or deleting the record based on the returned error.
func (d *WorkerDispatcher) ReplayRecord(rec DLQRecord) error {
	bodyBytes, err := json.Marshal(rec.Event)
	if err != nil {
		return fmt.Errorf("webhooks: replay: marshalling event: %w", err)
	}
	sub := Subscription{
		ID:     rec.SubscriptionID,
		URL:    rec.SubscriptionURL,
		Secret: rec.SubscriptionSecret,
	}
	return d.deliverOnce(sub, bodyBytes)
}

// autoRetry is a background goroutine that periodically scans the DLQ for
// records whose NextRetryAfter has passed and replays them.
func (d *WorkerDispatcher) autoRetry() {
	ticker := time.NewTicker(d.cfg.DLQScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-d.stopAutoRetry:
			return
		case <-ticker.C:
			d.runAutoRetryPass()
		}
	}
}

// runAutoRetryPass is a single auto-retry scan. It is broken out to make it
// testable independently of the ticker loop.
func (d *WorkerDispatcher) runAutoRetryPass() {
	now := time.Now().UTC()

	records, err := d.dlq.List()
	if err != nil {
		slog.Error("webhooks: auto-retry: failed to list DLQ records", "error", err)
		return
	}

	// Use a semaphore to limit concurrency during auto-retry.
	sem := make(chan struct{}, d.cfg.WorkerCount)
	var wg sync.WaitGroup

	for _, rec := range records {
		if rec.IsTerminal(d.cfg.DLQMaxAttempts) {
			continue
		}
		if rec.NextRetryAfter.After(now) {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(r DLQRecord) {
			defer wg.Done()
			defer func() { <-sem }()
			d.replayAndUpdate(r)
		}(rec)
	}

	wg.Wait()
}

// replayAndUpdate attempts to replay rec and updates the DLQ store with the result.
func (d *WorkerDispatcher) replayAndUpdate(rec DLQRecord) {
	err := d.ReplayRecord(rec)
	rec.Attempts++
	rec.FailedAt = time.Now().UTC()

	if err == nil {
		metricDLQReplaySuccessTotal.Add(1)
		slog.Info("webhooks: auto-retry: delivery succeeded, removing DLQ record",
			"dlq_id", rec.ID,
			"event_id", rec.Event.ID,
			"subscription_id", rec.SubscriptionID,
			"attempts", rec.Attempts,
		)
		if delErr := d.dlq.Delete(rec.ID); delErr != nil {
			slog.Error("webhooks: auto-retry: failed to delete DLQ record after success",
				"dlq_id", rec.ID,
				"error", delErr,
			)
		}
		return
	}

	metricDLQReplayFailureTotal.Add(1)
	rec.LastError = err.Error()
	// Exponential back-off: 5m, 10m, 20m … capped at 24h.
	// After Attempts is already incremented, the exponent is (Attempts-1).
	// max(..., 0) guards against any future refactoring that removes the pre-increment.
	backoff := d.cfg.DLQCoolingOff * (1 << min(max(rec.Attempts-1, 0), 8))
	if backoff > 24*time.Hour {
		backoff = 24 * time.Hour
	}
	rec.NextRetryAfter = rec.FailedAt.Add(backoff)

	slog.Warn("webhooks: auto-retry: delivery failed",
		"dlq_id", rec.ID,
		"event_id", rec.Event.ID,
		"subscription_id", rec.SubscriptionID,
		"attempts", rec.Attempts,
		"next_retry_after", rec.NextRetryAfter,
		"error", err,
	)

	if saveErr := d.dlq.Save(&rec); saveErr != nil {
		slog.Error("webhooks: auto-retry: failed to update DLQ record",
			"dlq_id", rec.ID,
			"error", saveErr,
		)
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

