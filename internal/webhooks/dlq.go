package webhooks

import (
	"encoding/json"
	"errors"
	"expvar"
	"fmt"
	"time"

	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
)

const dlqBucket = "webhook_dlq"

// DLQ expvar metrics — incremented throughout the delivery pipeline.
var (
	metricDLQStoredTotal        = expvar.NewInt("webhook_dlq_stored_total")
	metricDLQReplaySuccessTotal = expvar.NewInt("webhook_dlq_replay_success_total")
	metricDLQReplayFailureTotal = expvar.NewInt("webhook_dlq_replay_failure_total")
)

// DLQRecord is a persisted record of a failed webhook delivery.
// It is written by the dispatcher when all delivery attempts are exhausted and
// read back by the replay endpoints and the auto-retry scheduler.
type DLQRecord struct {
	// ID is the stable storage key for this record (nanosecond timestamp + ".json").
	// It is assigned by DLQStore.Save when the record is first created.
	ID string `json:"id"`

	// SubscriptionID, SubscriptionURL, and SubscriptionSecret reproduce the
	// delivery target so the record can be replayed without refetching subscriptions.
	SubscriptionID     string `json:"subscription_id"`
	SubscriptionURL    string `json:"subscription_url"`
	SubscriptionSecret string `json:"subscription_secret,omitempty"`

	// Event is the original event payload that failed to be delivered.
	Event Event `json:"event"`

	// Attempts is the number of replay attempts (not counting the initial
	// delivery burst that preceded this record being stored).
	Attempts int `json:"attempts"`

	// LastError holds the human-readable error string from the most recent
	// failed attempt.
	LastError string `json:"last_error"`

	// FailedAt is the UTC timestamp of the most recent failed delivery.
	FailedAt time.Time `json:"failed_at"`

	// NextRetryAfter is the earliest UTC time at which the auto-retry
	// scheduler will attempt a re-delivery. It is updated with exponential
	// back-off on each failed replay. Records whose NextRetryAfter is in the
	// future are skipped by the scheduler until the time is reached.
	// Manual replay via the HTTP endpoint ignores this field.
	NextRetryAfter time.Time `json:"next_retry_after"`
}

// IsTerminal reports whether the record has reached the maximum number of
// replay attempts and should no longer be auto-retried.
func (r *DLQRecord) IsTerminal(maxAttempts int) bool {
	return maxAttempts > 0 && r.Attempts >= maxAttempts
}

// DLQStore is a bbolt-backed store for DLQRecord values. Records are kept in
// the "webhook_dlq" bucket, keyed by DLQRecord.ID.
//
// The same underlying storage.Store can be shared with a StoreProvider (each
// uses a different named bucket).
//
// Create instances with NewDLQStore. The zero value is not usable.
type DLQStore struct {
	store storage.Store
}

// NewDLQStore returns a DLQStore backed by the given Store.
func NewDLQStore(store storage.Store) *DLQStore {
	return &DLQStore{store: store}
}

// Save persists r in the store. If r.ID is empty a nanosecond-timestamped key
// is generated and assigned to r.ID before writing.
func (d *DLQStore) Save(r *DLQRecord) error {
	if r.ID == "" {
		r.ID = fmt.Sprintf("%d.json", time.Now().UnixNano())
	}
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("webhooks: dlq: encoding record: %w", err)
	}
	return d.store.Write(dlqBucket, r.ID, data)
}

// Get retrieves the DLQ record with the given id.
// Returns storage.ErrNotFound when no matching record exists.
func (d *DLQStore) Get(id string) (DLQRecord, error) {
	data, err := d.store.Get(dlqBucket, id)
	if err != nil {
		return DLQRecord{}, err
	}
	var r DLQRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return DLQRecord{}, fmt.Errorf("webhooks: dlq: decoding record %q: %w", id, err)
	}
	return r, nil
}

// Delete removes the record with the given id.
// Returns storage.ErrNotFound when no matching record exists.
func (d *DLQStore) Delete(id string) error {
	return d.store.Delete(dlqBucket, id)
}

// List returns all DLQ records in ascending key order.
func (d *DLQStore) List() ([]DLQRecord, error) {
	keys, err := d.store.List(dlqBucket)
	if err != nil {
		return nil, fmt.Errorf("webhooks: dlq: listing records: %w", err)
	}
	return d.loadKeys(keys)
}

// ListPage returns up to limit records starting strictly after cursor.
// Pass cursor="" to start from the beginning.
// nextCursor is the last key returned; pass it as cursor on the next call.
// nextCursor is "" when there are no further records.
func (d *DLQStore) ListPage(cursor string, limit int) (records []DLQRecord, nextCursor string, err error) {
	keys, next, err := d.store.ListPage(dlqBucket, cursor, limit)
	if err != nil {
		return nil, "", fmt.Errorf("webhooks: dlq: listing page: %w", err)
	}
	records, err = d.loadKeys(keys)
	return records, next, err
}

// loadKeys reads and decodes the DLQ records for the given keys.
// Records that have been concurrently deleted are silently skipped.
func (d *DLQStore) loadKeys(keys []string) ([]DLQRecord, error) {
	out := make([]DLQRecord, 0, len(keys))
	for _, key := range keys {
		data, err := d.store.Get(dlqBucket, key)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				continue // raced with a concurrent delete
			}
			return nil, fmt.Errorf("webhooks: dlq: reading record %q: %w", key, err)
		}
		var r DLQRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("webhooks: dlq: decoding record %q: %w", key, err)
		}
		out = append(out, r)
	}
	return out, nil
}

// DLQReplayer is the interface for replaying a DLQ record to its target
// subscription. The implementation attempts a single delivery and returns
// the delivery error (nil on success).
//
// Callers are responsible for updating the DLQ store after the call returns.
type DLQReplayer interface {
	ReplayRecord(record DLQRecord) error
}
