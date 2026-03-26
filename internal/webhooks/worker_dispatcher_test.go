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

// noBackoff is a zero-delay RetryBackoff used in tests to avoid real sleeps.
func noBackoff(_ int) time.Duration { return 0 }

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

func TestWorkerDispatcher_StarWildcardSubscription(t *testing.T) {
	var calls atomic.Int32

	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	// Events: ["*"] means "receive all event types" (explicit wildcard).
	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-star", URL: receiver.URL, Enabled: true, Events: []string{"*"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		ServiceName:    "aiistech-backend",
		MaxAttempts:    3,
		WorkerCount:    1,
		TimeoutSeconds: 5,
		RetryBackoff:   noBackoff,
	}, provider)

	if err := d.Dispatch(context.Background(), webhooks.Event{ID: "evt-star", Type: "some.other.event"}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if n := calls.Load(); n != 1 {
		t.Errorf("expected 1 delivery for \"*\" wildcard subscription, got %d", n)
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
