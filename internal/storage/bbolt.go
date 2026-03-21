package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	bolt "go.etcd.io/bbolt"
)

// BBoltStore is a bbolt-backed implementation of Store.
// It uses one database file per site, with one bucket per data category.
type BBoltStore struct {
	db *bolt.DB
}

// Open opens (or creates) the bbolt database at path.
// Parent directories are created as needed.
func Open(path string) (*BBoltStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating db dir %q: %w", filepath.Dir(path), err)
	}
	db, err := bolt.Open(path, 0o644, nil)
	if err != nil {
		return nil, fmt.Errorf("opening bbolt db %q: %w", path, err)
	}
	return &BBoltStore{db: db}, nil
}

// Write stores value at bucket/key, creating the bucket if it does not exist.
func (s *BBoltStore) Write(bucket, key string, value []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return fmt.Errorf("creating bucket %q: %w", bucket, err)
		}
		return b.Put([]byte(key), value)
	})
}

// Get retrieves the value stored at bucket/key.
// Returns ErrNotFound when the bucket or key is absent.
func (s *BBoltStore) Get(bucket, key string) ([]byte, error) {
	var out []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return ErrNotFound
		}
		v := b.Get([]byte(key))
		if v == nil {
			return ErrNotFound
		}
		// Copy the value out of the read transaction before returning.
		out = make([]byte, len(v))
		copy(out, v)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// List returns all keys in bucket in ascending byte order.
// Returns an empty slice when the bucket does not exist yet.
func (s *BBoltStore) List(bucket string) ([]string, error) {
	var keys []string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil // bucket absent = empty list, not an error
		}
		return b.ForEach(func(k, _ []byte) error {
			keys = append(keys, string(k))
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if keys == nil {
		keys = []string{}
	}
	return keys, nil
}

// Delete removes the entry at bucket/key.
// Returns ErrNotFound when the bucket or key is absent.
func (s *BBoltStore) Delete(bucket, key string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return ErrNotFound
		}
		if b.Get([]byte(key)) == nil {
			return ErrNotFound
		}
		return b.Delete([]byte(key))
	})
}

// Close closes the underlying bbolt database.
func (s *BBoltStore) Close() error {
	return s.db.Close()
}

// ListPage returns up to limit keys from bucket that come strictly after cursor
// in ascending byte order. Pass cursor="" to start from the beginning.
// nextCursor is the last key in the returned page; pass it as cursor on the
// next call to fetch subsequent pages. nextCursor is "" when the end of the
// bucket has been reached.
func (s *BBoltStore) ListPage(bucket, cursor string, limit int) (keys []string, nextCursor string, err error) {
	err = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil // bucket absent = empty page
		}
		c := b.Cursor()

		var k []byte
		if cursor == "" {
			k, _ = c.First()
		} else {
			// Seek to the cursor key; if present, advance one position past it.
			k, _ = c.Seek([]byte(cursor))
			if k != nil && string(k) == cursor {
				k, _ = c.Next()
			}
		}

		for ; k != nil && len(keys) < limit; k, _ = c.Next() {
			keys = append(keys, string(k))
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	if keys == nil {
		keys = []string{}
	}
	if len(keys) > 0 {
		nextCursor = keys[len(keys)-1]
		// If we consumed fewer than limit items there are no more pages.
		if len(keys) < limit {
			nextCursor = ""
		}
	}
	return keys, nextCursor, nil
}

// compile-time check that *BBoltStore satisfies Store.
var _ Store = (*BBoltStore)(nil)

// isNotFound reports whether err wraps ErrNotFound.
func isNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}
