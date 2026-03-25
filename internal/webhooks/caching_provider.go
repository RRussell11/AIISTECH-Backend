package webhooks

import (
	"context"
	"strings"
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
// When pollInterval > 0 a background goroutine proactively refreshes all
// known cache entries on that interval, preventing TTL-expiry cold-start
// spikes under continuous load. Call Close to stop the goroutine.
//
// Create instances with NewCachingProvider. The zero value is not usable.
type CachingProvider struct {
	inner        Provider
	ttl          time.Duration
	pollInterval time.Duration

	mu    sync.RWMutex
	cache map[string]cacheEntry
	sf    singleflight.Group

	// stopCh and doneCh are only non-nil when a poll goroutine is running.
	stopCh chan struct{}
	doneCh chan struct{}
}

// NewCachingProvider creates a CachingProvider that wraps inner with a TTL
// cache. ttl must be positive; values ≤ 0 fall back to a 30-second default.
//
// When pollInterval > 0 a background goroutine is started that proactively
// refreshes all tracked cache entries on that interval. Call Close to stop it.
// A pollInterval ≤ 0 disables background polling (lazy TTL eviction only).
func NewCachingProvider(inner Provider, ttl time.Duration, pollInterval time.Duration) *CachingProvider {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	cp := &CachingProvider{
		inner:        inner,
		ttl:          ttl,
		pollInterval: pollInterval,
		cache:        make(map[string]cacheEntry),
	}
	if pollInterval > 0 {
		cp.stopCh = make(chan struct{})
		cp.doneCh = make(chan struct{})
		go cp.poll()
	}
	return cp
}

// Close stops the background poll goroutine (if running) and waits for it to
// exit. It is safe to call Close on a CachingProvider that was created without
// a poll interval — the call is a no-op in that case.
func (c *CachingProvider) Close() {
	if c.stopCh != nil {
		close(c.stopCh)
		<-c.doneCh
	}
}

// poll is the background goroutine body. It ticks at pollInterval and calls
// refreshAll on each tick until stopCh is closed.
func (c *CachingProvider) poll() {
	defer close(c.doneCh)
	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.refreshAll()
		}
	}
}

// refreshAll iterates over all currently-tracked cache keys and re-fetches
// each one from the inner Provider. On error the existing cache entry is
// preserved (stale-on-error); errors are never written into the cache.
func (c *CachingProvider) refreshAll() {
	c.mu.RLock()
	keys := make([]string, 0, len(c.cache))
	for k := range c.cache {
		keys = append(keys, k)
	}
	c.mu.RUnlock()

	for _, k := range keys {
		// sfKey format: service + "\x00" + eventType + "\x00" + tenantID
		parts := strings.SplitN(k, "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		service, eventType, tenantID := parts[0], parts[1], parts[2]

		fetchCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		subs, err := c.inner.ListSubscriptions(fetchCtx, service, eventType, tenantID)
		cancel()
		if err != nil {
			// Preserve the existing (possibly stale) entry rather than evicting.
			continue
		}

		c.mu.Lock()
		c.cache[k] = cacheEntry{subs: subs, expires: time.Now().Add(c.ttl)}
		c.mu.Unlock()
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
