package http_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	chihttp "github.com/RRussell11/AIISTECH-Backend/internal/http"
	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

// --- DLQ HTTP handler test helpers ---

// openTestDLQStore opens a fresh bbolt DLQStore in a temp directory.
func openTestDLQStore(t *testing.T) *webhooks.DLQStore {
	t.Helper()
	s, err := storage.Open(filepath.Join(t.TempDir(), "dlq.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return webhooks.NewDLQStore(s)
}

// seedDLQRecord saves a DLQ record and returns it with its assigned ID.
func seedDLQRecord(t *testing.T, dlq *webhooks.DLQStore, eventID, subID, subURL string) webhooks.DLQRecord {
	t.Helper()
	rec := &webhooks.DLQRecord{
		SubscriptionID:  subID,
		SubscriptionURL: subURL,
		Event: webhooks.Event{
			ID:        eventID,
			Type:      "audit.write",
			CreatedAt: time.Now().UTC(),
		},
		LastError:      "connection refused",
		FailedAt:       time.Now().UTC(),
		NextRetryAfter: time.Now().UTC().Add(5 * time.Minute),
	}
	if err := dlq.Save(rec); err != nil {
		t.Fatalf("seedDLQRecord Save: %v", err)
	}
	return *rec
}

// successReplayer always succeeds.
type successReplayer struct{}

func (r *successReplayer) ReplayRecord(_ webhooks.DLQRecord) error { return nil }

// failReplayer always fails.
type failReplayer struct{}

func (r *failReplayer) ReplayRecord(_ webhooks.DLQRecord) error {
	return &replayError{msg: "delivery failed: connection refused"}
}

type replayError struct{ msg string }

func (e *replayError) Error() string { return e.msg }

// newDLQRouter builds a router with a DLQ store and replayer for tests.
func newDLQRouter(t *testing.T, dlq *webhooks.DLQStore, replayer webhooks.DLQReplayer) http.Handler {
	t.Helper()
	stores := storage.NewRegistry()
	t.Cleanup(func() { stores.CloseAll() })
	return chihttp.NewRouter(makeTestRegistry(t), stores, nil, dlq, replayer, nil)
}

// --- GET /webhooks/dlq ---

func TestListDLQHandler_Empty(t *testing.T) {
	dlq := openTestDLQStore(t)
	rr := do(t, newDLQRouter(t, dlq, &successReplayer{}), http.MethodGet, "/webhooks/dlq/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	records := body["records"].([]any)
	if len(records) != 0 {
		t.Errorf("records = %d, want 0", len(records))
	}
	if body["next_cursor"] != "" {
		t.Errorf("next_cursor = %q, want empty", body["next_cursor"])
	}
}

func TestListDLQHandler_WithRecords(t *testing.T) {
	dlq := openTestDLQStore(t)
	seedDLQRecord(t, dlq, "e1", "s1", "https://a.example.com/hook")
	seedDLQRecord(t, dlq, "e2", "s2", "https://b.example.com/hook")

	rr := do(t, newDLQRouter(t, dlq, &successReplayer{}), http.MethodGet, "/webhooks/dlq/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	records := body["records"].([]any)
	if len(records) != 2 {
		t.Errorf("records = %d, want 2", len(records))
	}
}

// --- GET /webhooks/dlq/{id} ---

func TestGetDLQHandler_Found(t *testing.T) {
	dlq := openTestDLQStore(t)
	rec := seedDLQRecord(t, dlq, "e-get", "s-get", "https://example.com/hook")

	rr := do(t, newDLQRouter(t, dlq, &successReplayer{}), http.MethodGet, "/webhooks/dlq/"+rec.ID, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	var got webhooks.DLQRecord
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != rec.ID {
		t.Errorf("got.ID = %q, want %q", got.ID, rec.ID)
	}
	if got.Event.ID != "e-get" {
		t.Errorf("got.Event.ID = %q, want %q", got.Event.ID, "e-get")
	}
}

func TestGetDLQHandler_NotFound(t *testing.T) {
	dlq := openTestDLQStore(t)
	rr := do(t, newDLQRouter(t, dlq, &successReplayer{}), http.MethodGet, "/webhooks/dlq/nonexistent.json", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// --- DELETE /webhooks/dlq/{id} ---

func TestDeleteDLQHandler_OK(t *testing.T) {
	dlq := openTestDLQStore(t)
	rec := seedDLQRecord(t, dlq, "e-del", "s-del", "https://example.com/hook")
	router := newDLQRouter(t, dlq, &successReplayer{})

	rr := do(t, router, http.MethodDelete, "/webhooks/dlq/"+rec.ID, nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rr.Code)
	}

	// Verify it's gone.
	rr2 := do(t, router, http.MethodGet, "/webhooks/dlq/"+rec.ID, nil)
	if rr2.Code != http.StatusNotFound {
		t.Errorf("after delete, GET status = %d, want 404", rr2.Code)
	}
}

func TestDeleteDLQHandler_NotFound(t *testing.T) {
	dlq := openTestDLQStore(t)
	rr := do(t, newDLQRouter(t, dlq, &successReplayer{}), http.MethodDelete, "/webhooks/dlq/nonexistent.json", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// --- POST /webhooks/dlq/{id}/replay ---

func TestReplayDLQHandler_Success(t *testing.T) {
	dlq := openTestDLQStore(t)
	rec := seedDLQRecord(t, dlq, "e-replay", "s-replay", "https://example.com/hook")
	router := newDLQRouter(t, dlq, &successReplayer{})

	rr := do(t, router, http.MethodPost, "/webhooks/dlq/"+rec.ID+"/replay", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "delivered" {
		t.Errorf("status = %q, want %q", body["status"], "delivered")
	}

	// Record should be deleted after successful replay.
	rr2 := do(t, router, http.MethodGet, "/webhooks/dlq/"+rec.ID, nil)
	if rr2.Code != http.StatusNotFound {
		t.Errorf("after successful replay, GET status = %d, want 404", rr2.Code)
	}
}

func TestReplayDLQHandler_Failure(t *testing.T) {
	dlq := openTestDLQStore(t)
	rec := seedDLQRecord(t, dlq, "e-replay-fail", "s-replay-fail", "https://example.com/hook")
	router := newDLQRouter(t, dlq, &failReplayer{})

	rr := do(t, router, http.MethodPost, "/webhooks/dlq/"+rec.ID+"/replay", nil)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body: %s)", rr.Code, rr.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "failed" {
		t.Errorf("status = %q, want %q", body["status"], "failed")
	}
	if body["error"] == "" {
		t.Error("error field should be non-empty on failure")
	}

	// Record should still exist with incremented attempts.
	got, err := dlq.Get(rec.ID)
	if err != nil {
		t.Fatalf("DLQ.Get() after failed replay: %v", err)
	}
	if got.Attempts != rec.Attempts+1 {
		t.Errorf("Attempts = %d, want %d", got.Attempts, rec.Attempts+1)
	}
}

func TestReplayDLQHandler_NotFound(t *testing.T) {
	dlq := openTestDLQStore(t)
	rr := do(t, newDLQRouter(t, dlq, &successReplayer{}), http.MethodPost, "/webhooks/dlq/nonexistent.json/replay", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// --- POST /webhooks/dlq/replay-all ---

func TestReplayAllDLQHandler_AllSuccess(t *testing.T) {
	dlq := openTestDLQStore(t)
	seedDLQRecord(t, dlq, "e1", "s1", "https://a.example.com/hook")
	seedDLQRecord(t, dlq, "e2", "s2", "https://b.example.com/hook")
	router := newDLQRouter(t, dlq, &successReplayer{})

	rr := do(t, router, http.MethodPost, "/webhooks/dlq/replay-all", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if int(body["total"].(float64)) != 2 {
		t.Errorf("total = %v, want 2", body["total"])
	}
	if int(body["succeeded"].(float64)) != 2 {
		t.Errorf("succeeded = %v, want 2", body["succeeded"])
	}
	if int(body["failed"].(float64)) != 0 {
		t.Errorf("failed = %v, want 0", body["failed"])
	}

	// All records should be deleted.
	records, _ := dlq.List()
	if len(records) != 0 {
		t.Errorf("DLQ has %d records after replay-all success, want 0", len(records))
	}
}

func TestReplayAllDLQHandler_AllFail(t *testing.T) {
	dlq := openTestDLQStore(t)
	seedDLQRecord(t, dlq, "e1", "s1", "https://a.example.com/hook")
	router := newDLQRouter(t, dlq, &failReplayer{})

	rr := do(t, router, http.MethodPost, "/webhooks/dlq/replay-all", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if int(body["failed"].(float64)) != 1 {
		t.Errorf("failed = %v, want 1", body["failed"])
	}
	if int(body["succeeded"].(float64)) != 0 {
		t.Errorf("succeeded = %v, want 0", body["succeeded"])
	}
}

func TestReplayAllDLQHandler_Empty(t *testing.T) {
	dlq := openTestDLQStore(t)
	rr := do(t, newDLQRouter(t, dlq, &successReplayer{}), http.MethodPost, "/webhooks/dlq/replay-all", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if int(body["total"].(float64)) != 0 {
		t.Errorf("total = %v, want 0", body["total"])
	}
}

// --- DLQ routes not mounted when store is nil ---

func TestDLQRoutes_NotMountedWhenNil(t *testing.T) {
	// router without DLQ (both nil) should return 404 for DLQ routes.
	router := newRouter(t) // uses nil, nil
	rr := do(t, router, http.MethodGet, "/webhooks/dlq/", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("DLQ route without store: status = %d, want 404", rr.Code)
	}
}
