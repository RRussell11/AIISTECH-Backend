package http_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	chihttp "github.com/RRussell11/AIISTECH-Backend/internal/http"
	"github.com/RRussell11/AIISTECH-Backend/internal/site"
)

// makeTestRegistry returns a Registry loaded from a temp file with local+staging.
func makeTestRegistry(t *testing.T) *site.Registry {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "sites.yaml")
	content := `
default_site_id: local
sites:
  - site_id: local
  - site_id: staging
`
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("writing registry: %v", err)
	}
	reg, err := site.LoadRegistry(p)
	if err != nil {
		t.Fatalf("loading registry: %v", err)
	}
	return reg
}

func newRouter(t *testing.T) http.Handler {
	t.Helper()
	return chihttp.NewRouter(makeTestRegistry(t))
}

// do performs an HTTP request against the router and returns the response recorder.
func do(t *testing.T, router http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequest(method, path, nil)
	}
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// --- /healthz ---

func TestHealthz(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodGet, "/healthz", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status field = %q, want %q", body["status"], "ok")
	}
}

// --- GET /sites ---

func TestListSites(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodGet, "/sites", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["default_site_id"] != "local" {
		t.Errorf("default_site_id = %v, want local", body["default_site_id"])
	}
	sites, ok := body["sites"].([]any)
	if !ok || len(sites) != 2 {
		t.Errorf("expected 2 sites, got %v", body["sites"])
	}
}

// --- GET /sites/{site_id} ---

func TestGetSite_Valid(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodGet, "/sites/local/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["site_id"] != "local" {
		t.Errorf("site_id = %q, want %q", body["site_id"], "local")
	}
	if body["state_root"] == "" {
		t.Error("state_root should not be empty")
	}
}

func TestGetSite_Unknown(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodGet, "/sites/unknown/", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestGetSite_InvalidID(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodGet, "/sites/bad..id/", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// --- GET /sites/{site_id}/healthz ---

func TestSiteHealthz_Valid(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodGet, "/sites/local/healthz", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["site_id"] != "local" {
		t.Errorf("site_id = %q, want %q", body["site_id"], "local")
	}
}

func TestSiteHealthz_UnknownSite(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodGet, "/sites/nope/healthz", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// --- POST /sites/{site_id}/events ---

func TestPostEvent_Valid(t *testing.T) {
	// Redirect state writes to a temp dir so tests are hermetic.
	t.Chdir(t.TempDir())

	router := newRouter(t)
	payload := []byte(`{"event":"test","value":1}`)
	rr := do(t, router, http.MethodPost, "/sites/local/events", payload)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["site_id"] != "local" {
		t.Errorf("site_id = %q, want local", body["site_id"])
	}
	filename := body["file"]
	if filename == "" {
		t.Fatal("file field should not be empty")
	}

	// Verify the file was written with the correct content.
	written, err := os.ReadFile(filepath.Join("var", "state", "local", "events", filename))
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if !bytes.Equal(written, payload) {
		t.Errorf("file content = %s, want %s", written, payload)
	}
}

func TestPostEvent_InvalidJSON(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodPost, "/sites/local/events", []byte("not json"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestPostEvent_UnknownSite(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodPost, "/sites/unknown/events", []byte(`{}`))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// --- GET /sites/{site_id}/events ---

func TestListEvents_Empty(t *testing.T) {
	t.Chdir(t.TempDir())

	rr := do(t, newRouter(t), http.MethodGet, "/sites/local/events", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	events, ok := body["events"].([]any)
	if !ok {
		t.Fatalf("events field missing or wrong type: %v", body)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestListEvents_AfterPost(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	// Write two events.
	do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"n":1}`))
	do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"n":2}`))

	rr := do(t, router, http.MethodGet, "/sites/local/events", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	events := body["events"].([]any)
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}
}

// --- GET /sites/{site_id}/events/{filename} ---

func TestGetEvent_Valid(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	payload := []byte(`{"key":"val"}`)
	postRR := do(t, router, http.MethodPost, "/sites/local/events", payload)
	if postRR.Code != http.StatusCreated {
		t.Fatalf("post failed: %d", postRR.Code)
	}

	var postBody map[string]string
	json.Unmarshal(postRR.Body.Bytes(), &postBody) //nolint:errcheck
	filename := postBody["file"]

	getRR := do(t, router, http.MethodGet, "/sites/local/events/"+filename, nil)
	if getRR.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", getRR.Code, getRR.Body.String())
	}
	if !bytes.Equal(getRR.Body.Bytes(), payload) {
		t.Errorf("body = %s, want %s", getRR.Body.Bytes(), payload)
	}
}

func TestGetEvent_NotFound(t *testing.T) {
	t.Chdir(t.TempDir())

	rr := do(t, newRouter(t), http.MethodGet, "/sites/local/events/9999999.json", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestGetEvent_InvalidFilename(t *testing.T) {
	// filename with ".." should be rejected.
	rr := do(t, newRouter(t), http.MethodGet, "/sites/local/events/..%2Fetc%2Fpasswd", nil)
	if rr.Code != http.StatusBadRequest && rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 400 or 404", rr.Code)
	}
}

// --- Site isolation: events written for one site must not appear for another ---

func TestSiteIsolation(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	// Write event for "local".
	do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"site":"local"}`))

	// List events for "staging" — should be empty.
	rr := do(t, router, http.MethodGet, "/sites/staging/events", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	json.Unmarshal(rr.Body.Bytes(), &body) //nolint:errcheck
	events := body["events"].([]any)
	if len(events) != 0 {
		t.Errorf("staging should have 0 events, got %d", len(events))
	}
}
