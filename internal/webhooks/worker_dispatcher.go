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

	d := &WorkerDispatcher{
		cfg:      cfg,
		provider: provider,
		// Buffer 4× the worker count before Dispatch starts blocking callers.
		jobs:   make(chan dispatchJob, cfg.WorkerCount*4),
		client: &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second},
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
		time.Duration(d.cfg.TimeoutSeconds)*2*time.Second,
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
		d.deliverWithRetry(sub, evt.ID, bodyBytes)
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
func (d *WorkerDispatcher) deliverWithRetry(sub Subscription, eventID string, bodyBytes []byte) {
	var lastErr error
	for attempt := 1; attempt <= d.cfg.MaxAttempts; attempt++ {
		if err := d.deliverOnce(sub, bodyBytes); err == nil {
			slog.Info("webhooks: delivered",
				"subscription_id", sub.ID,
				"event_id", eventID,
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
				"event_id", eventID,
				"attempt", attempt,
				"backoff", backoff,
				"error", lastErr,
			)
			time.Sleep(backoff)
		}
	}
	slog.Error("webhooks: delivery abandoned after max attempts",
		"subscription_id", sub.ID,
		"event_id", eventID,
		"max_attempts", d.cfg.MaxAttempts,
		"error", lastErr,
	)
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
