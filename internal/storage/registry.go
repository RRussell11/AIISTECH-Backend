package storage

import (
	"fmt"
	"sync"

	"github.com/RRussell11/AIISTECH-Backend/internal/state"
)

// Registry lazily opens and caches a BBoltStore for each site.
// It is safe for concurrent use.
type Registry struct {
	mu     sync.Mutex
	stores map[string]*BBoltStore
}

// NewRegistry returns a new, empty Registry.
func NewRegistry() *Registry {
	return &Registry{stores: make(map[string]*BBoltStore)}
}

// Open returns the Store for siteID, opening the database the first time it is
// requested. Subsequent calls for the same siteID return the cached store.
func (r *Registry) Open(siteID string) (Store, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if s, ok := r.stores[siteID]; ok {
		return s, nil
	}

	s, err := Open(state.DBPath(siteID))
	if err != nil {
		return nil, fmt.Errorf("opening store for site %q: %w", siteID, err)
	}
	r.stores[siteID] = s
	return s, nil
}

// CloseAll closes every open store held by the registry.
func (r *Registry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, s := range r.stores {
		_ = s.Close()
		delete(r.stores, id)
	}
}
