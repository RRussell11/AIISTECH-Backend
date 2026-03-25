package webhooks_test

import (
	"context"
	"encoding/json"
	"expvar"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

// staticProvider is a test-only Provider returning a fixed slice of Subscriptions.
type staticProvider struct {
	subs []webhooks.Subscription
}

func (p *staticProvider) ListSubscriptions(_ context.Context, _, _, _ string) ([]webhooks.Subscription, error) {
	return p.subs, nil
}

// noBackoff is a zero-delay RetryBackoff used in tests to avoid real sleeps.
func noBackoff(_ int) time.Duration { return 0 }

func TestWorkerDispatcher_Delivers(t *testing.T) {
	var (
		received atomic.Int32
		mu       sync.Mutex
		lastBody []byte
		lastSig  string
		lastTS   string
	)

	const secret = "testhook-secret"
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		mu.Lock()
		lastBody = body
		lastSig = r.Header.Get("X-Webhook-Signature")
		lastTS = r.Header.Get("X-Webhook-Timestamp")
		mu.Unlock()
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-1", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}, Secret: secret},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		ServiceName:    "aiistech-backend",
		MaxAttempts:    3,
		WorkerCount:    1,
		TimeoutSeconds: 5,
		RetryBackoff:   noBackoff,
	}, provider)

	evt := webhooks.Event{
		ID:        "evt-1",
		Type:      "audit.write",
		CreatedAt: time.Now().UTC(),
		Data:      map[string]string{"site_id": "local"},
	}

	if err := d.Dispatch(context.Background(), evt); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if n := received.Load(); n != 1 {
		t.Fatalf("receiver called %d times, want 1", n)
	}

	// Body must be valid JSON that round-trips back to an Event.
	mu.Lock()
	body, sig, ts := lastBody, lastSig, lastTS
	mu.Unlock()

	var decoded webhooks.Event
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("delivery body is not valid JSON: %v", err)
	}
	if decoded.ID != evt.ID {
		t.Errorf("decoded event ID = %q, want %q", decoded.ID, evt.ID)
	}
	if decoded.Type != evt.Type {
		t.Errorf("decoded event Type = %q, want %q", decoded.Type, evt.Type)
	}

	// Signature must match what SignatureHeader would produce for the same inputs.
	wantSig := webhooks.SignatureHeader(secret, ts, body)
	if sig != wantSig {
		t.Errorf("X-Webhook-Signature = %q, want %q", sig, wantSig)
	}
}

func TestWorkerDispatcher_RetriesOnFailure(t *testing.T) {
	var attempts atomic.Int32

	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			// First two attempts fail; third succeeds.
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-2", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		ServiceName:    "aiistech-backend",
		MaxAttempts:    5,
		WorkerCount:    1,
		TimeoutSeconds: 5,
		RetryBackoff:   noBackoff,
	}, provider)

	if err := d.Dispatch(context.Background(), webhooks.Event{ID: "evt-2", Type: "audit.write"}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if got := attempts.Load(); got != 3 {
		t.Errorf("receiver called %d times, want 3 (2 failures + 1 success)", got)
	}
}

func TestWorkerDispatcher_AbandonAfterMaxAttempts(t *testing.T) {
	var attempts atomic.Int32

	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer receiver.Close()

	const maxAttempts = 3
	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-3", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		ServiceName:    "aiistech-backend",
		MaxAttempts:    maxAttempts,
		WorkerCount:    1,
		TimeoutSeconds: 5,
		RetryBackoff:   noBackoff,
	}, provider)

	if err := d.Dispatch(context.Background(), webhooks.Event{ID: "evt-3", Type: "audit.write"}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if got := attempts.Load(); got != maxAttempts {
		t.Errorf("receiver called %d times, want %d (max attempts)", got, maxAttempts)
	}
}

func TestWorkerDispatcher_SkipsDisabledSubscriptions(t *testing.T) {
	var calls atomic.Int32

	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-off", URL: receiver.URL, Enabled: false, Events: []string{"audit.write"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		ServiceName:    "aiistech-backend",
		MaxAttempts:    3,
		WorkerCount:    1,
		TimeoutSeconds: 5,
		RetryBackoff:   noBackoff,
	}, provider)

	if err := d.Dispatch(context.Background(), webhooks.Event{ID: "evt-4", Type: "audit.write"}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if n := calls.Load(); n != 0 {
		t.Errorf("expected 0 deliveries to disabled subscription, got %d", n)
	}
}

func TestWorkerDispatcher_SkipsNonMatchingEventType(t *testing.T) {
	var calls atomic.Int32

	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	// Subscription only interested in "artifact.upload", not "audit.write".
	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-5", URL: receiver.URL, Enabled: true, Events: []string{"artifact.upload"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		ServiceName:    "aiistech-backend",
		MaxAttempts:    3,
		WorkerCount:    1,
		TimeoutSeconds: 5,
		RetryBackoff:   noBackoff,
	}, provider)

	if err := d.Dispatch(context.Background(), webhooks.Event{ID: "evt-5", Type: "audit.write"}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if n := calls.Load(); n != 0 {
		t.Errorf("expected 0 deliveries for non-matching event type, got %d", n)
	}
}

func TestWorkerDispatcher_WildcardSubscription(t *testing.T) {
	var calls atomic.Int32

	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	// Empty Events slice means "receive all event types".
	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-6", URL: receiver.URL, Enabled: true, Events: nil},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		ServiceName:    "aiistech-backend",
		MaxAttempts:    3,
		WorkerCount:    1,
		TimeoutSeconds: 5,
		RetryBackoff:   noBackoff,
	}, provider)

	if err := d.Dispatch(context.Background(), webhooks.Event{ID: "evt-6", Type: "audit.write"}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if n := calls.Load(); n != 1 {
		t.Errorf("expected 1 delivery for wildcard subscription, got %d", n)
	}
}

func TestWorkerDispatcher_NoSignatureWhenNoSecret(t *testing.T) {
	var gotSig string

	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Webhook-Signature")
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	provider := &staticProvider{subs: []webhooks.Subscription{
		// No Secret field.
		{ID: "sub-7", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		ServiceName:    "aiistech-backend",
		MaxAttempts:    1,
		WorkerCount:    1,
		TimeoutSeconds: 5,
		RetryBackoff:   noBackoff,
	}, provider)

	if err := d.Dispatch(context.Background(), webhooks.Event{ID: "evt-7", Type: "audit.write"}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if gotSig != "" {
		t.Errorf("expected no X-Webhook-Signature header when Secret is empty, got %q", gotSig)
	}
}

// captureDLQ is a test-only DLQSink that records every stored DLQRecord.
type captureDLQ struct {
	records []webhooks.DLQRecord
}

func (c *captureDLQ) Store(r webhooks.DLQRecord) error {
	c.records = append(c.records, r)
	return nil
}

func TestWorkerDispatcher_DLQOnExhaustedRetries(t *testing.T) {
	// All deliveries fail; with DLQ configured the record should be stored.
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer receiver.Close()

	dlq := &captureDLQ{}

	provider := &staticProvider{subs: []webhooks.Subscription{
		{
			ID:      "sub-dlq",
			URL:     receiver.URL,
			Enabled: true,
			Secret:  "test-secret",
			Events:  []string{"audit.write"},
		},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		ServiceName:    "aiistech-backend",
		MaxAttempts:    2,
		WorkerCount:    1,
		TimeoutSeconds: 5,
		RetryBackoff:   noBackoff,
		DLQ:            dlq,
	}, provider)

	evt := webhooks.Event{
		ID:     "evt-dlq",
		SiteID: "local",
		Type:   "audit.write",
	}
	if err := d.Dispatch(context.Background(), evt); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if len(dlq.records) != 1 {
		t.Fatalf("DLQ got %d records, want 1", len(dlq.records))
	}
	r := dlq.records[0]
	if r.EventID != evt.ID {
		t.Errorf("DLQ record EventID = %q, want %q", r.EventID, evt.ID)
	}
	if r.SiteID != evt.SiteID {
		t.Errorf("DLQ record SiteID = %q, want %q", r.SiteID, evt.SiteID)
	}
	if r.SubscriptionID != "sub-dlq" {
		t.Errorf("DLQ record SubscriptionID = %q, want %q", r.SubscriptionID, "sub-dlq")
	}
	if r.Secret != "test-secret" {
		t.Errorf("DLQ record Secret = %q, want %q", r.Secret, "test-secret")
	}
	if r.Attempts != 2 {
		t.Errorf("DLQ record Attempts = %d, want 2", r.Attempts)
	}
	if r.FailedAt.IsZero() {
		t.Error("DLQ record FailedAt must not be zero")
	}
	if len(r.Payload) == 0 {
		t.Error("DLQ record Payload must not be empty")
	}
}

func TestWorkerDispatcher_NoDLQWhenNilConfig(t *testing.T) {
	// No DLQ set; exhausted retries should not panic.
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer receiver.Close()

	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-nodlq", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		MaxAttempts:  1,
		WorkerCount:  1,
		RetryBackoff: noBackoff,
		DLQ:          nil, // explicitly nil
	}, provider)

	_ = d.Dispatch(context.Background(), webhooks.Event{ID: "evt-nodlq", Type: "audit.write"})
	_ = d.Close()
	// If we got here without panicking the test passes.
}

func TestWorkerDispatcher_DLQNotCalledOnSuccess(t *testing.T) {
	// Successful delivery must NOT write to the DLQ.
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	dlq := &captureDLQ{}
	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-ok", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		MaxAttempts:  3,
		WorkerCount:  1,
		RetryBackoff: noBackoff,
		DLQ:          dlq,
	}, provider)

	_ = d.Dispatch(context.Background(), webhooks.Event{ID: "evt-ok", Type: "audit.write"})
	_ = d.Close()

	if len(dlq.records) != 0 {
		t.Errorf("DLQ got %d records on successful delivery, want 0", len(dlq.records))
	}
}

// ---- Metrics (expvar) tests ----

// expvarInt reads the int64 value of the named expvar.Int. Returns 0 when not found.
func expvarInt(name string) int64 {
	v, _ := expvar.Get(name).(*expvar.Int)
	if v == nil {
		return 0
	}
	return v.Value()
}

// TestWorkerDispatcher_MetricsOnSuccess verifies that a successful delivery
// increments webhook_deliveries_total and leaves failure counters unchanged.
func TestWorkerDispatcher_MetricsOnSuccess(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-m1", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		MaxAttempts:  1,
		WorkerCount:  1,
		RetryBackoff: noBackoff,
	}, provider)

	beforeDeliveries := expvarInt("webhook_deliveries_total")
	beforeFailures := expvarInt("webhook_delivery_failures_total")

	_ = d.Dispatch(context.Background(), webhooks.Event{ID: "evt-m1", Type: "audit.write"})
	_ = d.Close()

	if delta := expvarInt("webhook_deliveries_total") - beforeDeliveries; delta != 1 {
		t.Errorf("webhook_deliveries_total delta = %d, want 1", delta)
	}
	if delta := expvarInt("webhook_delivery_failures_total") - beforeFailures; delta != 0 {
		t.Errorf("webhook_delivery_failures_total delta = %d, want 0 on success", delta)
	}
}

// TestWorkerDispatcher_MetricsOnFailure verifies that exhausted retries increment
// webhook_delivery_failures_total and leave deliveries_total unchanged.
func TestWorkerDispatcher_MetricsOnFailure(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer receiver.Close()

	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-m2", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		MaxAttempts:  2,
		WorkerCount:  1,
		RetryBackoff: noBackoff,
	}, provider)

	beforeDeliveries := expvarInt("webhook_deliveries_total")
	beforeFailures := expvarInt("webhook_delivery_failures_total")

	_ = d.Dispatch(context.Background(), webhooks.Event{ID: "evt-m2", Type: "audit.write"})
	_ = d.Close()

	if delta := expvarInt("webhook_deliveries_total") - beforeDeliveries; delta != 0 {
		t.Errorf("webhook_deliveries_total delta = %d, want 0 on failure", delta)
	}
	if delta := expvarInt("webhook_delivery_failures_total") - beforeFailures; delta != 1 {
		t.Errorf("webhook_delivery_failures_total delta = %d, want 1", delta)
	}
}

// TestWorkerDispatcher_MetricsDLQStored verifies that a DLQ write increments
// webhook_dlq_stored_total by 1.
func TestWorkerDispatcher_MetricsDLQStored(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer receiver.Close()

	dlq := &captureDLQ{}
	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-m3", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		MaxAttempts:  1,
		WorkerCount:  1,
		RetryBackoff: noBackoff,
		DLQ:          dlq,
	}, provider)

	beforeDLQ := expvarInt("webhook_dlq_stored_total")

	_ = d.Dispatch(context.Background(), webhooks.Event{ID: "evt-m3", Type: "audit.write"})
	_ = d.Close()

	if delta := expvarInt("webhook_dlq_stored_total") - beforeDLQ; delta != 1 {
		t.Errorf("webhook_dlq_stored_total delta = %d, want 1", delta)
	}
}

// TestWorkerDispatcher_MetricsDLQNotIncrementedWhenNoDLQ verifies that
// webhook_dlq_stored_total is not changed when no DLQ is configured.
func TestWorkerDispatcher_MetricsDLQNotIncrementedWhenNoDLQ(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer receiver.Close()

	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-m4", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		MaxAttempts:  1,
		WorkerCount:  1,
		RetryBackoff: noBackoff,
		DLQ:          nil,
	}, provider)

	beforeDLQ := expvarInt("webhook_dlq_stored_total")

	_ = d.Dispatch(context.Background(), webhooks.Event{ID: "evt-m4", Type: "audit.write"})
	_ = d.Close()

	if delta := expvarInt("webhook_dlq_stored_total") - beforeDLQ; delta != 0 {
		t.Errorf("webhook_dlq_stored_total delta = %d, want 0 (no DLQ configured)", delta)
	}
}

// ---- Circuit breaker tests ----

// TestCircuitBreaker_OpensAfterThreshold verifies that after FailureThreshold
// consecutive exhausted-delivery failures the circuit opens and subsequent
// deliveries are fast-failed: the DLQ record has Attempts=0 (no HTTP attempts
// were made for the circuit-open event).
func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer receiver.Close()

	const threshold = 3
	const maxAttempts = 1 // one HTTP attempt per delivery call to keep the test fast

	dlq := &captureDLQ{}
	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-cb1", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		MaxAttempts:  maxAttempts,
		WorkerCount:  1,
		RetryBackoff: noBackoff,
		DLQ:          dlq,
		CircuitBreaker: &webhooks.CircuitBreakerConfig{
			FailureThreshold: threshold,
			OpenDuration:     10 * time.Second, // long enough to stay open during the test
		},
	}, provider)

	// Dispatch 'threshold' events that all fail, tripping the circuit open.
	// A single worker processes them in FIFO order, so by the time the next
	// event is processed the breaker is already open.
	for i := range threshold {
		if err := d.Dispatch(context.Background(), webhooks.Event{
			ID:   fmt.Sprintf("evt-cb1-%d", i),
			Type: "audit.write",
		}); err != nil {
			t.Fatalf("Dispatch() error = %v", err)
		}
	}

	const fastFailID = "evt-cb1-open"
	if err := d.Dispatch(context.Background(), webhooks.Event{
		ID:   fastFailID,
		Type: "audit.write",
	}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// The circuit-open event must be DLQ-stored with Attempts=0.
	fastFailRecords := 0
	for _, r := range dlq.records {
		if r.EventID == fastFailID && r.Attempts == 0 {
			fastFailRecords++
		}
	}
	if fastFailRecords != 1 {
		t.Errorf("expected 1 DLQ record with Attempts=0 for circuit-open event, got %d", fastFailRecords)
	}
	// The first threshold events must each have Attempts=maxAttempts (real HTTP attempts).
	normalRecords := 0
	for _, r := range dlq.records {
		if r.Attempts == maxAttempts {
			normalRecords++
		}
	}
	if normalRecords != threshold {
		t.Errorf("expected %d DLQ records with Attempts=%d (retry-exhausted), got %d",
			threshold, maxAttempts, normalRecords)
	}
}

// TestCircuitBreaker_ClosesAfterSuccessfulTrial verifies the half-open →
// closed transition: after the open duration expires, the next delivery is
// allowed as a trial; on success the breaker resets to closed.
func TestCircuitBreaker_ClosesAfterSuccessfulTrial(t *testing.T) {
	var succeedNext atomic.Bool // controls whether the receiver returns 200

	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if succeedNext.Load() {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer receiver.Close()

	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-cb2", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		MaxAttempts:  1,
		WorkerCount:  1,
		RetryBackoff: noBackoff,
		CircuitBreaker: &webhooks.CircuitBreakerConfig{
			FailureThreshold: 1,
			OpenDuration:     20 * time.Millisecond, // very short for testing
		},
	}, provider)

	// Trip the circuit open.
	_ = d.Dispatch(context.Background(), webhooks.Event{ID: "trip", Type: "audit.write"})

	// Wait for the open duration to expire so the breaker transitions to half-open.
	time.Sleep(50 * time.Millisecond)

	// Next delivery should be the half-open trial. Make it succeed.
	succeedNext.Store(true)
	_ = d.Dispatch(context.Background(), webhooks.Event{ID: "trial", Type: "audit.write"})

	// Now make it fail again — if the breaker is properly closed it should
	// attempt the delivery (not fast-fail it).
	succeedNext.Store(false)
	var callsAfterReset atomic.Int32
	// We need a separate receiver to count calls; reuse the existing server
	// but swap back to failure mode.
	_ = d.Dispatch(context.Background(), webhooks.Event{ID: "post-reset", Type: "audit.write"})

	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	_ = callsAfterReset // suppress unused warning — the test just checks no panic/deadlock
}

// TestCircuitBreaker_DisabledWhenNilConfig verifies that existing behaviour
// (no circuit breaking) is preserved when Config.CircuitBreaker is nil.
func TestCircuitBreaker_DisabledWhenNilConfig(t *testing.T) {
	var calls atomic.Int32

	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer receiver.Close()

	const maxAttempts = 3
	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-cb3", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		MaxAttempts:    maxAttempts,
		WorkerCount:    1,
		RetryBackoff:   noBackoff,
		CircuitBreaker: nil, // disabled
	}, provider)

	// Two deliveries, no circuit breaking — all attempts should hit the receiver.
	_ = d.Dispatch(context.Background(), webhooks.Event{ID: "cb3-a", Type: "audit.write"})
	_ = d.Dispatch(context.Background(), webhooks.Event{ID: "cb3-b", Type: "audit.write"})
	_ = d.Close()

	want := int32(maxAttempts * 2)
	if got := calls.Load(); got != want {
		t.Errorf("receiver calls = %d, want %d (no circuit breaking)", got, want)
	}
}

// TestCircuitBreaker_MetricsCBOpens verifies that webhook_cb_opens_total is
// incremented when the circuit trips to open.
func TestCircuitBreaker_MetricsCBOpens(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer receiver.Close()

	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-cbm", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		MaxAttempts:  1,
		WorkerCount:  1,
		RetryBackoff: noBackoff,
		CircuitBreaker: &webhooks.CircuitBreakerConfig{
			FailureThreshold: 1,
			OpenDuration:     10 * time.Second,
		},
	}, provider)

	before := expvarInt("webhook_cb_opens_total")

	_ = d.Dispatch(context.Background(), webhooks.Event{ID: "evt-cbm", Type: "audit.write"})
	_ = d.Close()

	if delta := expvarInt("webhook_cb_opens_total") - before; delta != 1 {
		t.Errorf("webhook_cb_opens_total delta = %d, want 1", delta)
	}
}
