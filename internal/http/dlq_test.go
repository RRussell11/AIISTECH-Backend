package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	chihttp "github.com/RRussell11/AIISTECH-Backend/internal/http"
	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

// newDLQRouter creates a router with a real StoreDLQSink and returns the router
// plus the underlying stores so tests can pre-populate the DLQ bucket.
func newDLQRouter(t *testing.T, replayClient *http.Client) (http.Handler, *storage.Registry) {
	t.Helper()
	stores := storage.NewRegistry()
	t.Cleanup(func() { stores.CloseAll() })
	dlq := webhooks.NewStoreDLQSink(stores)
	ops := chihttp.OpsConfig{
		DLQ:          dlq,
		ReplayClient: replayClient,
	}
	return chihttp.NewRouter(makeTestRegistry(t), stores, nil, ops), stores
}

// seedDLQRecord writes one DLQRecord into the site store and returns its key.
func seedDLQRecord(t *testing.T, stores *storage.Registry, siteID string, rec webhooks.DLQRecord) string {
	t.Helper()
	sink := webhooks.NewStoreDLQSink(stores)
	if err := sink.Store(rec); err != nil {
		t.Fatalf("seeding DLQ record: %v", err)
	}
	store, err := stores.Open(siteID)
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	keys, err := store.List(webhooks.DLQBucket)
	if err != nil || len(keys) == 0 {
		t.Fatalf("expected at least one DLQ key; err=%v keys=%v", err, keys)
	}
	return keys[len(keys)-1] // most recently written key
}

// --- ListDLQHandler ---

func TestDLQ_ListEmpty(t *testing.T) {
	t.Chdir(t.TempDir())
	router, _ := newDLQRouter(t, nil)

	rr := do(t, router, http.MethodGet, "/sites/local/webhooks/dlq", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	entries, ok := body["entries"].([]any)
	if !ok {
		t.Fatalf("entries field missing or wrong type; body=%s", rr.Body.String())
	}
	if len(entries) != 0 {
		t.Errorf("expected empty entries, got %d", len(entries))
	}
}

func TestDLQ_ListReturnsEntries(t *testing.T) {
	t.Chdir(t.TempDir())
	router, stores := newDLQRouter(t, nil)

	seedDLQRecord(t, stores, "local", webhooks.DLQRecord{
		SiteID:    "local",
		EventID:   "evt-1",
		EventType: "audit.write",
		Payload:   []byte(`{"type":"audit.write"}`),
	})

	rr := do(t, router, http.MethodGet, "/sites/local/webhooks/dlq", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	entries := body["entries"].([]any)
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
}

// --- GetDLQHandler ---

func TestDLQ_GetNotFound(t *testing.T) {
	t.Chdir(t.TempDir())
	router, _ := newDLQRouter(t, nil)

	rr := do(t, router, http.MethodGet, "/sites/local/webhooks/dlq/nonexistent.json", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestDLQ_GetReturnsRecord(t *testing.T) {
	t.Chdir(t.TempDir())
	router, stores := newDLQRouter(t, nil)

	key := seedDLQRecord(t, stores, "local", webhooks.DLQRecord{
		SiteID:    "local",
		EventID:   "evt-get",
		EventType: "audit.write",
		Payload:   []byte(`{}`),
	})

	rr := do(t, router, http.MethodGet, "/sites/local/webhooks/dlq/"+key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var rec webhooks.DLQRecord
	if err := json.Unmarshal(rr.Body.Bytes(), &rec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rec.EventID != "evt-get" {
		t.Errorf("EventID = %q, want %q", rec.EventID, "evt-get")
	}
}

// --- DeleteDLQHandler ---

func TestDLQ_DeleteNotFound(t *testing.T) {
	t.Chdir(t.TempDir())
	router, _ := newDLQRouter(t, nil)

	rr := do(t, router, http.MethodDelete, "/sites/local/webhooks/dlq/missing.json", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestDLQ_DeleteRemovesEntry(t *testing.T) {
	t.Chdir(t.TempDir())
	router, stores := newDLQRouter(t, nil)

	key := seedDLQRecord(t, stores, "local", webhooks.DLQRecord{
		SiteID:  "local",
		Payload: []byte(`{}`),
	})

	rr := do(t, router, http.MethodDelete, "/sites/local/webhooks/dlq/"+key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}

	// Verify entry is gone.
	rr2 := do(t, router, http.MethodGet, "/sites/local/webhooks/dlq/"+key, nil)
	if rr2.Code != http.StatusNotFound {
		t.Errorf("after delete: status = %d, want 404", rr2.Code)
	}
}

// --- ReplayDLQHandler ---

func TestDLQ_ReplayNotFound(t *testing.T) {
	t.Chdir(t.TempDir())
	router, _ := newDLQRouter(t, nil)

	rr := do(t, router, http.MethodPost, "/sites/local/webhooks/dlq/missing.json/replay", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestDLQ_ReplaySuccess(t *testing.T) {
	t.Chdir(t.TempDir())

	// Stand up a mock receiver that accepts the replayed payload.
	var receivedCount int
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	router, stores := newDLQRouter(t, receiver.Client())

	key := seedDLQRecord(t, stores, "local", webhooks.DLQRecord{
		SiteID:          "local",
		SubscriptionURL: receiver.URL,
		Payload:         []byte(`{"event":"audit.write"}`),
	})

	rr := do(t, router, http.MethodPost, "/sites/local/webhooks/dlq/"+key+"/replay", nil)
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
	if body["entry_deleted"] != true {
		t.Errorf("entry_deleted = %v, want true", body["entry_deleted"])
	}
	if receivedCount != 1 {
		t.Errorf("receiver called %d times, want 1", receivedCount)
	}

	// Entry should be gone after successful replay.
	rr2 := do(t, router, http.MethodGet, "/sites/local/webhooks/dlq/"+key, nil)
	if rr2.Code != http.StatusNotFound {
		t.Errorf("after replay: status = %d, want 404", rr2.Code)
	}
}

func TestDLQ_ReplayReceiverFailure(t *testing.T) {
	t.Chdir(t.TempDir())

	// Receiver returns 500.
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer receiver.Close()

	router, stores := newDLQRouter(t, receiver.Client())

	key := seedDLQRecord(t, stores, "local", webhooks.DLQRecord{
		SiteID:          "local",
		SubscriptionURL: receiver.URL,
		Payload:         []byte(`{}`),
	})

	rr := do(t, router, http.MethodPost, "/sites/local/webhooks/dlq/"+key+"/replay", nil)
	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rr.Code)
	}

	// Entry must still be present.
	rr2 := do(t, router, http.MethodGet, "/sites/local/webhooks/dlq/"+key, nil)
	if rr2.Code != http.StatusOK {
		t.Errorf("after failed replay: status = %d, want 200 (entry preserved)", rr2.Code)
	}
}

func TestDLQ_ReplaySignsWithSecret(t *testing.T) {
	t.Chdir(t.TempDir())

	var gotSig, gotTS string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Webhook-Signature")
		gotTS = r.Header.Get("X-Webhook-Timestamp")
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	router, stores := newDLQRouter(t, receiver.Client())

	const secret = "replay-secret"
	key := seedDLQRecord(t, stores, "local", webhooks.DLQRecord{
		SiteID:          "local",
		SubscriptionURL: receiver.URL,
		Secret:          secret,
		Payload:         []byte(`{"hello":"world"}`),
	})

	rr := do(t, router, http.MethodPost, "/sites/local/webhooks/dlq/"+key+"/replay", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	if gotSig == "" {
		t.Error("expected X-Webhook-Signature on replay, got empty")
	}
	if gotTS == "" {
		t.Error("expected X-Webhook-Timestamp on replay, got empty")
	}
	// Verify the signature matches what SignatureHeader would produce.
	want := webhooks.SignatureHeader(secret, gotTS, []byte(`{"hello":"world"}`))
	if gotSig != want {
		t.Errorf("X-Webhook-Signature = %q, want %q", gotSig, want)
	}
}
