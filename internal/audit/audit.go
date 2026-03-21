package audit

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"
)

type Entry struct {
	RequestID  string `json:"request_id"`
	SiteID     string `json:"site_id"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	Timestamp  string `json:"timestamp"`
}

// Storer is the write-side of any storage backend that can persist audit entries.
// It is satisfied by *storage.BBoltStore (and any mock in tests).
type Storer interface {
	Write(bucket, key string, value []byte) error
}

// auditSeq ensures keys remain unique even if the OS clock returns duplicate
// UnixNano values for closely-spaced calls (common on Windows/CI).
var auditSeq uint64

// Write serialises e and persists it under the "audit" bucket with a key that is
// primarily nanosecond-timestamped, with a sequence suffix to guarantee uniqueness.
func Write(e Entry, s Storer) error {
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshalling audit entry: %w", err)
	}

	ns := time.Now().UnixNano()
	seq := atomic.AddUint64(&auditSeq, 1)
	key := fmt.Sprintf("%d-%d.json", ns, seq)

	if err := s.Write("audit", key, data); err != nil {
		return fmt.Errorf("writing audit entry: %w", err)
	}
	return nil
}
