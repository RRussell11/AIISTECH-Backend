package http_test

import (
	"bytes"
	"encoding/json"
	"expvar"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chihttp "github.com/RRussell11/AIISTECH-Backend/internal/http"
	"github.com/RRussell11/AIISTECH-Backend/internal/site"
	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
	"github.com/RRussell11/AIISTECH-Backend/internal/version"
)

// makeTestRegistry returns an AtomicRegistry loaded from a temp file with local+staging.
func makeTestRegistry(t *testing.T) *site.AtomicRegistry {
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
	return site.NewAtomicRegistry(reg)
}

func newRouter(t *testing.T) http.Handler {
	t.Helper()
	stores := storage.NewRegistry()
	t.Cleanup(func() { stores.CloseAll() })
	return chihttp.NewRouter(makeTestRegistry(t), stores, nil)
}

// newRouterWithLogLevel returns a router wired with the provided *slog.LevelVar
// so that the /debug/log-level endpoints are fully functional in tests.
func newRouterWithLogLevel(t *testing.T, lv *slog.LevelVar) http.Handler {
	t.Helper()
	stores := storage.NewRegistry()
	t.Cleanup(func() { stores.CloseAll() })
	return chihttp.NewRouter(makeTestRegistry(t), stores, nil, chihttp.OpsConfig{LogLevel: lv})
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

// --- GET /version ---

func TestVersion(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodGet, "/version", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// All three fields must be present.
	for _, field := range []string{"version", "commit", "build_time"} {
		if _, ok := body[field]; !ok {
			t.Errorf("missing field %q in /version response", field)
		}
	}
	// Default values match the package-level vars (no ldflags in tests).
	if got, want := body["version"], version.Version; got != want {
		t.Errorf("version = %q, want %q", got, want)
	}
	if got, want := body["commit"], version.Commit; got != want {
		t.Errorf("commit = %q, want %q", got, want)
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
	// bbolt DB files are written to var/state/<site_id>/data.db relative to CWD.
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

	// Verify the event can be retrieved via the API.
	getRR := do(t, router, http.MethodGet, "/sites/local/events/"+filename, nil)
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET event status = %d, want 200; body: %s", getRR.Code, getRR.Body.String())
	}
	if !bytes.Equal(getRR.Body.Bytes(), payload) {
		t.Errorf("retrieved event = %s, want %s", getRR.Body.Bytes(), payload)
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

// --- Audit ---

func TestListAudit_Empty(t *testing.T) {
	t.Chdir(t.TempDir())
	rr := do(t, newRouter(t), http.MethodGet, "/sites/local/audit", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	entries, ok := body["entries"].([]any)
	if !ok {
		t.Fatalf("entries field missing or wrong type: %v", body)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestAuditAutoWritten(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"x":1}`))

	rr := do(t, router, http.MethodGet, "/sites/local/audit", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	entries := body["entries"].([]any)
	if len(entries) != 1 {
		t.Errorf("expected 1 audit entry after POST event, got %d", len(entries))
	}
}

func TestAuditEntryContent(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"x":1}`))

	listRR := do(t, router, http.MethodGet, "/sites/local/audit", nil)
	var listBody map[string]any
	json.Unmarshal(listRR.Body.Bytes(), &listBody) //nolint:errcheck
	entries := listBody["entries"].([]any)
	if len(entries) == 0 {
		t.Fatal("expected at least 1 audit entry")
	}
	filename := entries[0].(string)

	getRR := do(t, router, http.MethodGet, "/sites/local/audit/"+filename, nil)
	if getRR.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", getRR.Code)
	}
	var entry map[string]any
	if err := json.Unmarshal(getRR.Body.Bytes(), &entry); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if entry["site_id"] != "local" {
		t.Errorf("site_id = %v, want local", entry["site_id"])
	}
	if entry["method"] != http.MethodPost {
		t.Errorf("method = %v, want POST", entry["method"])
	}
	if entry["status"] == nil {
		t.Error("status field should be present")
	}
}

func TestGetAudit_NotFound(t *testing.T) {
	t.Chdir(t.TempDir())
	rr := do(t, newRouter(t), http.MethodGet, "/sites/local/audit/9999999.json", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestGetAudit_InvalidFilename(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodGet, "/sites/local/audit/..%2Fetc%2Fpasswd", nil)
	if rr.Code != http.StatusBadRequest && rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 400 or 404", rr.Code)
	}
}

func TestAuditSiteIsolation(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	// Write event for "local" → generates audit entry for "local".
	do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"site":"local"}`))

	// Audit entries for "staging" must be empty.
	rr := do(t, router, http.MethodGet, "/sites/staging/audit", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	json.Unmarshal(rr.Body.Bytes(), &body) //nolint:errcheck
	entries := body["entries"].([]any)
	if len(entries) != 0 {
		t.Errorf("staging should have 0 audit entries, got %d", len(entries))
	}
}

// --- POST /sites/{site_id}/artifacts ---

func TestPostArtifact_Valid(t *testing.T) {
	t.Chdir(t.TempDir())
	payload := []byte(`{"build":"1.0.0","hash":"abc123"}`)
	rr := do(t, newRouter(t), http.MethodPost, "/sites/local/artifacts", payload)
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
	if body["file"] == "" {
		t.Error("file field should not be empty")
	}
}

func TestPostArtifact_InvalidJSON(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodPost, "/sites/local/artifacts", []byte("not json"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestPostArtifact_UnknownSite(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodPost, "/sites/unknown/artifacts", []byte(`{}`))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// --- GET /sites/{site_id}/artifacts ---

func TestListArtifacts_Empty(t *testing.T) {
	t.Chdir(t.TempDir())
	rr := do(t, newRouter(t), http.MethodGet, "/sites/local/artifacts", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	artifacts, ok := body["artifacts"].([]any)
	if !ok {
		t.Fatalf("artifacts field missing or wrong type: %v", body)
	}
	if len(artifacts) != 0 {
		t.Errorf("expected 0 artifacts, got %d", len(artifacts))
	}
}

func TestListArtifacts_AfterPost(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	do(t, router, http.MethodPost, "/sites/local/artifacts", []byte(`{"v":1}`))
	do(t, router, http.MethodPost, "/sites/local/artifacts", []byte(`{"v":2}`))

	rr := do(t, router, http.MethodGet, "/sites/local/artifacts", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	json.Unmarshal(rr.Body.Bytes(), &body) //nolint:errcheck
	artifacts := body["artifacts"].([]any)
	if len(artifacts) != 2 {
		t.Errorf("expected 2 artifacts, got %d", len(artifacts))
	}
}

// --- GET /sites/{site_id}/artifacts/{filename} ---

func TestGetArtifact_Valid(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	payload := []byte(`{"artifact":"data"}`)
	postRR := do(t, router, http.MethodPost, "/sites/local/artifacts", payload)
	if postRR.Code != http.StatusCreated {
		t.Fatalf("post failed: %d", postRR.Code)
	}

	var postBody map[string]string
	json.Unmarshal(postRR.Body.Bytes(), &postBody) //nolint:errcheck
	filename := postBody["file"]

	getRR := do(t, router, http.MethodGet, "/sites/local/artifacts/"+filename, nil)
	if getRR.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", getRR.Code)
	}
	if !bytes.Equal(getRR.Body.Bytes(), payload) {
		t.Errorf("body = %s, want %s", getRR.Body.Bytes(), payload)
	}
}

func TestGetArtifact_NotFound(t *testing.T) {
	t.Chdir(t.TempDir())
	rr := do(t, newRouter(t), http.MethodGet, "/sites/local/artifacts/9999999.json", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestGetArtifact_InvalidFilename(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodGet, "/sites/local/artifacts/..%2Fetc%2Fpasswd", nil)
	if rr.Code != http.StatusBadRequest && rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 400 or 404", rr.Code)
	}
}

// --- DELETE /sites/{site_id}/artifacts/{filename} ---

func TestDeleteArtifact_Valid(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	postRR := do(t, router, http.MethodPost, "/sites/local/artifacts", []byte(`{"v":1}`))
	if postRR.Code != http.StatusCreated {
		t.Fatalf("post failed: %d", postRR.Code)
	}
	var postBody map[string]string
	json.Unmarshal(postRR.Body.Bytes(), &postBody) //nolint:errcheck
	filename := postBody["file"]

	delRR := do(t, router, http.MethodDelete, "/sites/local/artifacts/"+filename, nil)
	if delRR.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", delRR.Code, delRR.Body.String())
	}

	// Verify the file is gone.
	getRR := do(t, router, http.MethodGet, "/sites/local/artifacts/"+filename, nil)
	if getRR.Code != http.StatusNotFound {
		t.Errorf("after delete: status = %d, want 404", getRR.Code)
	}
}

func TestDeleteArtifact_NotFound(t *testing.T) {
	t.Chdir(t.TempDir())
	rr := do(t, newRouter(t), http.MethodDelete, "/sites/local/artifacts/9999999.json", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// --- DELETE /sites/{site_id}/events/{filename} ---

func TestDeleteEvent_Valid(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	postRR := do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"action":"test"}`))
	if postRR.Code != http.StatusCreated {
		t.Fatalf("post event failed: %d", postRR.Code)
	}
	var postBody map[string]string
	json.Unmarshal(postRR.Body.Bytes(), &postBody) //nolint:errcheck
	filename := postBody["file"]

	delRR := do(t, router, http.MethodDelete, "/sites/local/events/"+filename, nil)
	if delRR.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body: %s", delRR.Code, delRR.Body.String())
	}

	// Verify the event is gone.
	getRR := do(t, router, http.MethodGet, "/sites/local/events/"+filename, nil)
	if getRR.Code != http.StatusNotFound {
		t.Errorf("after delete: status = %d, want 404", getRR.Code)
	}
}

func TestDeleteEvent_NotFound(t *testing.T) {
	t.Chdir(t.TempDir())
	rr := do(t, newRouter(t), http.MethodDelete, "/sites/local/events/9999999.json", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// --- GET /sites/{site_id}/config ---

func TestGetConfig_NoFile(t *testing.T) {
	t.Chdir(t.TempDir()) // no config files present
	rr := do(t, newRouter(t), http.MethodGet, "/sites/local/config", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["site_id"] != "local" {
		t.Errorf("site_id = %v, want local", body["site_id"])
	}
	if body["settings"] == nil {
		t.Error("settings field should be present")
	}
}

func TestGetConfig_WithFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Create contracts/sites/local/config.yaml inside the temp CWD.
	configDir := filepath.Join(dir, "contracts", "sites", "local")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(configDir, "config.yaml"),
		[]byte("site_id: local\nsettings:\n  env: test\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rr := do(t, newRouter(t), http.MethodGet, "/sites/local/config", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	settings, ok := body["settings"].(map[string]any)
	if !ok {
		t.Fatalf("settings is not a map: %v", body)
	}
	if settings["env"] != "test" {
		t.Errorf("settings.env = %v, want test", settings["env"])
	}
}

// --- Observability ---

func TestLivez(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodGet, "/healthz/live", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %q, want ok", body["status"])
	}
	if body["uptime_seconds"] == nil {
		t.Error("uptime_seconds field should be present")
	}
}

func TestReadyz(t *testing.T) {
	t.Chdir(t.TempDir())
	rr := do(t, newRouter(t), http.MethodGet, "/healthz/ready", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
	if body["sites"] == nil {
		t.Error("sites field should be present")
	}
	if body["stores"] == nil {
		t.Error("stores field should be present")
	}
	// Verify each site in the test registry reports "ok" store status.
	storeMap, ok := body["stores"].(map[string]any)
	if !ok {
		t.Fatalf("stores field is not a map: %T", body["stores"])
	}
	for siteID, v := range storeMap {
		if v != "ok" {
			t.Errorf("stores[%q] = %v, want ok", siteID, v)
		}
	}
}

func TestReadyz_AllSitesReported(t *testing.T) {
	t.Chdir(t.TempDir())
	rr := do(t, newRouter(t), http.MethodGet, "/healthz/ready", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The test registry has "local" and "staging" — both must appear in stores.
	storeMap := body["stores"].(map[string]any)
	for _, want := range []string{"local", "staging"} {
		if storeMap[want] == nil {
			t.Errorf("stores map missing entry for site %q", want)
		}
	}
}

func TestMetrics(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodGet, "/metrics", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["requests_total"]; !ok {
		t.Error("requests_total counter should be present in /metrics")
	}
	if _, ok := body["requests_by_site"]; !ok {
		t.Error("requests_by_site counter should be present in /metrics")
	}
}

// --- Authentication ---

// newRouterWithKey sets up a CWD that contains a config file for the "local"
// site with the given API key, then returns a router anchored to that CWD.
// Callers must have already called t.Chdir(t.TempDir()) or equivalent.
func newRouterWithKey(t *testing.T, siteID, apiKey string) http.Handler {
	t.Helper()
	configDir := filepath.Join("contracts", "sites", siteID)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	content := "site_id: " + siteID + "\napi_key: " + apiKey + "\nsettings:\n  env: test\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return newRouter(t)
}

func TestAuth_MutatingRequest_NoKey_SiteWithoutAuth(t *testing.T) {
	// "local" has no api_key in temp CWD → auth disabled → POST must succeed
	t.Chdir(t.TempDir())
	rr := do(t, newRouter(t), http.MethodPost, "/sites/local/events", []byte(`{"x":1}`))
	if rr.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201 (no auth configured)", rr.Code)
	}
}

func TestAuth_MutatingRequest_MissingHeader_SiteWithAuth(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouterWithKey(t, "local", "super-secret")
	rr := do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"x":1}`))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 when Authorization header is absent", rr.Code)
	}
}

func TestAuth_MutatingRequest_WrongKey(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouterWithKey(t, "local", "super-secret")

	req, _ := http.NewRequest(http.MethodPost, "/sites/local/events", bytes.NewReader([]byte(`{"x":1}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong-key")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for wrong key", rr.Code)
	}
}

func TestAuth_MutatingRequest_CorrectKey(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouterWithKey(t, "local", "super-secret")

	req, _ := http.NewRequest(http.MethodPost, "/sites/local/events", bytes.NewReader([]byte(`{"x":1}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer super-secret")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201 with correct Bearer key", rr.Code)
	}
}

func TestAuth_ReadOnlyRequest_NoKey_SiteWithAuth(t *testing.T) {
	// GET requests must not require auth even when the site has an api_key.
	t.Chdir(t.TempDir())
	router := newRouterWithKey(t, "local", "super-secret")
	rr := do(t, router, http.MethodGet, "/sites/local/events", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (GET is open regardless of api_key)", rr.Code)
	}
}

func TestAuth_WWWAuthenticateHeader(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouterWithKey(t, "local", "super-secret")
	rr := do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"x":1}`))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if rr.Header().Get("WWW-Authenticate") == "" {
		t.Error("WWW-Authenticate header should be present on 401 response")
	}
}

func TestAuth_DeleteRequiresKey(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouterWithKey(t, "local", "super-secret")

	// First create an artifact with the correct key.
	postReq, _ := http.NewRequest(http.MethodPost, "/sites/local/artifacts", bytes.NewReader([]byte(`{"v":1}`)))
	postReq.Header.Set("Content-Type", "application/json")
	postReq.Header.Set("Authorization", "Bearer super-secret")
	postRR := httptest.NewRecorder()
	router.ServeHTTP(postRR, postReq)
	if postRR.Code != http.StatusCreated {
		t.Fatalf("post status = %d, want 201", postRR.Code)
	}
	var postBody map[string]string
	json.Unmarshal(postRR.Body.Bytes(), &postBody) //nolint:errcheck
	filename := postBody["file"]

	// Now DELETE without a key — must be rejected.
	rr := do(t, router, http.MethodDelete, "/sites/local/artifacts/"+filename, nil)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("DELETE without key: status = %d, want 401", rr.Code)
	}
}

// --- Pagination ---

// postN posts n events to /sites/local/events and returns the keys in insertion order.
func postN(t *testing.T, router http.Handler, n int) []string {
	t.Helper()
	keys := make([]string, 0, n)
	for i := 0; i < n; i++ {
		rr := do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"i":1}`))
		if rr.Code != http.StatusCreated {
			t.Fatalf("post %d: status=%d", i, rr.Code)
		}
		var body map[string]string
		json.Unmarshal(rr.Body.Bytes(), &body) //nolint:errcheck
		keys = append(keys, body["file"])
	}
	return keys
}

func listEventsPage(t *testing.T, router http.Handler, cursor, limit string) map[string]any {
	t.Helper()
	url := "/sites/local/events"
	sep := "?"
	if cursor != "" {
		url += sep + "cursor=" + cursor
		sep = "&"
	}
	if limit != "" {
		url += sep + "limit=" + limit
	}
	rr := do(t, router, http.MethodGet, url, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s: status=%d body=%s", url, rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func TestPagination_DefaultLimit_ReturnsAllWhenFewItems(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)
	postN(t, router, 3)

	body := listEventsPage(t, router, "", "")
	events := body["events"].([]any)
	if len(events) != 3 {
		t.Errorf("events len = %d, want 3", len(events))
	}
	if nc := body["next_cursor"].(string); nc != "" {
		t.Errorf("next_cursor = %q, want empty (all items fit in one page)", nc)
	}
}

func TestPagination_LimitParam_ConstrainsPageSize(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)
	postN(t, router, 5)

	body := listEventsPage(t, router, "", "2")
	events := body["events"].([]any)
	if len(events) != 2 {
		t.Errorf("page size = %d, want 2", len(events))
	}
	nc := body["next_cursor"].(string)
	if nc == "" {
		t.Errorf("next_cursor should be non-empty when more items exist")
	}
}

func TestPagination_CursorAdvancesToNextPage(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)
	postN(t, router, 5)

	// Page 1: limit=2
	p1 := listEventsPage(t, router, "", "2")
	p1Events := p1["events"].([]any)
	if len(p1Events) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(p1Events))
	}
	cursor := p1["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("page1 next_cursor is empty, expected a cursor for more pages")
	}

	// Page 2: limit=2 starting after cursor
	p2 := listEventsPage(t, router, cursor, "2")
	p2Events := p2["events"].([]any)
	if len(p2Events) != 2 {
		t.Fatalf("page2 len = %d, want 2", len(p2Events))
	}
	// No overlap between pages.
	p1Set := make(map[any]bool)
	for _, k := range p1Events {
		p1Set[k] = true
	}
	for _, k := range p2Events {
		if p1Set[k] {
			t.Errorf("key %v appears on both page 1 and page 2", k)
		}
	}
}

func TestPagination_LastPageHasEmptyNextCursor(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)
	postN(t, router, 4)

	// Page 1: limit=3
	p1 := listEventsPage(t, router, "", "3")
	cursor := p1["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("page1 next_cursor is empty")
	}

	// Page 2 should return the remaining 1 item with an empty next_cursor.
	p2 := listEventsPage(t, router, cursor, "3")
	p2Events := p2["events"].([]any)
	if len(p2Events) != 1 {
		t.Errorf("page2 len = %d, want 1", len(p2Events))
	}
	if nc := p2["next_cursor"].(string); nc != "" {
		t.Errorf("last page next_cursor = %q, want empty", nc)
	}
}

func TestPagination_InvalidLimit_Returns400(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	for _, bad := range []string{"0", "-1", "abc", "1.5"} {
		rr := do(t, router, http.MethodGet, "/sites/local/events?limit="+bad, nil)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("limit=%q: status=%d, want 400", bad, rr.Code)
		}
	}
}

func TestPagination_MaxLimitClamped(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)
	postN(t, router, 3)

	// limit=99999 should be clamped but not error; all 3 items returned.
	body := listEventsPage(t, router, "", "99999")
	events := body["events"].([]any)
	if len(events) != 3 {
		t.Errorf("events len = %d, want 3 (limit clamped to max)", len(events))
	}
}

func TestPagination_ArtifactsAndAuditSupportCursor(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	// Create 3 artifacts.
	for i := 0; i < 3; i++ {
		do(t, router, http.MethodPost, "/sites/local/artifacts", []byte(`{"n":1}`))
	}

	// Artifacts: first page of 2.
	rr := do(t, router, http.MethodGet, "/sites/local/artifacts?limit=2", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("artifacts list: status=%d", rr.Code)
	}
	var artBody map[string]any
	json.Unmarshal(rr.Body.Bytes(), &artBody) //nolint:errcheck
	if _, hasNC := artBody["next_cursor"]; !hasNC {
		t.Error("artifacts response missing next_cursor field")
	}
	arts := artBody["artifacts"].([]any)
	if len(arts) != 2 {
		t.Errorf("artifacts page len = %d, want 2", len(arts))
	}

	// Audit: at least 3 entries written by the 3 POST requests above;
	// fetch page of 2 and check for next_cursor.
	rrA := do(t, router, http.MethodGet, "/sites/local/audit?limit=2", nil)
	if rrA.Code != http.StatusOK {
		t.Fatalf("audit list: status=%d", rrA.Code)
	}
	var auditBody map[string]any
	json.Unmarshal(rrA.Body.Bytes(), &auditBody) //nolint:errcheck
	if _, hasNC := auditBody["next_cursor"]; !hasNC {
		t.Error("audit response missing next_cursor field")
	}
	entries := auditBody["entries"].([]any)
	if len(entries) != 2 {
		t.Errorf("audit page len = %d, want 2", len(entries))
	}
}

// --- Segment 19: Tenant-scoped storage namespacing ---

// doTenant is like do but always includes the tenant credentials for the "acme"
// tenant configured by newTenantRouter.
func doTenant(t *testing.T, router http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
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
	req.Header.Set("X-Tenant-ID", "acme")
	req.Header.Set("Authorization", "Bearer acme-secret")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// TestTenantStorage_PostListGetEvent verifies the full event lifecycle in tenant
// mode: write, list (bare key returned), get by bare key.
func TestTenantStorage_PostListGetEvent(t *testing.T) {
	router := newTenantRouter(t)

	// POST an event as tenant acme.
	postRR := doTenant(t, router, http.MethodPost, "/sites/local/events", []byte(`{"tenant":"acme"}`))
	if postRR.Code != http.StatusCreated {
		t.Fatalf("POST event: status=%d body=%s", postRR.Code, postRR.Body.String())
	}
	var postBody map[string]string
	json.Unmarshal(postRR.Body.Bytes(), &postBody) //nolint:errcheck
	file := postBody["file"]
	if file == "" {
		t.Fatal("POST event response missing 'file' field")
	}
	// The bare file key returned must not contain a slash (tenant prefix stripped).
	if strings.Contains(file, "/") {
		t.Errorf("POST event 'file' key %q should not contain slash", file)
	}

	// LIST events — must return the same bare key.
	listRR := doTenant(t, router, http.MethodGet, "/sites/local/events", nil)
	if listRR.Code != http.StatusOK {
		t.Fatalf("LIST events: status=%d", listRR.Code)
	}
	var listBody map[string]any
	json.Unmarshal(listRR.Body.Bytes(), &listBody) //nolint:errcheck
	events := listBody["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(events), events)
	}
	if events[0].(string) != file {
		t.Errorf("list key = %q, want %q", events[0], file)
	}

	// GET event by the bare key.
	getRR := doTenant(t, router, http.MethodGet, "/sites/local/events/"+file, nil)
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET event: status=%d body=%s", getRR.Code, getRR.Body.String())
	}
}

// TestTenantStorage_TenantIsolation verifies that tenant acme cannot see globex's events.
func TestTenantStorage_TenantIsolation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfgDir := filepath.Join(dir, "contracts", "sites", "local")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(`site_id: local
tenants:
  - tenant_id: acme
    api_key: "acme-secret"
  - tenant_id: globex
    api_key: "globex-secret"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	stores := storage.NewRegistry()
	t.Cleanup(func() { stores.CloseAll() })
	router := chihttp.NewRouter(makeTestRegistry(t), stores, nil)

	// Write an event as acme.
	acmeReq, _ := http.NewRequest(http.MethodPost, "/sites/local/events", bytes.NewReader([]byte(`{"owner":"acme"}`)))
	acmeReq.Header.Set("Content-Type", "application/json")
	acmeReq.Header.Set("X-Tenant-ID", "acme")
	acmeReq.Header.Set("Authorization", "Bearer acme-secret")
	acmePost := httptest.NewRecorder()
	router.ServeHTTP(acmePost, acmeReq)
	if acmePost.Code != http.StatusCreated {
		t.Fatalf("acme POST: status=%d", acmePost.Code)
	}

	// List events as globex — must see 0 events (isolation enforced by key prefix).
	globexReq, _ := http.NewRequest(http.MethodGet, "/sites/local/events", nil)
	globexReq.Header.Set("X-Tenant-ID", "globex")
	globexReq.Header.Set("Authorization", "Bearer globex-secret")
	globexList := httptest.NewRecorder()
	router.ServeHTTP(globexList, globexReq)
	if globexList.Code != http.StatusOK {
		t.Fatalf("globex LIST: status=%d", globexList.Code)
	}
	var body map[string]any
	json.Unmarshal(globexList.Body.Bytes(), &body) //nolint:errcheck
	events := body["events"].([]any)
	if len(events) != 0 {
		t.Errorf("globex should see 0 acme events, got %d: %v", len(events), events)
	}
}

// TestTenantStorage_GetNonexistentReturns404 verifies that GET for a bare key that
// does not exist in the tenant's namespace returns 404.
func TestTenantStorage_GetNonexistentReturns404(t *testing.T) {
	router := newTenantRouter(t)
	rr := doTenant(t, router, http.MethodGet, "/sites/local/events/9999999999999999999.json", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// --- Segment 20: Event/artifact schema validation ---

// newSchemaRouter creates a router for a site that has event_schema and
// artifact_schema configured with required fields.
func newSchemaRouter(t *testing.T) http.Handler {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)

	cfgDir := filepath.Join(dir, "contracts", "sites", "local")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(`site_id: local
event_schema:
  required:
    - type
    - source
artifact_schema:
  required:
    - name
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	stores := storage.NewRegistry()
	t.Cleanup(func() { stores.CloseAll() })
	return chihttp.NewRouter(makeTestRegistry(t), stores, nil)
}

// TestSchemaValidation_EventMissingFields verifies 422 when required event fields are absent.
func TestSchemaValidation_EventMissingFields(t *testing.T) {
	router := newSchemaRouter(t)
	rr := do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"other":"value"}`))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
	var body map[string]any
	json.Unmarshal(rr.Body.Bytes(), &body) //nolint:errcheck
	if body["error"] != "schema validation failed" {
		t.Errorf("error = %q, want %q", body["error"], "schema validation failed")
	}
	missing, ok := body["missing_fields"].([]any)
	if !ok || len(missing) == 0 {
		t.Error("expected non-empty missing_fields")
	}
}

// TestSchemaValidation_EventPartialMissing verifies that only truly missing fields are reported.
func TestSchemaValidation_EventPartialMissing(t *testing.T) {
	router := newSchemaRouter(t)
	// Provide "type" but not "source"
	rr := do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"type":"test"}`))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
	var body map[string]any
	json.Unmarshal(rr.Body.Bytes(), &body) //nolint:errcheck
	missing := body["missing_fields"].([]any)
	if len(missing) != 1 || missing[0].(string) != "source" {
		t.Errorf("missing_fields = %v, want [source]", missing)
	}
}

// TestSchemaValidation_EventAllFieldsPresent verifies 201 when all required fields are provided.
func TestSchemaValidation_EventAllFieldsPresent(t *testing.T) {
	router := newSchemaRouter(t)
	rr := do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"type":"audit","source":"backend","extra":"ok"}`))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}
}

// TestSchemaValidation_ArtifactMissingName verifies 422 when artifact required field absent.
func TestSchemaValidation_ArtifactMissingName(t *testing.T) {
	router := newSchemaRouter(t)
	rr := do(t, router, http.MethodPost, "/sites/local/artifacts", []byte(`{"version":"1.0"}`))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
	var body map[string]any
	json.Unmarshal(rr.Body.Bytes(), &body) //nolint:errcheck
	if body["error"] != "schema validation failed" {
		t.Errorf("error = %q, want %q", body["error"], "schema validation failed")
	}
}

// TestSchemaValidation_NoSchemaConfigured verifies no validation happens without config.
func TestSchemaValidation_NoSchemaConfigured(t *testing.T) {
	// newRouter uses no config file, so EventSchema is nil.
	router := newRouter(t)
	rr := do(t, router, http.MethodPost, "/sites/local/events", []byte(`{}`))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (no schema configured)", rr.Code)
	}
}

// ---- Metrics (expvar) tests ----

// expvarMapInt reads the int64 value for key from the named expvar.Map.
// Returns 0 when the map or key has not yet been created.
func expvarMapInt(mapName, key string) int64 {
	m, _ := expvar.Get(mapName).(*expvar.Map)
	if m == nil {
		return 0
	}
	v, _ := m.Get(key).(*expvar.Int)
	if v == nil {
		return 0
	}
	return v.Value()
}

// TestMetrics_EventWriteIncrementsCounter verifies that a successful POST /events
// increments events_written_by_site for the target site by exactly 1.
func TestMetrics_EventWriteIncrementsCounter(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	before := expvarMapInt("events_written_by_site", "local")
	rr := do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"x":1}`))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rr.Code)
	}
	after := expvarMapInt("events_written_by_site", "local")
	if after-before != 1 {
		t.Errorf("events_written_by_site[local] delta = %d, want 1", after-before)
	}
}

// TestMetrics_ArtifactWriteIncrementsCounter verifies that a successful POST /artifacts
// increments artifacts_written_by_site for the target site by exactly 1.
func TestMetrics_ArtifactWriteIncrementsCounter(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	before := expvarMapInt("artifacts_written_by_site", "local")
	rr := do(t, router, http.MethodPost, "/sites/local/artifacts", []byte(`{"k":"v"}`))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rr.Code)
	}
	after := expvarMapInt("artifacts_written_by_site", "local")
	if after-before != 1 {
		t.Errorf("artifacts_written_by_site[local] delta = %d, want 1", after-before)
	}
}

// TestMetrics_EventWriteCounterNotIncrementedOnError verifies that a rejected
// request (e.g. invalid JSON) does not increment the write counter.
func TestMetrics_EventWriteCounterNotIncrementedOnError(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	before := expvarMapInt("events_written_by_site", "local")
	rr := do(t, router, http.MethodPost, "/sites/local/events", []byte(`not-json`))
	if rr.Code == http.StatusCreated {
		t.Fatal("expected non-201 for invalid JSON body")
	}
	after := expvarMapInt("events_written_by_site", "local")
	if after != before {
		t.Errorf("events_written_by_site[local] changed on error (before=%d after=%d)", before, after)
	}
}

// TestMetrics_SitesTrackedSeparately verifies that writes to different sites
// increment independent counter slots.
func TestMetrics_SitesTrackedSeparately(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	beforeLocal := expvarMapInt("events_written_by_site", "local")
	beforeStaging := expvarMapInt("events_written_by_site", "staging")

	do(t, router, http.MethodPost, "/sites/local/events", []byte(`{"a":1}`))
	do(t, router, http.MethodPost, "/sites/staging/events", []byte(`{"b":2}`))
	do(t, router, http.MethodPost, "/sites/staging/events", []byte(`{"c":3}`))

	if delta := expvarMapInt("events_written_by_site", "local") - beforeLocal; delta != 1 {
		t.Errorf("local delta = %d, want 1", delta)
	}
	if delta := expvarMapInt("events_written_by_site", "staging") - beforeStaging; delta != 2 {
		t.Errorf("staging delta = %d, want 2", delta)
	}
}

// ---- Log-level toggle tests ----

// TestLogLevel_GetUnmanaged verifies that GET /debug/log-level returns INFO and
// managed=false when no LogLevel was wired (the default newRouter helper).
func TestLogLevel_GetUnmanaged(t *testing.T) {
	router := newRouter(t)
	rr := do(t, router, http.MethodGet, "/debug/log-level", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["level"] != "INFO" {
		t.Errorf("level = %q, want %q", body["level"], "INFO")
	}
	if body["managed"] != false {
		t.Errorf("managed = %v, want false", body["managed"])
	}
}

// TestLogLevel_PutUnmanaged verifies that PUT /debug/log-level returns 501 when
// no LogLevel was wired.
func TestLogLevel_PutUnmanaged(t *testing.T) {
	router := newRouter(t)
	rr := do(t, router, http.MethodPut, "/debug/log-level", []byte(`{"level":"DEBUG"}`))
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rr.Code)
	}
}

// TestLogLevel_GetManaged verifies that GET /debug/log-level reflects the current
// level when a LogLevel var is wired.
func TestLogLevel_GetManaged(t *testing.T) {
	lv := new(slog.LevelVar) // defaults to INFO
	router := newRouterWithLogLevel(t, lv)

	rr := do(t, router, http.MethodGet, "/debug/log-level", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["level"] != "INFO" {
		t.Errorf("level = %q, want %q", body["level"], "INFO")
	}
	if body["managed"] != true {
		t.Errorf("managed = %v, want true", body["managed"])
	}
}

// TestLogLevel_PutChangesLevel verifies that PUT /debug/log-level updates the
// slog.LevelVar and returns the new level in the response.
func TestLogLevel_PutChangesLevel(t *testing.T) {
	lv := new(slog.LevelVar) // defaults to INFO
	router := newRouterWithLogLevel(t, lv)

	rr := do(t, router, http.MethodPut, "/debug/log-level", []byte(`{"level":"DEBUG"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	// LevelVar must be updated.
	if lv.Level() != slog.LevelDebug {
		t.Errorf("lv.Level() = %v, want DEBUG", lv.Level())
	}

	// Response body must reflect the new level.
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["level"] != "DEBUG" {
		t.Errorf("level = %q, want %q", body["level"], "DEBUG")
	}

	// Subsequent GET must also show the updated level.
	rr2 := do(t, router, http.MethodGet, "/debug/log-level", nil)
	var body2 map[string]any
	if err := json.Unmarshal(rr2.Body.Bytes(), &body2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body2["level"] != "DEBUG" {
		t.Errorf("GET after PUT: level = %q, want %q", body2["level"], "DEBUG")
	}
}

// TestLogLevel_PutInvalidLevel verifies that an unknown level name returns 400.
func TestLogLevel_PutInvalidLevel(t *testing.T) {
	lv := new(slog.LevelVar)
	router := newRouterWithLogLevel(t, lv)

	rr := do(t, router, http.MethodPut, "/debug/log-level", []byte(`{"level":"VERBOSE"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	// LevelVar must be unchanged.
	if lv.Level() != slog.LevelInfo {
		t.Errorf("lv.Level() mutated on bad request: got %v, want INFO", lv.Level())
	}
}

// TestLogLevel_PutCaseInsensitive verifies that level names are case-insensitive.
func TestLogLevel_PutCaseInsensitive(t *testing.T) {
	lv := new(slog.LevelVar)
	router := newRouterWithLogLevel(t, lv)

	rr := do(t, router, http.MethodPut, "/debug/log-level", []byte(`{"level":"warn"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if lv.Level() != slog.LevelWarn {
		t.Errorf("lv.Level() = %v, want WARN", lv.Level())
	}
}

// TestLogLevel_PutInvalidJSON verifies that malformed JSON in PUT body returns 400.
func TestLogLevel_PutInvalidJSON(t *testing.T) {
	lv := new(slog.LevelVar)
	router := newRouterWithLogLevel(t, lv)

	rr := do(t, router, http.MethodPut, "/debug/log-level", []byte(`not-json`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

// ---- SIGHUP hot-reload tests ----

// TestHotReload_NewSiteBecomesAccessible verifies that after an AtomicRegistry swap
// (simulating a SIGHUP reload), requests for the newly added site are served
// while requests for a removed site return 404.
func TestHotReload_NewSiteBecomesAccessible(t *testing.T) {
	t.Chdir(t.TempDir())

	// Build an initial AtomicRegistry with only "local".
	dir := t.TempDir()
	p := filepath.Join(dir, "sites.yaml")
	writeYAML := func(content string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("writeYAML: %v", err)
		}
	}
	writeYAML(`
default_site_id: local
sites:
  - site_id: local
`)
	reg1, err := site.LoadRegistry(p)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	ar := site.NewAtomicRegistry(reg1)

	stores := storage.NewRegistry()
	t.Cleanup(func() { stores.CloseAll() })
	router := chihttp.NewRouter(ar, stores, nil)

	// Before swap: "staging" is unknown → 404.
	rr := do(t, router, http.MethodGet, "/sites/staging/healthz", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("before swap: status = %d, want 404", rr.Code)
	}

	// Simulate SIGHUP reload by swapping in a registry that includes "staging".
	writeYAML(`
default_site_id: local
sites:
  - site_id: local
  - site_id: staging
`)
	reg2, err := site.LoadRegistry(p)
	if err != nil {
		t.Fatalf("LoadRegistry after update: %v", err)
	}
	ar.Store(reg2)

	// After swap: "staging" is now known → 200.
	rr2 := do(t, router, http.MethodGet, "/sites/staging/healthz", nil)
	if rr2.Code != http.StatusOK {
		t.Fatalf("after swap: status = %d, want 200", rr2.Code)
	}
}

// TestHotReload_ListSitesReflectsNewRegistry verifies that GET /sites reflects
// the latest registry after a hot-swap.
func TestHotReload_ListSitesReflectsNewRegistry(t *testing.T) {
	t.Chdir(t.TempDir())

	dir := t.TempDir()
	p := filepath.Join(dir, "sites.yaml")

	if err := os.WriteFile(p, []byte(`
default_site_id: local
sites:
  - site_id: local
`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	reg1, err := site.LoadRegistry(p)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	ar := site.NewAtomicRegistry(reg1)

	stores := storage.NewRegistry()
	t.Cleanup(func() { stores.CloseAll() })
	router := chihttp.NewRouter(ar, stores, nil)

	// Before swap: /sites lists only "local".
	rr := do(t, router, http.MethodGet, "/sites", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /sites status = %d, want 200", rr.Code)
	}
	var body1 struct {
		Sites []string `json:"sites"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body1); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body1.Sites) != 1 {
		t.Errorf("before swap: site count = %d, want 1", len(body1.Sites))
	}

	// Swap in a two-site registry.
	if err := os.WriteFile(p, []byte(`
default_site_id: local
sites:
  - site_id: local
  - site_id: staging
`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	reg2, err := site.LoadRegistry(p)
	if err != nil {
		t.Fatalf("LoadRegistry after update: %v", err)
	}
	ar.Store(reg2)

	rr2 := do(t, router, http.MethodGet, "/sites", nil)
	var body2 struct {
		Sites []string `json:"sites"`
	}
	if err := json.Unmarshal(rr2.Body.Bytes(), &body2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body2.Sites) != 2 {
		t.Errorf("after swap: site count = %d, want 2", len(body2.Sites))
	}
}

// --- Subscription management ---

func TestSubscriptions_CreateAndList(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	payload := []byte(`{"url":"https://receiver.example.com/hook","service":"svc","events":["audit.write"]}`)
	rr := do(t, router, http.MethodPost, "/sites/local/webhooks/subscriptions", payload)
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}

	var created map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created["id"] == "" || created["id"] == nil {
		t.Error("create response missing id")
	}
	// enabled should default to true
	if created["enabled"] != true {
		t.Errorf("enabled = %v, want true", created["enabled"])
	}

	// List should return the subscription.
	rr2 := do(t, router, http.MethodGet, "/sites/local/webhooks/subscriptions", nil)
	if rr2.Code != http.StatusOK {
		t.Fatalf("GET list status = %d, want 200", rr2.Code)
	}
	var listBody map[string]any
	if err := json.Unmarshal(rr2.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	subs, ok := listBody["subscriptions"].([]any)
	if !ok {
		t.Fatalf("subscriptions field not an array: %T", listBody["subscriptions"])
	}
	if len(subs) != 1 {
		t.Fatalf("subscriptions count = %d, want 1", len(subs))
	}
	if _, hasNC := listBody["next_cursor"]; !hasNC {
		t.Error("list response missing next_cursor field")
	}
}

func TestSubscriptions_Create_MissingURL_Returns400(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodPost, "/sites/local/webhooks/subscriptions",
		[]byte(`{"service":"svc","events":["audit.write"]}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
}

func TestSubscriptions_Create_MissingService_Returns400(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodPost, "/sites/local/webhooks/subscriptions",
		[]byte(`{"url":"https://example.com/hook","events":["audit.write"]}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
}

func TestSubscriptions_Create_EmptyEvents_Returns400(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodPost, "/sites/local/webhooks/subscriptions",
		[]byte(`{"url":"https://example.com/hook","service":"svc","events":[]}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
}

func TestSubscriptions_Create_InvalidJSON_Returns400(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodPost, "/sites/local/webhooks/subscriptions",
		[]byte("not json"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
}

func TestSubscriptions_Create_ExplicitlyDisabled(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	payload := []byte(`{"url":"https://example.com/hook","service":"svc","events":["e"],"enabled":false}`)
	rr := do(t, router, http.MethodPost, "/sites/local/webhooks/subscriptions", payload)
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}
	var created map[string]any
	json.Unmarshal(rr.Body.Bytes(), &created) //nolint:errcheck
	if created["enabled"] != false {
		t.Errorf("enabled = %v, want false", created["enabled"])
	}
}

func TestSubscriptions_GetByID(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	// Create one subscription.
	rr := do(t, router, http.MethodPost, "/sites/local/webhooks/subscriptions",
		[]byte(`{"url":"https://example.com/hook","service":"svc","events":["e"]}`))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d", rr.Code)
	}
	var created map[string]any
	json.Unmarshal(rr.Body.Bytes(), &created) //nolint:errcheck
	id := created["id"].(string)

	// GET by ID should return it.
	rr2 := do(t, router, http.MethodGet, "/sites/local/webhooks/subscriptions/"+id, nil)
	if rr2.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body: %s", rr2.Code, rr2.Body.String())
	}
	var got map[string]any
	json.Unmarshal(rr2.Body.Bytes(), &got) //nolint:errcheck
	if got["id"] != id {
		t.Errorf("GET id = %v, want %q", got["id"], id)
	}
}

func TestSubscriptions_Get_NotFound(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodGet, "/sites/local/webhooks/subscriptions/nonexistent.json", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestSubscriptions_Delete_Existing(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	// Create and then delete.
	rr := do(t, router, http.MethodPost, "/sites/local/webhooks/subscriptions",
		[]byte(`{"url":"https://example.com/hook","service":"svc","events":["e"]}`))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d", rr.Code)
	}
	var created map[string]any
	json.Unmarshal(rr.Body.Bytes(), &created) //nolint:errcheck
	id := created["id"].(string)

	rr2 := do(t, router, http.MethodDelete, "/sites/local/webhooks/subscriptions/"+id, nil)
	if rr2.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204; body: %s", rr2.Code, rr2.Body.String())
	}

	// Subsequent GET should return 404.
	rr3 := do(t, router, http.MethodGet, "/sites/local/webhooks/subscriptions/"+id, nil)
	if rr3.Code != http.StatusNotFound {
		t.Fatalf("GET after delete status = %d, want 404", rr3.Code)
	}
}

func TestSubscriptions_Delete_NotFound(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodDelete, "/sites/local/webhooks/subscriptions/nonexistent.json", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestSubscriptions_ListPagination(t *testing.T) {
	t.Chdir(t.TempDir())
	router := newRouter(t)

	// Create 3 subscriptions.
	for i := range 3 {
		payload, _ := json.Marshal(map[string]any{
			"url":     "https://example.com/hook",
			"service": "svc",
			"events":  []string{"e"},
		})
		rr := do(t, router, http.MethodPost, "/sites/local/webhooks/subscriptions", payload)
		if rr.Code != http.StatusCreated {
			t.Fatalf("create #%d status = %d", i, rr.Code)
		}
	}

	// Page 1: limit=2.
	rr := do(t, router, http.MethodGet, "/sites/local/webhooks/subscriptions?limit=2", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("page1 status = %d", rr.Code)
	}
	var p1 map[string]any
	json.Unmarshal(rr.Body.Bytes(), &p1) //nolint:errcheck
	if len(p1["subscriptions"].([]any)) != 2 {
		t.Fatalf("page1 count = %d, want 2", len(p1["subscriptions"].([]any)))
	}
	cursor := p1["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("page1 next_cursor is empty, expected more pages")
	}

	// Page 2: should have 1 entry with empty next_cursor.
	rr2 := do(t, router, http.MethodGet, "/sites/local/webhooks/subscriptions?limit=2&cursor="+cursor, nil)
	if rr2.Code != http.StatusOK {
		t.Fatalf("page2 status = %d", rr2.Code)
	}
	var p2 map[string]any
	json.Unmarshal(rr2.Body.Bytes(), &p2) //nolint:errcheck
	if len(p2["subscriptions"].([]any)) != 1 {
		t.Fatalf("page2 count = %d, want 1", len(p2["subscriptions"].([]any)))
	}
	if p2["next_cursor"].(string) != "" {
		t.Errorf("page2 next_cursor = %q, want empty", p2["next_cursor"])
	}
}

// --- PATCH /webhooks/subscriptions/{id} ---

func TestSubscriptions_Update_PartialPatch(t *testing.T) {
t.Chdir(t.TempDir())
router := newRouter(t)

// Create a subscription.
createPayload := []byte(`{"url":"https://old.example.com/hook","service":"svc","events":["audit.write"],"enabled":true}`)
rr := do(t, router, http.MethodPost, "/sites/local/webhooks/subscriptions", createPayload)
if rr.Code != http.StatusCreated {
t.Fatalf("create status = %d", rr.Code)
}
var created map[string]any
json.Unmarshal(rr.Body.Bytes(), &created) //nolint:errcheck
id := created["id"].(string)

// PATCH: change URL only.
patchPayload := []byte(`{"url":"https://new.example.com/hook"}`)
rr2 := do(t, router, http.MethodPatch, "/sites/local/webhooks/subscriptions/"+id, patchPayload)
if rr2.Code != http.StatusOK {
t.Fatalf("PATCH status = %d, want 200; body: %s", rr2.Code, rr2.Body.String())
}

var updated map[string]any
json.Unmarshal(rr2.Body.Bytes(), &updated) //nolint:errcheck

if updated["url"] != "https://new.example.com/hook" {
t.Errorf("url = %v, want new URL", updated["url"])
}
// Other fields must be unchanged.
if updated["service"] != "svc" {
t.Errorf("service = %v, want svc", updated["service"])
}
if updated["enabled"] != true {
t.Errorf("enabled = %v, want true", updated["enabled"])
}
// ID preserved.
if updated["id"] != id {
t.Errorf("id changed: got %v, want %q", updated["id"], id)
}
}

func TestSubscriptions_Update_EnableDisable(t *testing.T) {
t.Chdir(t.TempDir())
router := newRouter(t)

rr := do(t, router, http.MethodPost, "/sites/local/webhooks/subscriptions",
[]byte(`{"url":"https://example.com/hook","service":"svc","events":["e"],"enabled":true}`))
if rr.Code != http.StatusCreated {
t.Fatalf("create status = %d", rr.Code)
}
var created map[string]any
json.Unmarshal(rr.Body.Bytes(), &created) //nolint:errcheck
id := created["id"].(string)

// Disable.
rr2 := do(t, router, http.MethodPatch, "/sites/local/webhooks/subscriptions/"+id,
[]byte(`{"enabled":false}`))
if rr2.Code != http.StatusOK {
t.Fatalf("PATCH disable status = %d; body: %s", rr2.Code, rr2.Body.String())
}
var patched map[string]any
json.Unmarshal(rr2.Body.Bytes(), &patched) //nolint:errcheck
if patched["enabled"] != false {
t.Errorf("enabled = %v, want false", patched["enabled"])
}
}

func TestSubscriptions_Update_ReplaceEvents(t *testing.T) {
t.Chdir(t.TempDir())
router := newRouter(t)

rr := do(t, router, http.MethodPost, "/sites/local/webhooks/subscriptions",
[]byte(`{"url":"https://example.com/hook","service":"svc","events":["audit.write"]}`))
if rr.Code != http.StatusCreated {
t.Fatalf("create status = %d", rr.Code)
}
var created map[string]any
json.Unmarshal(rr.Body.Bytes(), &created) //nolint:errcheck
id := created["id"].(string)

rr2 := do(t, router, http.MethodPatch, "/sites/local/webhooks/subscriptions/"+id,
[]byte(`{"events":["artifact.write","event.write"]}`))
if rr2.Code != http.StatusOK {
t.Fatalf("PATCH events status = %d; body: %s", rr2.Code, rr2.Body.String())
}
var updated map[string]any
json.Unmarshal(rr2.Body.Bytes(), &updated) //nolint:errcheck
events, _ := updated["events"].([]any)
if len(events) != 2 {
t.Errorf("events len = %d, want 2", len(events))
}
}

func TestSubscriptions_Update_NotFound(t *testing.T) {
rr := do(t, newRouter(t), http.MethodPatch, "/sites/local/webhooks/subscriptions/nonexistent.json",
[]byte(`{"url":"https://x.example.com/hook"}`))
if rr.Code != http.StatusNotFound {
t.Fatalf("status = %d, want 404", rr.Code)
}
}

func TestSubscriptions_Update_InvalidJSON(t *testing.T) {
rr := do(t, newRouter(t), http.MethodPatch, "/sites/local/webhooks/subscriptions/any.json",
[]byte("not json"))
if rr.Code != http.StatusBadRequest {
t.Fatalf("status = %d, want 400", rr.Code)
}
}

func TestSubscriptions_Update_Persisted(t *testing.T) {
t.Chdir(t.TempDir())
router := newRouter(t)

rr := do(t, router, http.MethodPost, "/sites/local/webhooks/subscriptions",
[]byte(`{"url":"https://old.example.com/hook","service":"svc","events":["e"]}`))
if rr.Code != http.StatusCreated {
t.Fatalf("create status = %d", rr.Code)
}
var created map[string]any
json.Unmarshal(rr.Body.Bytes(), &created) //nolint:errcheck
id := created["id"].(string)

do(t, router, http.MethodPatch, "/sites/local/webhooks/subscriptions/"+id, //nolint:errcheck
[]byte(`{"url":"https://updated.example.com/hook"}`))

// Confirm via GET.
rr3 := do(t, router, http.MethodGet, "/sites/local/webhooks/subscriptions/"+id, nil)
if rr3.Code != http.StatusOK {
t.Fatalf("GET after PATCH status = %d", rr3.Code)
}
var got map[string]any
json.Unmarshal(rr3.Body.Bytes(), &got) //nolint:errcheck
if got["url"] != "https://updated.example.com/hook" {
t.Errorf("GET after PATCH URL = %v, want updated URL", got["url"])
}
}
