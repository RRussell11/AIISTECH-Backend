package webhooks

import (
	"context"
	"log/slog"
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
// When constructed with a positive pollInterval, CachingProvider starts a
// background goroutine that proactively re-fetches every known cache key on
// that interval, keeping the cache warm so the dispatch hot path never blocks
// on an outbound HTTP call. Call Close to stop the goroutine gracefully.
//
// The cache is keyed per (service, eventType, tenantID) tuple. Entries are
// evicted lazily on the next read after they expire. Errors from the inner
// provider are never cached, so a transient failure causes an immediate retry
// on the next Dispatch call.
//
// It is safe for concurrent use.
type CachingProvider struct {
	inner        Provider
	ttl          time.Duration
	pollInterval time.Duration
	mu           sync.RWMutex
	cache        map[cacheKey]cacheEntry
	stopCh       chan struct{}
	closeOnce    sync.Once
	wg           sync.WaitGroup
}

// NewCachingProvider constructs a CachingProvider that wraps inner and caches
// results for ttl. A zero or negative ttl uses the default of 30 seconds.
//
// When pollInterval is positive a background goroutine is started that
// proactively refreshes all known cache keys every pollInterval. A zero or
// negative pollInterval disables background polling; the cache is then
// populated and refreshed only on demand (lazy). Call Close to stop the
// background goroutine when polling is enabled.
func NewCachingProvider(inner Provider, ttl, pollInterval time.Duration) *CachingProvider {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	cp := &CachingProvider{
		inner:        inner,
		ttl:          ttl,
		pollInterval: pollInterval,
		cache:        make(map[cacheKey]cacheEntry),
		stopCh:       make(chan struct{}),
	}
	if pollInterval > 0 {
		cp.wg.Add(1)
		go cp.poll()
	}
	return cp
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

// Close stops the background polling goroutine (if running) and waits for it
// to exit. It is safe to call Close on a CachingProvider that was constructed
// without a positive pollInterval (it returns nil immediately). Close is
// idempotent; subsequent calls after the first are no-ops.
func (c *CachingProvider) Close() error {
	c.closeOnce.Do(func() { close(c.stopCh) })
	c.wg.Wait()
	return nil
}

// poll is the background refresh loop. It ticks every c.pollInterval and
// calls refresh to update all known cache keys.
func (c *CachingProvider) poll() {
	defer c.wg.Done()
	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.refresh()
		case <-c.stopCh:
			return
		}
	}
}

// refresh re-fetches subscriptions for every key currently held in the cache.
// On success the entry is updated with a fresh TTL. On error the existing
// entry (if any) is left unchanged so that valid cached data continues to be
// served; the error is logged at WARN level.
func (c *CachingProvider) refresh() {
	c.mu.RLock()
	keys := make([]cacheKey, 0, len(c.cache))
	for k := range c.cache {
		keys = append(keys, k)
	}
	c.mu.RUnlock()

	for _, k := range keys {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		subs, err := c.inner.ListSubscriptions(ctx, k.service, k.eventType, k.tenantID)
		cancel()
		if err != nil {
			slog.Warn("background subscription poll failed",
				"service", k.service, "event_type", k.eventType, "error", err)
			continue
		}
		now := time.Now()
		c.mu.Lock()
		c.cache[k] = cacheEntry{subs: subs, expiresAt: now.Add(c.ttl)}
		c.mu.Unlock()
	}
}
