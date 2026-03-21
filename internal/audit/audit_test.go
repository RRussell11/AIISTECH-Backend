package audit_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/RRussell11/AIISTECH-Backend/internal/audit"
)

// mockStorer captures Write calls in memory for use in tests.
type mockStorer struct {
	mu   sync.Mutex
	keys []string
	data map[string][]byte
}

func (m *mockStorer) Write(bucket, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = make(map[string][]byte)
	}
	m.keys = append(m.keys, key)
	m.data[key] = append([]byte(nil), value...) // defensive copy
	return nil
}

func TestWrite_StoresEntryWithCorrectContent(t *testing.T) {
	s := &mockStorer{}

	entry := audit.Entry{
		RequestID: "req-123",
		SiteID:    "local",
		Method:    "POST",
		Path:      "/sites/local/events",
		Status:    201,
		Timestamp: "2024-01-01T00:00:00Z",
	}

	if err := audit.Write(entry, s); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if len(s.keys) != 1 {
		t.Fatalf("expected 1 write, got %d", len(s.keys))
	}

	var got audit.Entry
	if err := json.Unmarshal(s.data[s.keys[0]], &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.RequestID != entry.RequestID {
		t.Errorf("RequestID = %q, want %q", got.RequestID, entry.RequestID)
	}
	if got.SiteID != entry.SiteID {
		t.Errorf("SiteID = %q, want %q", got.SiteID, entry.SiteID)
	}
	if got.Method != entry.Method {
		t.Errorf("Method = %q, want %q", got.Method, entry.Method)
	}
	if got.Status != entry.Status {
		t.Errorf("Status = %d, want %d", got.Status, entry.Status)
	}
}

func TestWrite_KeyHasJsonSuffix(t *testing.T) {
	s := &mockStorer{}
	if err := audit.Write(audit.Entry{SiteID: "test"}, s); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(s.keys) == 0 {
		t.Fatal("no write recorded")
	}
	key := s.keys[0]
	if len(key) < 5 || key[len(key)-5:] != ".json" {
		t.Errorf("key %q does not end in .json", key)
	}
}

func TestWrite_MultipleEntriesDistinctKeys(t *testing.T) {
	s := &mockStorer{}

	for i := 0; i < 3; i++ {
		if err := audit.Write(audit.Entry{SiteID: "local", Status: 200}, s); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}

		// On some systems (notably Windows), successive calls can land on the same
		// clock tick, causing time-based keys to collide. Ensure time advances.
		time.Sleep(time.Millisecond)
	}

	seen := make(map[string]bool)
	for _, k := range s.keys {
		if seen[k] {
			t.Errorf("duplicate key %q", k)
		}
		seen[k] = true
	}
	if len(s.keys) != 3 {
		t.Errorf("expected 3 entries, got %d", len(s.keys))
	}
}

