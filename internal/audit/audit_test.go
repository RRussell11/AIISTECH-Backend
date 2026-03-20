package audit_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/RRussell11/AIISTECH-Backend/internal/audit"
)

func TestWrite_CreatesFileWithCorrectContent(t *testing.T) {
	dir := t.TempDir()
	auditDir := filepath.Join(dir, "audit")

	entry := audit.Entry{
		RequestID: "req-123",
		SiteID:    "local",
		Method:    "POST",
		Path:      "/sites/local/events",
		Status:    201,
		Timestamp: "2024-01-01T00:00:00Z",
	}

	if err := audit.Write(entry, auditDir); err != nil {
		t.Fatalf("Write: %v", err)
	}

	entries, err := os.ReadDir(auditDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit file, got %d", len(entries))
	}

	data, err := os.ReadFile(filepath.Join(auditDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var got audit.Entry
	if err := json.Unmarshal(data, &got); err != nil {
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

func TestWrite_CreatesNestedDir(t *testing.T) {
	dir := t.TempDir()
	auditDir := filepath.Join(dir, "nested", "audit")

	if err := audit.Write(audit.Entry{SiteID: "test"}, auditDir); err != nil {
		t.Fatalf("Write with nested dir: %v", err)
	}

	if _, err := os.Stat(auditDir); err != nil {
		t.Errorf("audit dir not created: %v", err)
	}
}

func TestWrite_MultipleEntriesDistinctFiles(t *testing.T) {
	dir := t.TempDir()

	for i := 0; i < 3; i++ {
		if err := audit.Write(audit.Entry{SiteID: "local", Status: 200}, dir); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("expected 3 audit files, got %d", len(entries))
	}
}
