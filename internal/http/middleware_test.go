package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	return chihttp.NewRouter(makeTestRegistry(t), stores, disp)
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

// --- Tenant mode (SiteMiddleware) ---

// newTenantRouter creates a router for a site that has tenant mode enabled.
// It writes a config.yaml with two tenants into the temp directory so that
// SiteMiddleware picks it up.
func newTenantRouter(t *testing.T) http.Handler {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)

	cfgDir := filepath.Join(dir, "contracts", "sites", "local")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	cfgYAML := `site_id: local
tenants:
  - tenant_id: acme
    api_key: "acme-secret"
  - tenant_id: globex
    api_key: "globex-secret"
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	stores := storage.NewRegistry()
	t.Cleanup(func() { stores.CloseAll() })
	return chihttp.NewRouter(makeTestRegistry(t), stores, nil)
}

// doWithHeaders performs an HTTP request with the given extra headers.
func doWithHeaders(t *testing.T, router http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(method, path, nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// TestSiteMiddleware_TenantMode_MissingTenantID returns 400 when X-Tenant-ID is absent.
func TestSiteMiddleware_TenantMode_MissingTenantID(t *testing.T) {
	rr := doWithHeaders(t, newTenantRouter(t), http.MethodGet, "/sites/local/healthz", map[string]string{
		"Authorization": "Bearer acme-secret",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

// TestSiteMiddleware_TenantMode_UnknownTenant returns 400 for an unknown X-Tenant-ID.
func TestSiteMiddleware_TenantMode_UnknownTenant(t *testing.T) {
	rr := doWithHeaders(t, newTenantRouter(t), http.MethodGet, "/sites/local/healthz", map[string]string{
		"X-Tenant-ID":   "unknown",
		"Authorization": "Bearer acme-secret",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

// TestSiteMiddleware_TenantMode_MissingAuth returns 401 when Authorization is absent.
func TestSiteMiddleware_TenantMode_MissingAuth(t *testing.T) {
	rr := doWithHeaders(t, newTenantRouter(t), http.MethodGet, "/sites/local/healthz", map[string]string{
		"X-Tenant-ID": "acme",
	})
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
	if rr.Header().Get("WWW-Authenticate") == "" {
		t.Error("expected WWW-Authenticate header on 401")
	}
}

// TestSiteMiddleware_TenantMode_WrongToken returns 401 when the bearer token is wrong.
func TestSiteMiddleware_TenantMode_WrongToken(t *testing.T) {
	rr := doWithHeaders(t, newTenantRouter(t), http.MethodGet, "/sites/local/healthz", map[string]string{
		"X-Tenant-ID":   "acme",
		"Authorization": "Bearer wrong-secret",
	})
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// TestSiteMiddleware_TenantMode_CrossTenantToken returns 401 when using another tenant's key.
func TestSiteMiddleware_TenantMode_CrossTenantToken(t *testing.T) {
	rr := doWithHeaders(t, newTenantRouter(t), http.MethodGet, "/sites/local/healthz", map[string]string{
		"X-Tenant-ID":   "acme",
		"Authorization": "Bearer globex-secret",
	})
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// TestSiteMiddleware_TenantMode_ValidRequest succeeds with correct credentials.
func TestSiteMiddleware_TenantMode_ValidRequest(t *testing.T) {
	rr := doWithHeaders(t, newTenantRouter(t), http.MethodGet, "/sites/local/healthz", map[string]string{
		"X-Tenant-ID":   "acme",
		"Authorization": "Bearer acme-secret",
	})
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
}

// TestSiteMiddleware_TenantMode_EnforcedOnGET verifies that GET requests require auth in tenant mode.
func TestSiteMiddleware_TenantMode_EnforcedOnGET(t *testing.T) {
	rr := doWithHeaders(t, newTenantRouter(t), http.MethodGet, "/sites/local/healthz", nil)
	if rr.Code == http.StatusOK {
		t.Error("expected non-200 for unauthenticated GET in tenant mode")
	}
}

// TestSiteMiddleware_LegacyMode_GetOpenWithoutAuth verifies that legacy mode
// (no tenants configured) still allows GET without credentials.
func TestSiteMiddleware_LegacyMode_GetOpenWithoutAuth(t *testing.T) {
	t.Chdir(t.TempDir())
	stores := storage.NewRegistry()
	t.Cleanup(func() { stores.CloseAll() })
	router := chihttp.NewRouter(makeTestRegistry(t), stores, nil)

	rr := do(t, router, http.MethodGet, "/sites/local/healthz", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
}
