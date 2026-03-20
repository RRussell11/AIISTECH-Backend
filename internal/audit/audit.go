package audit

import (
	"encoding/json"
	"fmt"
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

// Storer is the write-side of any storage backend that can persist audit entries.
// It is satisfied by *storage.BBoltStore (and any mock in tests).
type Storer interface {
	Write(bucket, key string, value []byte) error
}

// Write serialises e and persists it under the "audit" bucket with a
// nanosecond-timestamped key. The caller is responsible for providing a Storer
// that targets the correct per-site store.
func Write(e Entry, s Storer) error {
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshalling audit entry: %w", err)
	}
	key := fmt.Sprintf("%d.json", time.Now().UnixNano())
	if err := s.Write("audit", key, data); err != nil {
		return fmt.Errorf("writing audit entry: %w", err)
	}
	return nil
}

