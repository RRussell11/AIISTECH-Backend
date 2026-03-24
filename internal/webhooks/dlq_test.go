package webhooks_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

// TestStoreDLQSink_Write verifies that WriteDLQ persists a DLQRecord to the
// correct site's "webhook_dlq" bucket and the record round-trips correctly.
func TestStoreDLQSink_Write(t *testing.T) {
	t.Chdir(t.TempDir())

	reg := storage.NewRegistry()
	t.Cleanup(func() { reg.CloseAll() })

	// Pre-open the store so we can read from it after the write.
	store, err := reg.Open("testsite")
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}

	sink := webhooks.NewStoreDLQSink(reg)

	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	rec := webhooks.DLQRecord{
		EventID:        "evt-dlq-1",
		EventType:      "audit.write",
		SiteID:         "testsite",
		TenantID:       "acme",
		SubscriptionID: "sub-dlq-1",
		URL:            "https://example.com/hook",
		Payload:        []byte(`{"id":"evt-dlq-1"}`),
		AttemptCount:   3,
		LastError:      "receiver returned status 503",
		FailedAt:       now,
	}

	if err := sink.WriteDLQ(rec); err != nil {
		t.Fatalf("WriteDLQ() error = %v", err)
	}

	// There should be exactly one key in the webhook_dlq bucket.
	keys, err := store.List(webhooks.DLQBucket())
	if err != nil {
		t.Fatalf("List(webhook_dlq) error = %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("want 1 DLQ key, got %d: %v", len(keys), keys)
	}

	// The persisted JSON must round-trip back to the original record.
	data, err := store.Get(webhooks.DLQBucket(), keys[0])
	if err != nil {
		t.Fatalf("Get DLQ entry error = %v", err)
	}

	var got webhooks.DLQRecord
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal DLQ record: %v", err)
	}

	if got.EventID != rec.EventID {
		t.Errorf("EventID = %q, want %q", got.EventID, rec.EventID)
	}
	if got.SubscriptionID != rec.SubscriptionID {
		t.Errorf("SubscriptionID = %q, want %q", got.SubscriptionID, rec.SubscriptionID)
	}
	if got.SiteID != rec.SiteID {
		t.Errorf("SiteID = %q, want %q", got.SiteID, rec.SiteID)
	}
	if got.TenantID != rec.TenantID {
		t.Errorf("TenantID = %q, want %q", got.TenantID, rec.TenantID)
	}
	if got.AttemptCount != rec.AttemptCount {
		t.Errorf("AttemptCount = %d, want %d", got.AttemptCount, rec.AttemptCount)
	}
	if got.LastError != rec.LastError {
		t.Errorf("LastError = %q, want %q", got.LastError, rec.LastError)
	}
}

// TestStoreDLQSink_Write_EmptySiteID verifies that WriteDLQ silently succeeds
// when SiteID is empty (no store to write to, backwards compatible).
func TestStoreDLQSink_Write_EmptySiteID(t *testing.T) {
	t.Chdir(t.TempDir())

	reg := storage.NewRegistry()
	t.Cleanup(func() { reg.CloseAll() })
	sink := webhooks.NewStoreDLQSink(reg)

	err := sink.WriteDLQ(webhooks.DLQRecord{
		EventID:   "evt-no-site",
		EventType: "audit.write",
		SiteID:    "", // intentionally empty
	})
	if err != nil {
		t.Errorf("WriteDLQ with empty SiteID should be a no-op, got error: %v", err)
	}
}

// TestStoreDLQSink_KeysAreTimeSorted verifies that two DLQ records written in
// succession produce keys that sort in ascending time order.
func TestStoreDLQSink_KeysAreTimeSorted(t *testing.T) {
	t.Chdir(t.TempDir())

	reg := storage.NewRegistry()
	t.Cleanup(func() { reg.CloseAll() })

	store, err := reg.Open("sorttest")
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}

	sink := webhooks.NewStoreDLQSink(reg)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, offset := range []time.Duration{0, time.Nanosecond} {
		rec := webhooks.DLQRecord{
			EventID:  "evt-sort",
			SiteID:   "sorttest",
			FailedAt: base.Add(offset),
		}
		if err := sink.WriteDLQ(rec); err != nil {
			t.Fatalf("WriteDLQ #%d error = %v", i, err)
		}
	}

	keys, err := store.List(webhooks.DLQBucket())
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("want 2 keys, got %d", len(keys))
	}
	if keys[0] > keys[1] {
		t.Errorf("keys not in ascending order: %v", keys)
	}
}
