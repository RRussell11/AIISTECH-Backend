package webhooks

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
)

const DLQBucket = "webhook_dlq"

// dlqSeq makes DLQ keys unique even when UnixNano returns duplicate values
// (common in CI or on systems with low clock resolution).
var dlqSeq uint64

// DLQRecord is a persisted record of a webhook delivery that exhausted all
// retry attempts without a successful 2xx response from the receiver.
//
// The Payload field holds the exact JSON body that was (attempted to be)
// delivered so that a later replay can re-POST identical bytes and recompute
// a valid HMAC signature using the stored Secret.
type DLQRecord struct {
	// ID is the storage key of this record (e.g. "00001748000000000-1.json").
	// Returned to API consumers as the identifier for Get/Delete/Replay calls.
	ID string `json:"id"`

	// SubscriptionID is the PhaseMirror-HQ subscription identifier.
	SubscriptionID string `json:"subscription_id"`

	// SubscriptionURL is the endpoint that rejected all delivery attempts.
	SubscriptionURL string `json:"subscription_url"`

	// Secret is the per-subscription HMAC secret used to re-sign the payload
	// on replay. Omitted from JSON when empty.
	Secret string `json:"secret,omitempty"`

	// EventID is the originating Event.ID.
	EventID string `json:"event_id"`

	// EventType is the originating Event.Type (e.g. "audit.write").
	EventType string `json:"event_type"`

	// SiteID identifies the site that produced the event.
	SiteID string `json:"site_id"`

	// TenantID is the tenant scope of the originating event, or empty for
	// legacy (non-tenant) sites.
	TenantID string `json:"tenant_id,omitempty"`

	// Payload is the serialised JSON body that was sent (or attempted) during
	// delivery.
	Payload []byte `json:"payload"`

	// Attempts is the total number of delivery attempts that were made before
	// the entry was moved to the DLQ.
	Attempts int `json:"attempts"`

	// FailedAt is the UTC timestamp at which the final delivery attempt failed.
	FailedAt time.Time `json:"failed_at"`
}

// DLQSink receives webhook delivery failures after all retry attempts have
// been exhausted.  Implementations must be safe for concurrent use.
type DLQSink interface {
	// Store persists record in the dead-letter queue of the site identified by
	// record.SiteID.  Implementations should not return an error for
	// recoverable conditions; log and discard if storage is unavailable.
	Store(record DLQRecord) error
}

// StoreDLQSink is a bbolt-backed DLQSink that persists DLQ records in each
// site's own storage bucket ("webhook_dlq").  It retrieves (or lazily opens)
// the site's store through the provided storage.Registry.
type StoreDLQSink struct {
	stores *storage.Registry
}

// NewStoreDLQSink returns a StoreDLQSink that routes records to per-site
// bbolt stores obtained from stores.
func NewStoreDLQSink(stores *storage.Registry) *StoreDLQSink {
	return &StoreDLQSink{stores: stores}
}

// Store serialises record and writes it to the site's "webhook_dlq" bucket.
// The storage key is a zero-padded nanosecond timestamp with a sequence
// suffix to guarantee uniqueness, e.g. "00001748000000000000-1.json".
func (s *StoreDLQSink) Store(record DLQRecord) error {
	store, err := s.stores.Open(record.SiteID)
	if err != nil {
		return fmt.Errorf("dlq: opening store for site %q: %w", record.SiteID, err)
	}

	ns := time.Now().UnixNano()
	seq := atomic.AddUint64(&dlqSeq, 1)
	key := fmt.Sprintf("%020d-%d.json", ns, seq)
	record.ID = key

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("dlq: marshalling record: %w", err)
	}
	if err := store.Write(DLQBucket, key, data); err != nil {
		return fmt.Errorf("dlq: writing record: %w", err)
	}
	return nil
}

// compile-time check that *StoreDLQSink satisfies DLQSink.
var _ DLQSink = (*StoreDLQSink)(nil)
