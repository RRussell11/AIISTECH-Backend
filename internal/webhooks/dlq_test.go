package webhooks_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

// openDLQStore opens a fresh bbolt-backed DLQStore for testing.
func openDLQStore(t *testing.T) *webhooks.DLQStore {
	t.Helper()
	s, err := storage.Open(filepath.Join(t.TempDir(), "dlq.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return webhooks.NewDLQStore(s)
}

func newTestDLQRecord(eventID, subID, subURL string) *webhooks.DLQRecord {
	return &webhooks.DLQRecord{
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
}

func newAlwaysFailServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newCountingServer(t *testing.T, calls *int, statusCode int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		w.WriteHeader(statusCode)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- DLQStore tests ---

func TestDLQStore_SaveAndGet(t *testing.T) {
	d := openDLQStore(t)
	rec := newTestDLQRecord("evt-1", "sub-1", "https://example.com/hook")

	if err := d.Save(rec); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if rec.ID == "" {
		t.Fatal("Save() did not set record ID")
	}

	got, err := d.Get(rec.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != rec.ID {
		t.Errorf("Get() ID = %q, want %q", got.ID, rec.ID)
	}
	if got.Event.ID != rec.Event.ID {
		t.Errorf("Get() Event.ID = %q, want %q", got.Event.ID, rec.Event.ID)
	}
	if got.SubscriptionURL != rec.SubscriptionURL {
		t.Errorf("Get() SubscriptionURL = %q, want %q", got.SubscriptionURL, rec.SubscriptionURL)
	}
}

func TestDLQStore_Save_PreservesExistingID(t *testing.T) {
	d := openDLQStore(t)
	rec := newTestDLQRecord("evt-2", "sub-2", "https://example.com/hook")
	rec.ID = "explicit-id.json"

	if err := d.Save(rec); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if rec.ID != "explicit-id.json" {
		t.Errorf("Save() changed ID to %q, want %q", rec.ID, "explicit-id.json")
	}

	got, err := d.Get("explicit-id.json")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != "explicit-id.json" {
		t.Errorf("Get() ID = %q, want explicit-id.json", got.ID)
	}
}

func TestDLQStore_Delete(t *testing.T) {
	d := openDLQStore(t)
	rec := newTestDLQRecord("evt-3", "sub-3", "https://example.com/hook")
	if err := d.Save(rec); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if err := d.Delete(rec.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, err := d.Get(rec.ID)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Get() after Delete: want ErrNotFound, got %v", err)
	}
}

func TestDLQStore_Delete_NotFound(t *testing.T) {
	d := openDLQStore(t)
	if err := d.Delete("nonexistent.json"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Delete() nonexistent: want ErrNotFound, got %v", err)
	}
}

func TestDLQStore_Get_NotFound(t *testing.T) {
	d := openDLQStore(t)
	_, err := d.Get("nonexistent.json")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Get() nonexistent: want ErrNotFound, got %v", err)
	}
}

func TestDLQStore_List(t *testing.T) {
	d := openDLQStore(t)

	for i := range 3 {
		rec := newTestDLQRecord("evt-list-"+fmt.Sprintf("%d", i), "sub-"+fmt.Sprintf("%d", i), "https://example.com/hook")
		if err := d.Save(rec); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	records, err := d.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 3 {
		t.Errorf("List() = %d records, want 3", len(records))
	}
}

func TestDLQStore_List_Empty(t *testing.T) {
	d := openDLQStore(t)
	records, err := d.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 0 {
		t.Errorf("List() on empty store = %d records, want 0", len(records))
	}
}

func TestDLQStore_ListPage(t *testing.T) {
	d := openDLQStore(t)

	for i := range 5 {
		rec := newTestDLQRecord("evt-page-"+fmt.Sprintf("%d", i), "sub-"+fmt.Sprintf("%d", i), "https://example.com/hook")
		rec.ID = "" // let Save assign a key
		if err := d.Save(rec); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	page1, cursor, err := d.ListPage("", 3)
	if err != nil {
		t.Fatalf("ListPage(page1) error = %v", err)
	}
	if len(page1) != 3 {
		t.Errorf("page1 = %d records, want 3", len(page1))
	}
	if cursor == "" {
		t.Error("expected non-empty cursor after first page")
	}

	page2, cursor2, err := d.ListPage(cursor, 3)
	if err != nil {
		t.Fatalf("ListPage(page2) error = %v", err)
	}
	if len(page2) != 2 {
		t.Errorf("page2 = %d records, want 2", len(page2))
	}
	if cursor2 != "" {
		t.Errorf("expected empty cursor after last page, got %q", cursor2)
	}
}

func TestDLQRecord_IsTerminal(t *testing.T) {
	rec := &webhooks.DLQRecord{Attempts: 0}
	if rec.IsTerminal(10) {
		t.Error("IsTerminal(10) with Attempts=0 should be false")
	}

	rec.Attempts = 10
	if !rec.IsTerminal(10) {
		t.Error("IsTerminal(10) with Attempts=10 should be true")
	}

	rec.Attempts = 5
	if rec.IsTerminal(0) {
		t.Error("IsTerminal(0) (disabled) should always return false")
	}
}

// --- WorkerDispatcher DLQ integration tests ---

func TestWorkerDispatcher_StoresToDLQOnExhaustion(t *testing.T) {
	receiver := newAlwaysFailServer(t)

	dlqStore := openDLQStore(t)

	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-dlq", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		ServiceName:    "test",
		MaxAttempts:    2,
		WorkerCount:    1,
		TimeoutSeconds: 5,
		RetryBackoff:   noBackoff,
		DLQStore:       dlqStore,
		DLQCoolingOff:  5 * time.Minute,
	}, provider)

	if err := d.Dispatch(context.Background(), webhooks.Event{ID: "evt-dlq", Type: "audit.write"}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	records, err := dlqStore.List()
	if err != nil {
		t.Fatalf("DLQ.List(): %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("DLQ has %d records, want 1", len(records))
	}
	if records[0].Event.ID != "evt-dlq" {
		t.Errorf("DLQ record Event.ID = %q, want %q", records[0].Event.ID, "evt-dlq")
	}
	if records[0].SubscriptionID != "sub-dlq" {
		t.Errorf("DLQ record SubscriptionID = %q, want %q", records[0].SubscriptionID, "sub-dlq")
	}
	if records[0].LastError == "" {
		t.Error("DLQ record LastError is empty, want a non-empty error message")
	}
}

func TestWorkerDispatcher_NoDLQWhenDisabled(t *testing.T) {
	// No DLQStore configured — failures should not panic.
	receiver := newAlwaysFailServer(t)

	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-nodlq", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		ServiceName:    "test",
		MaxAttempts:    1,
		WorkerCount:    1,
		TimeoutSeconds: 5,
		RetryBackoff:   noBackoff,
		// DLQStore deliberately nil
	}, provider)

	if err := d.Dispatch(context.Background(), webhooks.Event{ID: "evt-nodlq", Type: "audit.write"}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	// No panic = success.
}

func TestWorkerDispatcher_ReplayRecord_Success(t *testing.T) {
	var calls int
	receiver := newCountingServer(t, &calls, http.StatusOK)

	rec := webhooks.DLQRecord{
		SubscriptionID:  "sub-replay",
		SubscriptionURL: receiver.URL,
		Event: webhooks.Event{
			ID:        "evt-replay",
			Type:      "audit.write",
			CreatedAt: time.Now().UTC(),
		},
	}

	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		ServiceName:    "test",
		MaxAttempts:    1,
		WorkerCount:    1,
		TimeoutSeconds: 5,
		RetryBackoff:   noBackoff,
	}, &staticProvider{})
	defer d.Close() //nolint:errcheck

	if err := d.ReplayRecord(rec); err != nil {
		t.Fatalf("ReplayRecord() error = %v", err)
	}
	if calls != 1 {
		t.Errorf("receiver called %d times after replay, want 1", calls)
	}
}

func TestWorkerDispatcher_ReplayRecord_Failure(t *testing.T) {
	receiver := newAlwaysFailServer(t)

	rec := webhooks.DLQRecord{
		SubscriptionID:  "sub-replay-fail",
		SubscriptionURL: receiver.URL,
		Event:           webhooks.Event{ID: "evt-replay-fail", Type: "audit.write"},
	}

	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		ServiceName:    "test",
		MaxAttempts:    1,
		WorkerCount:    1,
		TimeoutSeconds: 5,
		RetryBackoff:   noBackoff,
	}, &staticProvider{})
	defer d.Close() //nolint:errcheck

	if err := d.ReplayRecord(rec); err == nil {
		t.Error("ReplayRecord() should return error when receiver fails")
	}
}

func TestWorkerDispatcher_DLQNotStoredOnSuccess(t *testing.T) {
	// When delivery succeeds, nothing should be written to the DLQ.
	var calls int
	receiver := newCountingServer(t, &calls, http.StatusOK)

	dlqStore := openDLQStore(t)

	provider := &staticProvider{subs: []webhooks.Subscription{
		{ID: "sub-ok", URL: receiver.URL, Enabled: true, Events: []string{"audit.write"}},
	}}
	d := webhooks.NewWorkerDispatcher(webhooks.Config{
		ServiceName:    "test",
		MaxAttempts:    1,
		WorkerCount:    1,
		TimeoutSeconds: 5,
		RetryBackoff:   noBackoff,
		DLQStore:       dlqStore,
	}, provider)

	if err := d.Dispatch(context.Background(), webhooks.Event{ID: "evt-ok", Type: "audit.write"}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	records, err := dlqStore.List()
	if err != nil {
		t.Fatalf("DLQ.List(): %v", err)
	}
	if len(records) != 0 {
		t.Errorf("DLQ has %d records after successful delivery, want 0", len(records))
	}
}
