package http_test

import (
	"bytes"
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

// newOpsRouter builds a router with the given OpsConfig wired in.
func newOpsRouter(t *testing.T, ops chihttp.OpsConfig) http.Handler {
	t.Helper()
	stores := storage.NewRegistry()
	t.Cleanup(func() { stores.CloseAll() })
	return chihttp.NewRouter(makeTestRegistry(t), stores, nil, ops)
}

// ---- CORSMiddleware tests ----

// TestCORS_WildcardSetsHeader verifies that a wildcard config echoes the request
// Origin in Access-Control-Allow-Origin.
func TestCORS_WildcardSetsHeader(t *testing.T) {
	router := newOpsRouter(t, chihttp.OpsConfig{CORSOrigins: "*"})
	req, _ := http.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://example.com")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("ACAO = %q, want %q", got, "https://example.com")
	}
}

// TestCORS_SpecificOriginAllowed verifies that a listed origin is reflected.
func TestCORS_SpecificOriginAllowed(t *testing.T) {
	router := newOpsRouter(t, chihttp.OpsConfig{CORSOrigins: "https://allowed.com"})
	req, _ := http.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://allowed.com")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://allowed.com" {
		t.Errorf("ACAO = %q, want %q", got, "https://allowed.com")
	}
}

// TestCORS_UnlistedOriginBlocked verifies that an unlisted origin does not receive the header.
func TestCORS_UnlistedOriginBlocked(t *testing.T) {
	router := newOpsRouter(t, chihttp.OpsConfig{CORSOrigins: "https://allowed.com"})
	req, _ := http.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://attacker.com")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("ACAO = %q, want empty for unlisted origin", got)
	}
}

// TestCORS_PreflightReturns204 verifies that OPTIONS pre-flight returns 204 with no body.
func TestCORS_PreflightReturns204(t *testing.T) {
	router := newOpsRouter(t, chihttp.OpsConfig{CORSOrigins: "*"})
	req, _ := http.NewRequest(http.MethodOptions, "/healthz", nil)
	req.Header.Set("Origin", "https://example.com")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got == "" {
		t.Error("expected ACAO header on OPTIONS pre-flight")
	}
}

// TestCORS_Disabled verifies that no CORS headers are added when CORSOrigins is "".
func TestCORS_Disabled(t *testing.T) {
	router := newOpsRouter(t, chihttp.OpsConfig{})
	req, _ := http.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://example.com")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("ACAO = %q, want empty when CORS is disabled", got)
	}
}

// ---- MaxBodyMiddleware tests ----

// TestMaxBody_WithinLimit verifies that a request within the limit is processed normally.
func TestMaxBody_WithinLimit(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newOpsRouter(t, chihttp.OpsConfig{MaxBodyBytes: 1024})
	body := []byte(`{"small":true}`)
	req, _ := http.NewRequest(http.MethodPost, "/sites/local/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rr.Code)
	}
}

// TestMaxBody_ExceedsLimit verifies that an oversized body results in a non-2xx response.
func TestMaxBody_ExceedsLimit(t *testing.T) {
	t.Chdir(t.TempDir())
	// Set a very small limit (10 bytes) so even a tiny JSON body is over.
	router := newOpsRouter(t, chihttp.OpsConfig{MaxBodyBytes: 10})
	body := []byte(`{"this_key_is_definitely_too_long": true}`)
	req, _ := http.NewRequest(http.MethodPost, "/sites/local/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	// chi's Recoverer catches the MaxBytesError and returns 413 or the handler
	// returns 400 on body-read failure; either way the response must not be 2xx.
	if rr.Code >= 200 && rr.Code < 300 {
		t.Errorf("status = %d, want non-2xx for oversized body", rr.Code)
	}
}

// ---- RateLimitMiddleware tests ----

// TestRateLimit_BelowLimit verifies that requests within the limit succeed.
func TestRateLimit_BelowLimit(t *testing.T) {
	// 10 RPS, burst=10 — first request must always go through.
	router := newOpsRouter(t, chihttp.OpsConfig{RateLimitRPS: 10, RateLimitBurst: 10})
	req, _ := http.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "192.0.2.1:12345"
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for first request within burst", rr.Code)
	}
}

// TestRateLimit_ExceedsLimit verifies that a burst of requests beyond the limit
// receives 429 Too Many Requests.
func TestRateLimit_ExceedsLimit(t *testing.T) {
	// 1 RPS, burst=1 — second consecutive request from same IP should be throttled.
	router := newOpsRouter(t, chihttp.OpsConfig{RateLimitRPS: 1, RateLimitBurst: 1})

	var lastCode int
	for i := 0; i < 20; i++ {
		req, _ := http.NewRequest(http.MethodGet, "/healthz", nil)
		req.RemoteAddr = "192.0.2.42:9999"
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		lastCode = rr.Code
		if rr.Code == http.StatusTooManyRequests {
			return // got the expected 429
		}
	}
	t.Errorf("last status = %d; expected at least one 429 after exhausting burst", lastCode)
}

// TestRateLimit_Disabled verifies that no rate limiting occurs when RPS is 0.
func TestRateLimit_Disabled(t *testing.T) {
	router := newOpsRouter(t, chihttp.OpsConfig{RateLimitRPS: 0})
	for i := 0; i < 50; i++ {
		req, _ := http.NewRequest(http.MethodGet, "/healthz", nil)
		req.RemoteAddr = "192.0.2.99:1234"
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 with rate limiting disabled", i, rr.Code)
		}
	}
}

// TestRateLimit_DifferentIPsNotThrottled verifies that two distinct IPs have independent limiters.
func TestRateLimit_DifferentIPsNotThrottled(t *testing.T) {
	// burst=1 so a second request from the same IP would be throttled,
	// but a request from a different IP should still succeed.
	router := newOpsRouter(t, chihttp.OpsConfig{RateLimitRPS: 1, RateLimitBurst: 1})

	for _, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		req, _ := http.NewRequest(http.MethodGet, "/healthz", nil)
		req.RemoteAddr = ip + ":8080"
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("IP %s: status = %d, want 200 (first request should pass)", ip, rr.Code)
		}
	}
}
