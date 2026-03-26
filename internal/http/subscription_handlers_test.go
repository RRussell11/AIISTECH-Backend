package http_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	chihttp "github.com/RRussell11/AIISTECH-Backend/internal/http"
	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

// --- test helpers ---

// openTestStoreProvider opens a fresh bbolt-backed StoreProvider for testing.
func openTestStoreProvider(t *testing.T) *webhooks.StoreProvider {
	t.Helper()
	s, err := storage.Open(filepath.Join(t.TempDir(), "subs.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return webhooks.NewStoreProvider(s)
}

// seedSubscription creates a subscription in the store and returns it.
func seedSubscription(t *testing.T, sp *webhooks.StoreProvider, id, service, url string) webhooks.Subscription {
	t.Helper()
	now := time.Now().UTC()
	sub := webhooks.Subscription{
		ID:        id,
		Service:   service,
		URL:       url,
		Enabled:   true,
		Events:    []string{"audit.write"},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := sp.Create(&sub); err != nil {
		t.Fatalf("seedSubscription Create: %v", err)
	}
	return sub
}

// newSubRouter builds a router with only the StoreProvider wired (other optional
// params nil).
func newSubRouter(t *testing.T, sp *webhooks.StoreProvider) http.Handler {
	t.Helper()
	stores := storage.NewRegistry()
	t.Cleanup(func() { stores.CloseAll() })
	return chihttp.NewRouter(makeTestRegistry(t), stores, nil, nil, nil, sp, "")
}

// --- GET /webhooks/subscriptions/ ---

func TestListSubscriptionsHandler_Empty(t *testing.T) {
	sp := openTestStoreProvider(t)
	rr := do(t, newSubRouter(t, sp), http.MethodGet, "/webhooks/subscriptions/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	subs := body["subscriptions"].([]any)
	if len(subs) != 0 {
		t.Errorf("subscriptions = %d, want 0", len(subs))
	}
	if body["next_cursor"] != "" {
		t.Errorf("next_cursor = %q, want empty", body["next_cursor"])
	}
}

func TestListSubscriptionsHandler_WithRecords(t *testing.T) {
	sp := openTestStoreProvider(t)
	seedSubscription(t, sp, "sub-1", "svc", "https://a.example.com/hook")
	seedSubscription(t, sp, "sub-2", "svc", "https://b.example.com/hook")

	rr := do(t, newSubRouter(t, sp), http.MethodGet, "/webhooks/subscriptions/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	subs := body["subscriptions"].([]any)
	if len(subs) != 2 {
		t.Errorf("subscriptions = %d, want 2", len(subs))
	}
}

// --- POST /webhooks/subscriptions/ ---

func TestCreateSubscriptionHandler_OK(t *testing.T) {
	sp := openTestStoreProvider(t)
	router := newSubRouter(t, sp)

	body := map[string]any{
		"service": "aiistech-backend",
		"url":     "https://example.com/hook",
		"enabled": true,
		"events":  []string{"audit.write"},
	}
	b, _ := json.Marshal(body)
	rr := do(t, router, http.MethodPost, "/webhooks/subscriptions/", b)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rr.Code, rr.Body.String())
	}
	var got webhooks.Subscription
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.URL != "https://example.com/hook" {
		t.Errorf("URL = %q, want %q", got.URL, "https://example.com/hook")
	}
	if got.Service != "aiistech-backend" {
		t.Errorf("Service = %q, want %q", got.Service, "aiistech-backend")
	}
}

func TestCreateSubscriptionHandler_MissingURL(t *testing.T) {
	sp := openTestStoreProvider(t)
	body := map[string]any{"service": "svc"}
	b, _ := json.Marshal(body)
	rr := do(t, newSubRouter(t, sp), http.MethodPost, "/webhooks/subscriptions/", b)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestCreateSubscriptionHandler_MissingService(t *testing.T) {
	sp := openTestStoreProvider(t)
	body := map[string]any{"url": "https://example.com/hook"}
	b, _ := json.Marshal(body)
	rr := do(t, newSubRouter(t, sp), http.MethodPost, "/webhooks/subscriptions/", b)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestCreateSubscriptionHandler_InvalidJSON(t *testing.T) {
	sp := openTestStoreProvider(t)
	rr := do(t, newSubRouter(t, sp), http.MethodPost, "/webhooks/subscriptions/", []byte("not json"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

// --- GET /webhooks/subscriptions/{id} ---

func TestGetSubscriptionHandler_Found(t *testing.T) {
	sp := openTestStoreProvider(t)
	sub := seedSubscription(t, sp, "sub-get", "svc", "https://example.com/hook")

	rr := do(t, newSubRouter(t, sp), http.MethodGet, "/webhooks/subscriptions/sub-get", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	var got webhooks.Subscription
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != sub.ID {
		t.Errorf("ID = %q, want %q", got.ID, sub.ID)
	}
	if got.URL != sub.URL {
		t.Errorf("URL = %q, want %q", got.URL, sub.URL)
	}
}

func TestGetSubscriptionHandler_NotFound(t *testing.T) {
	sp := openTestStoreProvider(t)
	rr := do(t, newSubRouter(t, sp), http.MethodGet, "/webhooks/subscriptions/nonexistent", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// --- PATCH /webhooks/subscriptions/{id} ---

func TestPatchSubscriptionHandler_URL(t *testing.T) {
	sp := openTestStoreProvider(t)
	sub := seedSubscription(t, sp, "sub-patch", "svc", "https://old.example.com/hook")
	router := newSubRouter(t, sp)

	patch := map[string]any{"url": "https://new.example.com/hook"}
	pb, _ := json.Marshal(patch)
	rr := do(t, router, http.MethodPatch, "/webhooks/subscriptions/sub-patch", pb)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	var got webhooks.Subscription
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.URL != "https://new.example.com/hook" {
		t.Errorf("URL = %q, want %q", got.URL, "https://new.example.com/hook")
	}
	if got.ID != sub.ID {
		t.Errorf("ID changed: got %q, want %q", got.ID, sub.ID)
	}
}

func TestPatchSubscriptionHandler_Events(t *testing.T) {
	sp := openTestStoreProvider(t)
	seedSubscription(t, sp, "sub-events", "svc", "https://example.com/hook")

	patch := map[string]any{"events": []string{"event.a", "event.b"}}
	pb, _ := json.Marshal(patch)
	rr := do(t, newSubRouter(t, sp), http.MethodPatch, "/webhooks/subscriptions/sub-events", pb)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	var got webhooks.Subscription
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Events) != 2 || got.Events[0] != "event.a" {
		t.Errorf("events = %v, want [event.a event.b]", got.Events)
	}
}

func TestPatchSubscriptionHandler_Enabled(t *testing.T) {
	sp := openTestStoreProvider(t)
	seedSubscription(t, sp, "sub-disable", "svc", "https://example.com/hook")

	patch := map[string]any{"enabled": false}
	pb, _ := json.Marshal(patch)
	rr := do(t, newSubRouter(t, sp), http.MethodPatch, "/webhooks/subscriptions/sub-disable", pb)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got webhooks.Subscription
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Enabled {
		t.Error("expected Enabled=false after PATCH")
	}
}

func TestPatchSubscriptionHandler_NotFound(t *testing.T) {
	sp := openTestStoreProvider(t)
	patch := map[string]any{"url": "https://example.com/hook"}
	pb, _ := json.Marshal(patch)
	rr := do(t, newSubRouter(t, sp), http.MethodPatch, "/webhooks/subscriptions/nonexistent", pb)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestPatchSubscriptionHandler_InvalidJSON(t *testing.T) {
	sp := openTestStoreProvider(t)
	seedSubscription(t, sp, "sub-badjson", "svc", "https://example.com/hook")
	rr := do(t, newSubRouter(t, sp), http.MethodPatch, "/webhooks/subscriptions/sub-badjson",
		[]byte("not json"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

// --- DELETE /webhooks/subscriptions/{id} ---

func TestDeleteSubscriptionHandler_OK(t *testing.T) {
	sp := openTestStoreProvider(t)
	seedSubscription(t, sp, "sub-delete", "svc", "https://example.com/hook")
	router := newSubRouter(t, sp)

	rr := do(t, router, http.MethodDelete, "/webhooks/subscriptions/sub-delete", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body: %s)", rr.Code, rr.Body.String())
	}

	// Verify gone.
	rr2 := do(t, router, http.MethodGet, "/webhooks/subscriptions/sub-delete", nil)
	if rr2.Code != http.StatusNotFound {
		t.Errorf("after delete, GET status = %d, want 404", rr2.Code)
	}
}

func TestDeleteSubscriptionHandler_NotFound(t *testing.T) {
	sp := openTestStoreProvider(t)
	rr := do(t, newSubRouter(t, sp), http.MethodDelete, "/webhooks/subscriptions/nonexistent", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// --- Routes not mounted when storeProvider is nil ---

func TestSubscriptionRoutes_NotMountedWhenNil(t *testing.T) {
	router := newRouter(t) // nil storeProvider
	rr := do(t, router, http.MethodGet, "/webhooks/subscriptions/", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("subscription route without store: status = %d, want 404", rr.Code)
	}
}

// --- Default enabled on create ---

func TestCreateSubscriptionHandler_DefaultEnabled(t *testing.T) {
	sp := openTestStoreProvider(t)
	body := map[string]any{
		"service": "svc",
		"url":     "https://example.com/hook",
		// enabled not specified → should default to true
	}
	b, _ := json.Marshal(body)
	rr := do(t, newSubRouter(t, sp), http.MethodPost, "/webhooks/subscriptions/", b)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rr.Code)
	}
	var got webhooks.Subscription
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Enabled {
		t.Error("expected Enabled=true when not specified in create request")
	}
}

// --- Pagination on list ---

func TestListSubscriptionsHandler_Pagination(t *testing.T) {
	sp := openTestStoreProvider(t)
	for i := range 5 {
		seedSubscription(t, sp, fmt.Sprintf("sub-pg-%d", i), "svc", "https://example.com/hook")
	}
	router := newSubRouter(t, sp)

	rr := do(t, router, http.MethodGet, "/webhooks/subscriptions/?limit=3", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	subs := body["subscriptions"].([]any)
	if len(subs) != 3 {
		t.Errorf("page1 = %d subscriptions, want 3", len(subs))
	}
	cursor, _ := body["next_cursor"].(string)
	if cursor == "" {
		t.Error("expected non-empty next_cursor after first page")
	}

	// Second page.
	rr2 := do(t, router, http.MethodGet, "/webhooks/subscriptions/?limit=3&cursor="+cursor, nil)
	if rr2.Code != http.StatusOK {
		t.Fatalf("page2 status = %d, want 200", rr2.Code)
	}
	var body2 map[string]any
	if err := json.Unmarshal(rr2.Body.Bytes(), &body2); err != nil {
		t.Fatalf("decode page2: %v", err)
	}
	subs2 := body2["subscriptions"].([]any)
	if len(subs2) != 2 {
		t.Errorf("page2 = %d subscriptions, want 2", len(subs2))
	}
	cursor2, _ := body2["next_cursor"].(string)
	if cursor2 != "" {
		t.Errorf("expected empty next_cursor after last page, got %q", cursor2)
	}
}
