package webhooks_test

import (
	"context"
	"encoding/json"
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

// recordingProvider records the arguments passed to ListSubscriptions.
type recordingProvider struct {
	mu       sync.Mutex
	lastArgs struct {
		service, eventType, tenantID string
	}
	subs []webhooks.Subscription
}

func (p *recordingProvider) ListSubscriptions(_ context.Context, service, eventType, tenantID string) ([]webhooks.Subscription, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastArgs.service = service
	p.lastArgs.eventType = eventType
	p.lastArgs.tenantID = tenantID
	return p.subs, nil
}

func (p *recordingProvider) recorded() (service, eventType, tenantID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastArgs.service, p.lastArgs.eventType, p.lastArgs.tenantID
}

// noBackoff is a zero-delay RetryBackoff used in tests to avoid real sleeps.
func noBackoff(_ int) time.Duration { return 0 }

// recordingDLQSink is a test-only DLQSink that captures every DLQRecord written.
type recordingDLQSink struct {
	mu      sync.Mutex
	records []webhooks.DLQRecord
}

func (s *recordingDLQSink) WriteDLQ(rec webhooks.DLQRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, rec)
	return nil
}

func (s *recordingDLQSink) recorded() []webhooks.DLQRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]webhooks.DLQRecord, len(s.records))
	copy(out, s.records)
	return out
}

func TestWorkerDispatcher_Delivers(t *testing.T) {
	var (
		received  atomic.Int32
		mu        sync.Mutex
		lastBody  []byte
		lastSig   string
		lastTS    string
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

// TestWorkerDispatcher_PassesTenantIDToProvider verifies that the dispatcher
// forwards the event's TenantID to the provider's ListSubscriptions call.
func TestWorkerDispatcher_PassesTenantIDToProvider(t *testing.T) {
	provider := &recordingProvider{
		subs: []webhooks.Subscription{
			{ID: "sub-8", URL: "http://localhost", Enabled: false, Events: []string{"audit.write"}},
		},
	}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		ServiceName:    "aiistech-backend",
		MaxAttempts:    1,
		WorkerCount:    1,
		TimeoutSeconds: 5,
		RetryBackoff:   noBackoff,
	}, provider)

	evt := webhooks.Event{ID: "evt-8", Type: "audit.write", TenantID: "acme-corp"}
	if err := d.Dispatch(context.Background(), evt); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, _, tenantID := provider.recorded()
	if tenantID != "acme-corp" {
		t.Errorf("ListSubscriptions tenantID = %q, want %q", tenantID, "acme-corp")
	}
}

// TestWorkerDispatcher_EmptyTenantIDToProvider verifies that when an event has
// no TenantID, the provider receives an empty string (default bucket).
func TestWorkerDispatcher_EmptyTenantIDToProvider(t *testing.T) {
	provider := &recordingProvider{
		subs: []webhooks.Subscription{},
	}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		ServiceName:    "aiistech-backend",
		MaxAttempts:    1,
		WorkerCount:    1,
		TimeoutSeconds: 5,
		RetryBackoff:   noBackoff,
	}, provider)

	evt := webhooks.Event{ID: "evt-9", Type: "audit.write"} // no TenantID
	if err := d.Dispatch(context.Background(), evt); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, _, tenantID := provider.recorded()
	if tenantID != "" {
		t.Errorf("ListSubscriptions tenantID = %q, want empty string for default bucket", tenantID)
	}
}

// TestWorkerDispatcher_WritesDLQOnFinalFailure verifies that when all delivery
// attempts are exhausted, WorkerDispatcher writes a DLQRecord to cfg.DLQ.
func TestWorkerDispatcher_WritesDLQOnFinalFailure(t *testing.T) {
	// A server that always returns 503.
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer receiver.Close()

	dlq := &recordingDLQSink{}
	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-dlq", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}},
	}}

	const maxAttempts = 2
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		ServiceName:    "aiistech-backend",
		MaxAttempts:    maxAttempts,
		WorkerCount:    1,
		TimeoutSeconds: 5,
		RetryBackoff:   noBackoff,
		DLQ:            dlq,
	}, provider)

	evt := webhooks.Event{
		ID:       "evt-dlq",
		Type:     "audit.write",
		SiteID:   "mysite",
		TenantID: "t1",
	}
	if err := d.Dispatch(context.Background(), evt); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	recs := dlq.recorded()
	if len(recs) != 1 {
		t.Fatalf("DLQ received %d records, want 1", len(recs))
	}
	rec := recs[0]
	if rec.EventID != evt.ID {
		t.Errorf("DLQ EventID = %q, want %q", rec.EventID, evt.ID)
	}
	if rec.EventType != evt.Type {
		t.Errorf("DLQ EventType = %q, want %q", rec.EventType, evt.Type)
	}
	if rec.SiteID != evt.SiteID {
		t.Errorf("DLQ SiteID = %q, want %q", rec.SiteID, evt.SiteID)
	}
	if rec.TenantID != evt.TenantID {
		t.Errorf("DLQ TenantID = %q, want %q", rec.TenantID, evt.TenantID)
	}
	if rec.SubscriptionID != "sub-dlq" {
		t.Errorf("DLQ SubscriptionID = %q, want %q", rec.SubscriptionID, "sub-dlq")
	}
	if rec.AttemptCount != maxAttempts {
		t.Errorf("DLQ AttemptCount = %d, want %d", rec.AttemptCount, maxAttempts)
	}
	if rec.LastError == "" {
		t.Error("DLQ LastError must not be empty")
	}
	if rec.FailedAt.IsZero() {
		t.Error("DLQ FailedAt must not be zero")
	}
	if len(rec.Payload) == 0 {
		t.Error("DLQ Payload must not be empty")
	}
}

// TestWorkerDispatcher_NoDLQOnSuccess verifies that a successful delivery does
// NOT produce a DLQ record.
func TestWorkerDispatcher_NoDLQOnSuccess(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	dlq := &recordingDLQSink{}
	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-ok", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}},
	}}

	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		ServiceName:    "aiistech-backend",
		MaxAttempts:    3,
		WorkerCount:    1,
		TimeoutSeconds: 5,
		RetryBackoff:   noBackoff,
		DLQ:            dlq,
	}, provider)

	if err := d.Dispatch(context.Background(), webhooks.Event{ID: "evt-ok", Type: "audit.write"}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if recs := dlq.recorded(); len(recs) != 0 {
		t.Errorf("expected 0 DLQ records on success, got %d", len(recs))
	}
}
