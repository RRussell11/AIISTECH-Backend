package webhooks

import (
	"context"
	"sync"
	"time"
)

// cacheKey is the composite cache key for subscription lookups.
type cacheKey struct {
	service   string
	eventType string
	tenantID  string
}

// cacheEntry holds a cached subscription list and its expiry time.
type cacheEntry struct {
	subs      []Subscription
	expiresAt time.Time
}

// CachingProvider wraps a Provider and caches ListSubscriptions results for a
// configurable TTL. This reduces load on the PhaseMirror-HQ subscription API
// when many events are dispatched in a short period.
//
// The cache is keyed per (service, eventType, tenantID) tuple. Entries are
// evicted lazily on the next read after they expire; no background goroutine
// is required. Errors from the inner provider are never cached, so a transient
// failure causes an immediate retry on the next Dispatch call.
//
// It is safe for concurrent use.
type CachingProvider struct {
	inner Provider
	ttl   time.Duration
	mu    sync.RWMutex
	cache map[cacheKey]cacheEntry
}

// NewCachingProvider constructs a CachingProvider that wraps inner and caches
// results for ttl. A zero or negative ttl uses the default of 30 seconds.
func NewCachingProvider(inner Provider, ttl time.Duration) *CachingProvider {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &CachingProvider{
		inner: inner,
		ttl:   ttl,
		cache: make(map[cacheKey]cacheEntry),
	}
}

// ListSubscriptions returns subscriptions from the in-memory cache when a
// non-expired entry exists for the (service, eventType, tenantID) triple.
// On a cache miss or expiry it delegates to the inner Provider, stores the
// result, and returns it. Errors from the inner Provider are not cached.
func (c *CachingProvider) ListSubscriptions(ctx context.Context, service, eventType, tenantID string) ([]Subscription, error) {
	k := cacheKey{service: service, eventType: eventType, tenantID: tenantID}
	now := time.Now()

	// Fast path: valid cached entry.
	c.mu.RLock()
	if e, ok := c.cache[k]; ok && now.Before(e.expiresAt) {
		subs := e.subs
		c.mu.RUnlock()
		return subs, nil
	}
	c.mu.RUnlock()

	// Cache miss or expired entry: call the inner provider.
	subs, err := c.inner.ListSubscriptions(ctx, service, eventType, tenantID)
	if err != nil {
		return nil, err
	}

	// Store the fresh result; write lock required.
	c.mu.Lock()
	c.cache[k] = cacheEntry{subs: subs, expiresAt: now.Add(c.ttl)}
	c.mu.Unlock()

	return subs, nil
}
