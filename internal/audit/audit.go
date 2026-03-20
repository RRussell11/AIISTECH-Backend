package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry is a structured audit log record written for every mutating site-scoped request.
type Entry struct {
	RequestID string `json:"request_id"`
	SiteID    string `json:"site_id"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Status    int    `json:"status"`
	Timestamp string `json:"timestamp"`
}

// Write serialises e to a nanosecond-timestamped JSON file under auditDir.
// The directory is created if it does not already exist.
func Write(e Entry, auditDir string) error {
	if err := os.MkdirAll(auditDir, 0o755); err != nil {
		return fmt.Errorf("creating audit dir: %w", err)
	}
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshalling audit entry: %w", err)
	}
	filename := fmt.Sprintf("%d.json", time.Now().UnixNano())
	dest := filepath.Join(auditDir, filename)
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return fmt.Errorf("writing audit file: %w", err)
	}
	return nil
}
