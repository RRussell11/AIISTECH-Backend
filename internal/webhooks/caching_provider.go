package webhooks

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// cacheEntry holds a cached subscription slice and its expiry time.
type cacheEntry struct {
	subs    []Subscription
	expires time.Time
}

// CachingProvider wraps an inner Provider with a per-(service,eventType,tenantID)
// TTL cache, dramatically reducing outbound calls to the PhaseMirror-HQ
// subscription API under load.
//
// Cache misses are coalesced via singleflight so that concurrent callers
// waiting for the same key share a single outbound fetch rather than
// thundering into the upstream API.
//
// Errors from the inner provider are never cached; every error causes the next
// call to attempt a fresh fetch.
//
// Create instances with NewCachingProvider. The zero value is not usable.
type CachingProvider struct {
	inner Provider
	ttl   time.Duration

	mu    sync.RWMutex
	cache map[string]cacheEntry
	sf    singleflight.Group
}

// NewCachingProvider creates a CachingProvider that wraps inner with a TTL
// cache. ttl must be positive; values ≤ 0 fall back to a 30-second default.
func NewCachingProvider(inner Provider, ttl time.Duration) *CachingProvider {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &CachingProvider{
		inner: inner,
		ttl:   ttl,
		cache: make(map[string]cacheEntry),
	}
}

// sfKey builds the map/singleflight key from the three filter dimensions.
// \x00 is used as a separator because it cannot appear in valid service names,
// event type strings, or tenant IDs.
func (c *CachingProvider) sfKey(service, eventType, tenantID string) string {
	return service + "\x00" + eventType + "\x00" + tenantID
}

// ListSubscriptions returns subscriptions from the cache when a live entry
// exists, or fetches from the inner Provider (coalescing concurrent misses).
func (c *CachingProvider) ListSubscriptions(ctx context.Context, service, eventType, tenantID string) ([]Subscription, error) {
	key := c.sfKey(service, eventType, tenantID)

	// Fast path: read from cache.
	c.mu.RLock()
	if e, ok := c.cache[key]; ok && time.Now().Before(e.expires) {
		subs := e.subs
		c.mu.RUnlock()
		return subs, nil
	}
	c.mu.RUnlock()

	// Slow path: coalesce concurrent misses into one outbound fetch.
	// We use a background context with a fixed timeout for the inner fetch so
	// that all waiters within the singleflight group see the result even if the
	// triggering request's context is cancelled.
	v, err, _ := c.sf.Do(key, func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		subs, fetchErr := c.inner.ListSubscriptions(fetchCtx, service, eventType, tenantID)
		if fetchErr != nil {
			return nil, fetchErr
		}

		c.mu.Lock()
		c.cache[key] = cacheEntry{subs: subs, expires: time.Now().Add(c.ttl)}
		c.mu.Unlock()

		return subs, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]Subscription), nil
}

// Invalidate removes the cached entry for the given key dimensions, forcing the
// next call to fetch fresh data from the inner Provider. It is a no-op when no
// entry exists for the key.
func (c *CachingProvider) Invalidate(service, eventType, tenantID string) {
	key := c.sfKey(service, eventType, tenantID)
	c.mu.Lock()
	delete(c.cache, key)
	c.mu.Unlock()
}
