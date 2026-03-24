package webhooks

import (
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
)

// dlqBucket is the bbolt bucket name used to persist DLQ records.
const dlqBucket = "webhook_dlq"

// StoreDLQSink is the storage-backed DLQSink implementation. It resolves the
// correct site store from a shared Registry and writes each DLQRecord as JSON
// under the "webhook_dlq" bucket.
//
// Keys use the same nanosecond-timestamp pattern as the audit bucket:
//
//	<failed_at_unix_nano>-<monotonic_seq>.json
//
// This keeps entries time-sorted and guarantees uniqueness under concurrent failures.
//
// Create instances with NewStoreDLQSink; the zero value is not usable.
type StoreDLQSink struct {
	stores *storage.Registry
	seq    uint64 // monotonic counter; incremented atomically
}

// NewStoreDLQSink returns a StoreDLQSink that writes DLQ records into the
// site stores managed by stores.
func NewStoreDLQSink(stores *storage.Registry) *StoreDLQSink {
	return &StoreDLQSink{stores: stores}
}

// WriteDLQ serialises record and persists it in the "webhook_dlq" bucket of
// the site store identified by record.SiteID. A missing SiteID is a no-op.
func (s *StoreDLQSink) WriteDLQ(record DLQRecord) error {
	if record.SiteID == "" {
		return nil
	}

	store, err := s.stores.Open(record.SiteID)
	if err != nil {
		return fmt.Errorf("opening store for DLQ site %q: %w", record.SiteID, err)
	}

	b, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshaling DLQ record: %w", err)
	}

	ns := record.FailedAt.UnixNano()
	seq := atomic.AddUint64(&s.seq, 1)
	key := fmt.Sprintf("%d-%d.json", ns, seq)

	return store.Write(dlqBucket, key, b)
}

// compile-time check that *StoreDLQSink satisfies DLQSink.
var _ DLQSink = (*StoreDLQSink)(nil)

// DLQBucket returns the bucket name used by StoreDLQSink. Exported so HTTP
// handlers can reference the same constant without duplicating the string.
func DLQBucket() string { return dlqBucket }
