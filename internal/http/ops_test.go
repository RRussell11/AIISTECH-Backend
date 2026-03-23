package http_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chihttp "github.com/RRussell11/AIISTECH-Backend/internal/http"
	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
)

// newRouterWithOps builds a router using the provided OpsConfig.
func newRouterWithOps(t *testing.T, cfg chihttp.OpsConfig) http.Handler {
	t.Helper()
	stores := storage.NewRegistry()
	t.Cleanup(func() { stores.CloseAll() })
	return chihttp.NewRouter(makeTestRegistry(t), stores, nil, cfg)
}

// --- /version ---

func TestVersionEndpoint(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodGet, "/version", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode version response: %v", err)
	}
	// Keys must be present (values may be empty in test builds).
	for _, key := range []string{"version", "commit", "build_time"} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing key %q in /version response", key)
		}
	}
}

func TestVersionEndpoint_ContentType(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodGet, "/version", nil)
	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// --- CORSMiddleware ---

func TestCORSMiddleware_Disabled_NoHeaders(t *testing.T) {
	// CORS disabled when CORSAllowOrigins is empty (default).
	rr := do(t, newRouter(t), http.MethodGet, "/healthz", nil)
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("expected no CORS header when CORS is disabled")
	}
}

func TestCORSMiddleware_AllowedOrigin(t *testing.T) {
	router := newRouterWithOps(t, chihttp.OpsConfig{
		CORSAllowOrigins: []string{"https://example.com"},
		MaxBodyBytes:     1 << 20,
		RateLimitRPS:     10,
		RateLimitBurst:   20,
	})
	req, _ := http.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://example.com")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "https://example.com")
	}
}

func TestCORSMiddleware_UnknownOrigin_NoHeader(t *testing.T) {
	router := newRouterWithOps(t, chihttp.OpsConfig{
		CORSAllowOrigins: []string{"https://example.com"},
		MaxBodyBytes:     1 << 20,
		RateLimitRPS:     10,
		RateLimitBurst:   20,
	})
	req, _ := http.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://evil.com")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("unexpected Access-Control-Allow-Origin = %q for disallowed origin", got)
	}
}

func TestCORSMiddleware_WildcardOrigin(t *testing.T) {
	router := newRouterWithOps(t, chihttp.OpsConfig{
		CORSAllowOrigins: []string{"*"},
		MaxBodyBytes:     1 << 20,
		RateLimitRPS:     10,
		RateLimitBurst:   20,
	})
	req, _ := http.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://any.com")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "*")
	}
}

func TestCORSMiddleware_PreflightReturns204(t *testing.T) {
	router := newRouterWithOps(t, chihttp.OpsConfig{
		CORSAllowOrigins: []string{"https://example.com"},
		MaxBodyBytes:     1 << 20,
		RateLimitRPS:     10,
		RateLimitBurst:   20,
	})
	req, _ := http.NewRequest(http.MethodOptions, "/sites/local/events", nil)
	req.Header.Set("Origin", "https://example.com")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status = %d, want 204", rr.Code)
	}
}

// --- MaxBodyMiddleware ---

func TestMaxBodyMiddleware_ExceedsLimit_Returns413(t *testing.T) {
	t.Chdir(t.TempDir())
	// Set a 10-byte limit.
	router := newRouterWithOps(t, chihttp.OpsConfig{
		MaxBodyBytes:   10,
		RateLimitRPS:   10,
		RateLimitBurst: 100,
	})
	// Send a body larger than 10 bytes.
	body := bytes.Repeat([]byte("x"), 100)
	req, _ := http.NewRequest(http.MethodPost, "/sites/local/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rr.Code)
	}
}

func TestMaxBodyMiddleware_WithinLimit_Passes(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouterWithOps(t, chihttp.OpsConfig{
		MaxBodyBytes:   1 << 20,
		RateLimitRPS:   10,
		RateLimitBurst: 100,
	})
	rr := do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"ok":true}`))
	if rr.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}
}

func TestMaxBodyMiddleware_GETNotLimited(t *testing.T) {
	// MaxBodyMiddleware must not interfere with GET requests.
	router := newRouterWithOps(t, chihttp.OpsConfig{
		MaxBodyBytes:   1, // absurdly small
		RateLimitRPS:   10,
		RateLimitBurst: 100,
	})
	rr := do(t, router, http.MethodGet, "/healthz", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("GET /healthz status = %d, want 200", rr.Code)
	}
}

// --- RateLimitMiddleware ---

func TestRateLimitMiddleware_ExceedsBurst_Returns429(t *testing.T) {
	t.Chdir(t.TempDir())
	// Set burst=1 so any second request exceeds the burst.
	router := newRouterWithOps(t, chihttp.OpsConfig{
		MaxBodyBytes:   1 << 20,
		RateLimitRPS:   0.001, // effectively 0 tokens replenished during test
		RateLimitBurst: 1,
	})

	// First request should succeed (consumes the single token).
	rr1 := do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"r":1}`))
	if rr1.Code != http.StatusCreated {
		t.Fatalf("first POST status = %d, want 201; body: %s", rr1.Code, rr1.Body.String())
	}

	// Second request should be rate-limited.
	rr2 := do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"r":2}`))
	if rr2.Code != http.StatusTooManyRequests {
		t.Errorf("second POST status = %d, want 429", rr2.Code)
	}

	// 429 response must be JSON with an "error" key.
	var body map[string]string
	if err := json.Unmarshal(rr2.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 429 body: %v", err)
	}
	if body["error"] == "" {
		t.Errorf("expected non-empty 'error' in 429 JSON body")
	}
	if rr2.Header().Get("Retry-After") == "" {
		t.Errorf("expected Retry-After header in 429 response")
	}
}

func TestRateLimitMiddleware_GETNotLimited(t *testing.T) {
	// Rate limiter must not affect read-only requests.
	router := newRouterWithOps(t, chihttp.OpsConfig{
		MaxBodyBytes:   1 << 20,
		RateLimitRPS:   0.001,
		RateLimitBurst: 0, // zero → default 20, but GET is exempt anyway
	})
	for i := 0; i < 5; i++ {
		rr := do(t, router, http.MethodGet, "/healthz", nil)
		if rr.Code != http.StatusOK {
			t.Errorf("GET request %d: status = %d, want 200", i+1, rr.Code)
		}
	}
}
