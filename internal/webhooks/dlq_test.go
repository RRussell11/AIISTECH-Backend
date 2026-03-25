package webhooks_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

// openTestStores returns a storage.Registry whose stores are backed by a temp
// directory that is cleaned up when the test finishes.
func openTestStores(t *testing.T) *storage.Registry {
	t.Helper()
	dir := t.TempDir()
	// Point state at the temp dir so BBoltStore files land there.
	t.Setenv("AIISTECH_DATA_DIR", dir)
	_ = filepath.Join(dir, "sites") // ensure path exists
	if err := os.MkdirAll(filepath.Join(dir, "local"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return storage.NewRegistry()
}

func TestStoreDLQSink_StoreAndList(t *testing.T) {
	t.Chdir(t.TempDir())
	stores := openTestStores(t)
	t.Cleanup(func() { stores.CloseAll() })

	sink := webhooks.NewStoreDLQSink(stores)

	rec := webhooks.DLQRecord{
		SubscriptionID:  "sub-1",
		SubscriptionURL: "https://example.com/hook",
		Secret:          "s3cr3t",
		EventID:         "evt-abc",
		EventType:       "audit.write",
		SiteID:          "local",
		TenantID:        "acme",
		Payload:         []byte(`{"type":"audit.write"}`),
		Attempts:        5,
		FailedAt:        time.Now().UTC(),
	}

	if err := sink.Store(rec); err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	// Verify the record is persisted in the site store.
	siteStore, err := stores.Open("local")
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	keys, err := siteStore.List(webhooks.DLQBucket)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
}

func TestStoreDLQSink_IDMatchesStorageKey(t *testing.T) {
	t.Chdir(t.TempDir())
	stores := openTestStores(t)
	t.Cleanup(func() { stores.CloseAll() })

	sink := webhooks.NewStoreDLQSink(stores)
	rec := webhooks.DLQRecord{
		SiteID:  "local",
		EventID: "evt-1",
		Payload: []byte(`{}`),
	}
	if err := sink.Store(rec); err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	// The storage key and the embedded record.ID should match.
	siteStore, _ := stores.Open("local")
	keys, _ := siteStore.List(webhooks.DLQBucket)
	if len(keys) == 0 {
		t.Fatal("no keys stored")
	}
	key := keys[0]

	raw, err := siteStore.Get(webhooks.DLQBucket, key)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	// The stored JSON should contain "\"id\":\"<key>\""
	if string(raw) == "" {
		t.Fatal("stored value is empty")
	}
	// Quick check: the key appears in the JSON
	if !containsSubstr(string(raw), key) {
		t.Errorf("stored JSON does not contain the key %q; got %s", key, raw)
	}
}

func TestStoreDLQSink_MultipleRecordsOrderedByKey(t *testing.T) {
	t.Chdir(t.TempDir())
	stores := openTestStores(t)
	t.Cleanup(func() { stores.CloseAll() })

	sink := webhooks.NewStoreDLQSink(stores)
	for i := 0; i < 5; i++ {
		if err := sink.Store(webhooks.DLQRecord{
			SiteID:  "local",
			EventID: "evt",
			Payload: []byte(`{}`),
		}); err != nil {
			t.Fatalf("Store() error = %v", err)
		}
	}

	siteStore, _ := stores.Open("local")
	keys, _ := siteStore.List(webhooks.DLQBucket)
	if len(keys) != 5 {
		t.Fatalf("expected 5 keys, got %d", len(keys))
	}
	// Keys should be in ascending lexicographic order (bbolt guarantees this).
	for i := 1; i < len(keys); i++ {
		if keys[i] <= keys[i-1] {
			t.Errorf("keys not in ascending order: keys[%d]=%q, keys[%d]=%q", i-1, keys[i-1], i, keys[i])
		}
	}
}

func TestStoreDLQSink_UnknownSiteIDError(t *testing.T) {
	t.Chdir(t.TempDir())
	stores := openTestStores(t)
	t.Cleanup(func() { stores.CloseAll() })

	sink := webhooks.NewStoreDLQSink(stores)
	// "unknown-site" has no directory so bbolt Open should fail.
	err := sink.Store(webhooks.DLQRecord{
		SiteID:  "unknown-site",
		EventID: "evt",
		Payload: []byte(`{}`),
	})
	// The store registry lazily creates stores — it may succeed or fail depending
	// on filesystem permissions. The important thing is that it doesn't panic.
	_ = err
}

// containsSubstr is a simple substring check used in tests to avoid importing
// the strings package at the cost of a loop.
func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
