package webhooks_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

// stubProvider is a controllable Provider for testing.
type stubProvider struct {
	mu    sync.Mutex
	calls int32          // atomic call counter
	subs  []webhooks.Subscription
	err   error
}

func (s *stubProvider) ListSubscriptions(_ context.Context, _, _, _ string) ([]webhooks.Subscription, error) {
	atomic.AddInt32(&s.calls, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return s.subs, nil
}

func (s *stubProvider) callCount() int {
	return int(atomic.LoadInt32(&s.calls))
}

// --- CachingProvider tests ---

func TestCachingProvider_CacheHitReducesCalls(t *testing.T) {
	stub := &stubProvider{subs: []webhooks.Subscription{{ID: "s1", Enabled: true}}}
	cp := webhooks.NewCachingProvider(stub, 5*time.Second, 0)

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		subs, err := cp.ListSubscriptions(ctx, "svc", "audit.write", "")
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if len(subs) != 1 {
			t.Fatalf("call %d: expected 1 subscription, got %d", i, len(subs))
		}
	}
	// Inner provider should only have been called once.
	if stub.callCount() != 1 {
		t.Errorf("inner call count = %d, want 1", stub.callCount())
	}
}

func TestCachingProvider_CacheExpiry(t *testing.T) {
	stub := &stubProvider{subs: []webhooks.Subscription{{ID: "s1"}}}
	// Use a very short TTL so we can test expiry without sleeping long.
	cp := webhooks.NewCachingProvider(stub, 50*time.Millisecond, 0)

	ctx := context.Background()
	if _, err := cp.ListSubscriptions(ctx, "svc", "audit.write", ""); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Wait for the cache entry to expire.
	time.Sleep(100 * time.Millisecond)

	if _, err := cp.ListSubscriptions(ctx, "svc", "audit.write", ""); err != nil {
		t.Fatalf("second call: %v", err)
	}
	// Inner provider should have been called twice (cache miss after expiry).
	if stub.callCount() != 2 {
		t.Errorf("inner call count = %d, want 2", stub.callCount())
	}
}

func TestCachingProvider_ErrorsNotCached(t *testing.T) {
	stub := &stubProvider{err: errors.New("upstream down")}
	cp := webhooks.NewCachingProvider(stub, 5*time.Second, 0)

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, err := cp.ListSubscriptions(ctx, "svc", "audit.write", "")
		if err == nil {
			t.Fatalf("call %d: expected error, got nil", i)
		}
	}
	// Every call should hit the inner provider because errors must never be cached.
	if stub.callCount() != 3 {
		t.Errorf("inner call count = %d, want 3 (errors should not be cached)", stub.callCount())
	}
}

func TestCachingProvider_DifferentKeysAreCachedSeparately(t *testing.T) {
	stub := &stubProvider{subs: []webhooks.Subscription{{ID: "s1"}}}
	cp := webhooks.NewCachingProvider(stub, 5*time.Second, 0)

	ctx := context.Background()
	// Two calls with different eventType — should cause two inner fetches.
	if _, err := cp.ListSubscriptions(ctx, "svc", "audit.write", ""); err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if _, err := cp.ListSubscriptions(ctx, "svc", "artifact.create", ""); err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if stub.callCount() != 2 {
		t.Errorf("inner call count = %d, want 2", stub.callCount())
	}
	// Third call with same key as first — should hit cache.
	if _, err := cp.ListSubscriptions(ctx, "svc", "audit.write", ""); err != nil {
		t.Fatalf("call 3: %v", err)
	}
	if stub.callCount() != 2 {
		t.Errorf("inner call count = %d after cache hit, want still 2", stub.callCount())
	}
}

func TestCachingProvider_TenantIsolation(t *testing.T) {
	stub := &stubProvider{subs: []webhooks.Subscription{{ID: "s1"}}}
	cp := webhooks.NewCachingProvider(stub, 5*time.Second, 0)

	ctx := context.Background()
	// Same service+eventType, different tenantID — must be cached separately.
	if _, err := cp.ListSubscriptions(ctx, "svc", "audit.write", "acme"); err != nil {
		t.Fatalf("acme call: %v", err)
	}
	if _, err := cp.ListSubscriptions(ctx, "svc", "audit.write", "globex"); err != nil {
		t.Fatalf("globex call: %v", err)
	}
	if stub.callCount() != 2 {
		t.Errorf("inner call count = %d, want 2 (one per tenant)", stub.callCount())
	}
}

func TestCachingProvider_Invalidate(t *testing.T) {
	stub := &stubProvider{subs: []webhooks.Subscription{{ID: "s1"}}}
	cp := webhooks.NewCachingProvider(stub, 5*time.Second, 0)

	ctx := context.Background()
	if _, err := cp.ListSubscriptions(ctx, "svc", "audit.write", ""); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Invalidate the cache entry.
	cp.Invalidate("svc", "audit.write", "")
	// Next call should hit the inner provider again.
	if _, err := cp.ListSubscriptions(ctx, "svc", "audit.write", ""); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if stub.callCount() != 2 {
		t.Errorf("inner call count = %d, want 2 after invalidation", stub.callCount())
	}
}

func TestCachingProvider_DefaultTTL(t *testing.T) {
	stub := &stubProvider{subs: []webhooks.Subscription{{ID: "s1"}}}
	// A zero/negative TTL should fall back to the default (30 s) — cache must work.
	cp := webhooks.NewCachingProvider(stub, 0, 0)

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := cp.ListSubscriptions(ctx, "svc", "audit.write", ""); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if stub.callCount() != 1 {
		t.Errorf("inner call count = %d, want 1 (default TTL should cache)", stub.callCount())
	}
}

// TestCachingProvider_BackgroundPollRefreshesCache verifies that the background
// poll goroutine proactively refreshes cached entries before TTL expiry,
// so that updated subscriptions become visible without a cache miss.
func TestCachingProvider_BackgroundPollRefreshesCache(t *testing.T) {
	stub := &stubProvider{subs: []webhooks.Subscription{{ID: "v1"}}}
	// Long TTL so lazy eviction never triggers; short poll so the test is fast.
	pollInterval := 50 * time.Millisecond
	cp := webhooks.NewCachingProvider(stub, 10*time.Second, pollInterval)
	defer cp.Close()

	ctx := context.Background()
	// Populate the cache with v1.
	subs, err := cp.ListSubscriptions(ctx, "svc", "audit.write", "")
	if err != nil {
		t.Fatalf("initial call: %v", err)
	}
	if len(subs) == 0 || subs[0].ID != "v1" {
		t.Fatalf("expected v1, got %v", subs)
	}

	// Change the inner provider to return v2.
	stub.mu.Lock()
	stub.subs = []webhooks.Subscription{{ID: "v2"}}
	stub.mu.Unlock()

	// Wait long enough for at least two poll cycles.
	time.Sleep(3 * pollInterval)

	// The background poller should have refreshed the entry; expect v2.
	subs, err = cp.ListSubscriptions(ctx, "svc", "audit.write", "")
	if err != nil {
		t.Fatalf("post-poll call: %v", err)
	}
	if len(subs) == 0 || subs[0].ID != "v2" {
		t.Fatalf("expected v2 after background poll, got %v", subs)
	}
}

// TestCachingProvider_BackgroundPollErrorPreservesEntry verifies that when the
// inner provider returns an error during a background refresh, the existing
// cache entry is kept (stale-on-error), not evicted.
func TestCachingProvider_BackgroundPollErrorPreservesEntry(t *testing.T) {
	stub := &stubProvider{subs: []webhooks.Subscription{{ID: "stable"}}}
	pollInterval := 50 * time.Millisecond
	cp := webhooks.NewCachingProvider(stub, 10*time.Second, pollInterval)
	defer cp.Close()

	ctx := context.Background()
	// Populate the cache.
	if _, err := cp.ListSubscriptions(ctx, "svc", "audit.write", ""); err != nil {
		t.Fatalf("initial call: %v", err)
	}

	// Make the inner provider fail on subsequent calls.
	stub.mu.Lock()
	stub.err = errors.New("upstream down")
	stub.mu.Unlock()

	// Wait for poll cycles to run.
	time.Sleep(3 * pollInterval)

	// The cached entry must still be there and must return the original data.
	subs, err := cp.ListSubscriptions(ctx, "svc", "audit.write", "")
	if err != nil {
		t.Fatalf("expected stale-cache hit, got error: %v", err)
	}
	if len(subs) == 0 || subs[0].ID != "stable" {
		t.Fatalf("expected stable entry preserved, got %v", subs)
	}
}

// TestCachingProvider_CloseStopsPoller verifies that Close() returns promptly
// and does not block or panic.
func TestCachingProvider_CloseStopsPoller(t *testing.T) {
	stub := &stubProvider{subs: []webhooks.Subscription{{ID: "s1"}}}
	cp := webhooks.NewCachingProvider(stub, 5*time.Second, 10*time.Millisecond)

	done := make(chan struct{})
	go func() {
		cp.Close()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not return within 2 seconds")
	}
}

// TestCachingProvider_CloseNoOpWithoutPoller verifies that calling Close on a
// CachingProvider created without a poll interval is safe (no panic, no block).
func TestCachingProvider_CloseNoOpWithoutPoller(t *testing.T) {
	stub := &stubProvider{}
	cp := webhooks.NewCachingProvider(stub, 5*time.Second, 0)
	cp.Close() // must not panic or block
}
