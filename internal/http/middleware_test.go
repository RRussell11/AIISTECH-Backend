package http_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	auditpkg "github.com/RRussell11/AIISTECH-Backend/internal/audit"
	chihttp "github.com/RRussell11/AIISTECH-Backend/internal/http"
	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

// captureDispatcher is a test-only webhooks.Dispatcher that records every
// dispatched Event so tests can assert on them.
type captureDispatcher struct {
	mu     sync.Mutex
	events []webhooks.Event
}

func (c *captureDispatcher) Dispatch(_ context.Context, evt webhooks.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, evt)
	return nil
}

func (c *captureDispatcher) Close() error { return nil }

func (c *captureDispatcher) captured() []webhooks.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]webhooks.Event, len(c.events))
	copy(out, c.events)
	return out
}

// newRouterWithDispatcher builds a router wired to the given dispatcher.
func newRouterWithDispatcher(t *testing.T, disp webhooks.Dispatcher) http.Handler {
	t.Helper()
	stores := storage.NewRegistry()
	t.Cleanup(func() { stores.CloseAll() })
	return chihttp.NewRouter(makeTestRegistry(t), stores, disp, nil, nil, nil, "")
}

// TestAuditMiddleware_DispatchFiredOnPost verifies that a POST request causes
// exactly one "audit.write" webhook event to be dispatched.
func TestAuditMiddleware_DispatchFiredOnPost(t *testing.T) {
	t.Chdir(t.TempDir())

	disp := &captureDispatcher{}
	router := newRouterWithDispatcher(t, disp)

	rr := do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"test":true}`))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}

	evts := disp.captured()
	if len(evts) != 1 {
		t.Fatalf("dispatched %d events, want 1", len(evts))
	}

	evt := evts[0]
	if evt.Type != "audit.write" {
		t.Errorf("event Type = %q, want %q", evt.Type, "audit.write")
	}
	if evt.ID == "" {
		t.Error("event ID must not be empty")
	}
	if evt.CreatedAt.IsZero() {
		t.Error("event CreatedAt must not be zero")
	}
	if evt.Data == nil {
		t.Error("event Data must not be nil")
	}
}

// TestAuditMiddleware_EventDataIsAuditEntry verifies that the webhook event Data
// carries an audit.Entry with the correct SiteID, Method, and Path.
func TestAuditMiddleware_EventDataIsAuditEntry(t *testing.T) {
	t.Chdir(t.TempDir())

	disp := &captureDispatcher{}
	router := newRouterWithDispatcher(t, disp)

	rr := do(t, router, http.MethodPost, "/sites/staging/events", []byte(`{"x":1}`))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}

	evts := disp.captured()
	if len(evts) != 1 {
		t.Fatalf("dispatched %d events, want 1", len(evts))
	}

	entry, ok := evts[0].Data.(auditpkg.Entry)
	if !ok {
		t.Fatalf("event Data type = %T, want audit.Entry", evts[0].Data)
	}
	if entry.SiteID != "staging" {
		t.Errorf("entry SiteID = %q, want %q", entry.SiteID, "staging")
	}
	if entry.Method != http.MethodPost {
		t.Errorf("entry Method = %q, want %q", entry.Method, http.MethodPost)
	}
	if entry.Path != "/sites/staging/events" {
		t.Errorf("entry Path = %q, want %q", entry.Path, "/sites/staging/events")
	}
}

// TestAuditMiddleware_NilDispatcherSafe verifies that passing a nil dispatcher
// does not panic and the handler still responds normally.
func TestAuditMiddleware_NilDispatcherSafe(t *testing.T) {
	t.Chdir(t.TempDir())

	router := newRouterWithDispatcher(t, nil)
	rr := do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"test":true}`))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}
	// No panic = pass.
}

// TestAuditMiddleware_NoDispatchOnGET verifies that read-only requests do not
// trigger webhook dispatch.
func TestAuditMiddleware_NoDispatchOnGET(t *testing.T) {
	disp := &captureDispatcher{}
	router := newRouterWithDispatcher(t, disp)

	rr := do(t, router, http.MethodGet, "/sites/local/healthz", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	if evts := disp.captured(); len(evts) != 0 {
		t.Errorf("expected 0 dispatched events for GET, got %d", len(evts))
	}
}

// TestAuditMiddleware_DeleteDispatchFired verifies that DELETE also triggers dispatch.
func TestAuditMiddleware_DeleteDispatchFired(t *testing.T) {
	t.Chdir(t.TempDir())

	disp := &captureDispatcher{}
	router := newRouterWithDispatcher(t, disp)

	// Create an artifact to delete.
	postRR := do(t, router, http.MethodPost, "/sites/local/artifacts", []byte(`{"k":"v"}`))
	if postRR.Code != http.StatusCreated {
		t.Fatalf("POST artifact status = %d, want 201; body: %s", postRR.Code, postRR.Body.String())
	}

	// Clear captured events from the POST so we only count the DELETE event.
	disp.mu.Lock()
	disp.events = nil
	disp.mu.Unlock()

	// DELETE a non-existent artifact — 404 is expected. AuditMiddleware fires
	// after the handler regardless of status, so the event must still be dispatched.
	deleteRR := do(t, router, http.MethodDelete, "/sites/local/artifacts/nosuchfile.json", nil)
	if deleteRR.Code == http.StatusInternalServerError {
		t.Fatalf("unexpected 500 on DELETE")
	}

	evts := disp.captured()
	if len(evts) != 1 {
		t.Errorf("expected 1 dispatched event for DELETE, got %d", len(evts))
	}
	if len(evts) > 0 && evts[0].Type != "audit.write" {
		t.Errorf("event Type = %q, want %q", evts[0].Type, "audit.write")
	}
}

// --- SecurityHeadersMiddleware ---

// newSecurityRouter builds a minimal router with SecurityHeadersMiddleware wired.
func newSecurityRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return chihttp.SecurityHeadersMiddleware(mux)
}

func TestSecurityHeadersMiddleware_HeadersPresent(t *testing.T) {
	router := newSecurityRouter()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"X-XSS-Protection":       "0",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}
	for header, expected := range want {
		if got := rr.Header().Get(header); got != expected {
			t.Errorf("header %s = %q, want %q", header, got, expected)
		}
	}
}

func TestSecurityHeadersMiddleware_ViaRouter(t *testing.T) {
	// Ensure headers are present on a real router response.
	router := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rr.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
}

// --- MaxBytesMiddleware ---

func TestMaxBytesMiddleware_SmallBodyAllowed(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)
	body := bytes.Repeat([]byte("x"), 512) // well under 1 MiB
	req := httptest.NewRequest(http.MethodPost, "/sites/local/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	// Body is not valid JSON so expect 400 Bad Request — not 413.
	if rr.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("unexpected 413 for a small body")
	}
}

func TestMaxBytesMiddleware_OversizedBodyRejected(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)
	body := bytes.Repeat([]byte("a"), (1<<20)+1) // 1 MiB + 1 byte
	req := httptest.NewRequest(http.MethodPost, "/sites/local/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	// The handler reads the oversized body via json.Decode, which fails.
	// The handler should NOT return 200 or 201.
	if rr.Code == http.StatusOK || rr.Code == http.StatusCreated {
		t.Fatalf("expected non-2xx for oversized body, got %d", rr.Code)
	}
}

// --- AdminAuthMiddleware ---

// openTestDLQRouter builds a router with a DLQ store and the given adminAPIKey.
func openTestAdminRouter(t *testing.T, adminKey string) http.Handler {
	t.Helper()
	s, err := storage.Open(filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	sp := webhooks.NewStoreProvider(s)
	dlqStore := webhooks.NewDLQStore(s)
	stores := storage.NewRegistry()
	t.Cleanup(func() { stores.CloseAll() })
	return chihttp.NewRouter(makeTestRegistry(t), stores, nil, dlqStore, &successReplayer{}, sp, adminKey)
}

// TestAdminAuthMiddleware_NoKeyAllowsAll verifies that when adminAPIKey is empty
// all requests reach the handler without any Bearer token.
func TestAdminAuthMiddleware_NoKeyAllowsAll(t *testing.T) {
	router := openTestAdminRouter(t, "")
	req := httptest.NewRequest(http.MethodGet, "/webhooks/subscriptions/", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code == http.StatusUnauthorized {
		t.Fatal("expected no auth enforcement when adminAPIKey is empty, got 401")
	}
}

// TestAdminAuthMiddleware_WithKeyRejectsNoToken verifies that a request without
// a Bearer token gets 401 when an admin key is configured.
func TestAdminAuthMiddleware_WithKeyRejectsNoToken(t *testing.T) {
	router := openTestAdminRouter(t, "supersecret")
	for _, path := range []string{
		"/webhooks/subscriptions/",
		"/webhooks/dlq/",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without token: status = %d, want 401", path, rr.Code)
		}
		if !strings.Contains(rr.Header().Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("GET %s: missing WWW-Authenticate Bearer challenge", path)
		}
	}
}

// TestAdminAuthMiddleware_WithKeyRejectsWrongToken verifies that a request with
// a wrong Bearer token gets 401.
func TestAdminAuthMiddleware_WithKeyRejectsWrongToken(t *testing.T) {
	router := openTestAdminRouter(t, "supersecret")
	req := httptest.NewRequest(http.MethodGet, "/webhooks/subscriptions/", nil)
	req.Header.Set("Authorization", "Bearer wrongtoken")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// TestAdminAuthMiddleware_WithKeyAcceptsCorrectToken verifies that the correct
// Bearer token passes the admin auth gate.
func TestAdminAuthMiddleware_WithKeyAcceptsCorrectToken(t *testing.T) {
	router := openTestAdminRouter(t, "supersecret")
	req := httptest.NewRequest(http.MethodGet, "/webhooks/subscriptions/", nil)
	req.Header.Set("Authorization", "Bearer supersecret")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code == http.StatusUnauthorized {
		t.Fatalf("expected non-401 with correct token, got 401")
	}
}

// TestAdminAuthMiddleware_GetRequiresAuthToo verifies that GET requests (which
// site AuthMiddleware allows without auth) are also gated by AdminAuthMiddleware.
func TestAdminAuthMiddleware_GetRequiresAuthToo(t *testing.T) {
	router := openTestAdminRouter(t, "adminkey")
	req := httptest.NewRequest(http.MethodGet, "/webhooks/dlq/", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("GET /webhooks/dlq/ without token: status = %d, want 401", rr.Code)
	}
}
