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
	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
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
	stores := storage.NewRegistry()
	t.Cleanup(func() { stores.CloseAll() })
	return chihttp.NewRouter(makeTestRegistry(t), stores)
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
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %q, want ok", body["status"])
	}
}

func TestReadyz(t *testing.T) {
	rr := do(t, newRouter(t), http.MethodGet, "/healthz/ready", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
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
