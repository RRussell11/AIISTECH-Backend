package webhooks_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

// newSubscriptionServer returns an httptest.Server that serves a fixed slice of
// Subscriptions from GET /api/webhook-subscriptions and validates the bearer token.
func newSubscriptionServer(t *testing.T, token string, subs []webhooks.Subscription) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/webhook-subscriptions" {
			http.NotFound(w, r)
			return
		}
		if token != "" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer "+token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(webhooks.ListResponse{Data: subs})
	}))
}

func TestRemoteProvider_ListSubscriptions_OK(t *testing.T) {
	want := []webhooks.Subscription{
		{
			ID:        "sub-1",
			Service:   "aiistech-backend",
			URL:       "https://example.com/hook",
			Enabled:   true,
			Events:    []string{"audit.write"},
			CreatedAt: time.Now().UTC().Truncate(time.Second),
			UpdatedAt: time.Now().UTC().Truncate(time.Second),
		},
	}

	srv := newSubscriptionServer(t, "tok123", want)
	defer srv.Close()

	p := webhooks.NewRemoteProvider(srv.URL, "tok123", 5*time.Second)
	got, err := p.ListSubscriptions(context.Background(), "aiistech-backend", "audit.write", "")
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListSubscriptions() returned %d subscriptions, want 1", len(got))
	}
	if got[0].ID != want[0].ID {
		t.Errorf("ID = %q, want %q", got[0].ID, want[0].ID)
	}
	if got[0].URL != want[0].URL {
		t.Errorf("URL = %q, want %q", got[0].URL, want[0].URL)
	}
}

func TestRemoteProvider_ServiceQueryParam(t *testing.T) {
	var gotService string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotService = r.URL.Query().Get("service")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(webhooks.ListResponse{})
	}))
	defer srv.Close()

	p := webhooks.NewRemoteProvider(srv.URL, "", 5*time.Second)
	if _, err := p.ListSubscriptions(context.Background(), "my-service", "", ""); err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if gotService != "my-service" {
		t.Errorf("service param = %q, want %q", gotService, "my-service")
	}
}

func TestRemoteProvider_EventTypeQueryParam(t *testing.T) {
	var gotEventType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEventType = r.URL.Query().Get("event_type")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(webhooks.ListResponse{})
	}))
	defer srv.Close()

	p := webhooks.NewRemoteProvider(srv.URL, "", 5*time.Second)
	if _, err := p.ListSubscriptions(context.Background(), "", "audit.write", ""); err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if gotEventType != "audit.write" {
		t.Errorf("event_type param = %q, want %q", gotEventType, "audit.write")
	}
}

func TestRemoteProvider_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	p := webhooks.NewRemoteProvider(srv.URL, "", 5*time.Second)
	_, err := p.ListSubscriptions(context.Background(), "svc", "", "")
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestRemoteProvider_ContextCancelled(t *testing.T) {
	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-blocked:
		}
	}))
	defer func() { close(blocked); srv.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the request even starts

	p := webhooks.NewRemoteProvider(srv.URL, "", 5*time.Second)
	_, err := p.ListSubscriptions(ctx, "svc", "", "")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestRemoteProvider_BearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(webhooks.ListResponse{})
	}))
	defer srv.Close()

	p := webhooks.NewRemoteProvider(srv.URL, "supersecret", 5*time.Second)
	if _, err := p.ListSubscriptions(context.Background(), "", "", ""); err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if gotAuth != "Bearer supersecret" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer supersecret")
	}
}
