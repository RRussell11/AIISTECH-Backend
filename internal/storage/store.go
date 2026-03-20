package storage

import "errors"

// ErrNotFound is returned by Get and Delete when the requested key does not exist.
var ErrNotFound = errors.New("not found")

// Store is the interface satisfied by any persistent backend used to store
// site-scoped data. Each site has exactly one Store; data is organised into
// named buckets (e.g. "events", "artifacts", "audit").
type Store interface {
	// Write stores value under bucket/key, creating the bucket if necessary.
	Write(bucket, key string, value []byte) error
	// Get retrieves the value stored at bucket/key.
	// Returns ErrNotFound when the bucket or key is absent.
	Get(bucket, key string) ([]byte, error)
	// List returns all keys inside bucket in ascending order.
	// Returns an empty slice when the bucket is absent.
	List(bucket string) ([]string, error)
	// ListPage returns up to limit keys from bucket whose byte-order position
	// comes strictly after cursor (pass "" to start from the beginning).
	// nextCursor is the last key returned, and can be passed as cursor in a
	// subsequent call to fetch the next page. nextCursor is empty when there
	// are no more keys after the returned page.
	ListPage(bucket, cursor string, limit int) (keys []string, nextCursor string, err error)
	// Delete removes the entry at bucket/key.
	// Returns ErrNotFound when the bucket or key is absent.
	Delete(bucket, key string) error
	// Close releases the underlying resources held by the store.
	Close() error
}
