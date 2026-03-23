package webhooks

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingProvider is a test double that records the number of
// ListSubscriptions calls and returns a fixed result or error.
type countingProvider struct {
	mu    sync.Mutex
	calls int64
	subs  []Subscription
	err   error
}

func (p *countingProvider) ListSubscriptions(_ context.Context, _, _, _ string) ([]Subscription, error) {
	atomic.AddInt64(&p.calls, 1)
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.subs, p.err
}

// setError safely sets the error returned by future ListSubscriptions calls.
// Use this instead of assigning p.err directly when a background goroutine
// may be calling ListSubscriptions concurrently.
func (p *countingProvider) setError(err error) {
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
}

// TestCachingProvider_CachesOnHit verifies that repeated calls with the same
// arguments only invoke the inner provider once (cache hit for calls 2+).
func TestCachingProvider_CachesOnHit(t *testing.T) {
	inner := &countingProvider{
		subs: []Subscription{{ID: "sub-1", Enabled: true}},
	}
	cp := NewCachingProvider(inner, 30*time.Second, 0)

	for i := 0; i < 3; i++ {
		subs, err := cp.ListSubscriptions(context.Background(), "svc", "audit.write", "")
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if len(subs) != 1 || subs[0].ID != "sub-1" {
			t.Fatalf("call %d: unexpected subscriptions: %v", i, subs)
		}
	}

	if got := atomic.LoadInt64(&inner.calls); got != 1 {
		t.Errorf("inner called %d times, want 1 (cache hit for calls 2 and 3)", got)
	}
}

// TestCachingProvider_ExpiredEntryRefetches verifies that a cache entry that
// has passed its TTL is discarded and the inner provider is called again.
func TestCachingProvider_ExpiredEntryRefetches(t *testing.T) {
	inner := &countingProvider{
		subs: []Subscription{{ID: "sub-1", Enabled: true}},
	}
	cp := NewCachingProvider(inner, 10*time.Millisecond, 0) // very short TTL

	if _, err := cp.ListSubscriptions(context.Background(), "svc", "audit.write", ""); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Wait long enough for the entry to expire.
	time.Sleep(20 * time.Millisecond)

	if _, err := cp.ListSubscriptions(context.Background(), "svc", "audit.write", ""); err != nil {
		t.Fatalf("second call: %v", err)
	}

	if got := atomic.LoadInt64(&inner.calls); got != 2 {
		t.Errorf("inner called %d times, want 2 (re-fetch after expiry)", got)
	}
}

// TestCachingProvider_DifferentKeysNotShared verifies that distinct
// (service, eventType, tenantID) tuples maintain independent cache entries.
func TestCachingProvider_DifferentKeysNotShared(t *testing.T) {
	inner := &countingProvider{
		subs: []Subscription{{ID: "sub-1", Enabled: true}},
	}
	cp := NewCachingProvider(inner, 30*time.Second, 0)

	if _, err := cp.ListSubscriptions(context.Background(), "svc", "audit.write", ""); err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if _, err := cp.ListSubscriptions(context.Background(), "svc", "artifact.delete", ""); err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if _, err := cp.ListSubscriptions(context.Background(), "svc", "audit.write", "tenant-1"); err != nil {
		t.Fatalf("call 3: %v", err)
	}

	if got := atomic.LoadInt64(&inner.calls); got != 3 {
		t.Errorf("inner called %d times, want 3 (each distinct key is a cache miss)", got)
	}
}

// TestCachingProvider_ErrorNotCached verifies that errors returned by the inner
// provider are not stored in the cache, so subsequent calls retry immediately.
func TestCachingProvider_ErrorNotCached(t *testing.T) {
	inner := &countingProvider{
		err: errors.New("upstream unavailable"),
	}
	cp := NewCachingProvider(inner, 30*time.Second, 0)

	for i := 0; i < 3; i++ {
		if _, err := cp.ListSubscriptions(context.Background(), "svc", "audit.write", ""); err == nil {
			t.Fatalf("call %d: expected error, got nil", i)
		}
	}

	if got := atomic.LoadInt64(&inner.calls); got != 3 {
		t.Errorf("inner called %d times, want 3 (errors must not be cached)", got)
	}
}

// TestCachingProvider_DefaultTTL verifies that a zero TTL argument results in
// the 30-second default being applied.
func TestCachingProvider_DefaultTTL(t *testing.T) {
	inner := &countingProvider{}
	cp := NewCachingProvider(inner, 0, 0)
	if cp.ttl != 30*time.Second {
		t.Errorf("default TTL = %v, want 30s", cp.ttl)
	}
}

// TestCachingProvider_SameKeyAfterSuccessIsHit verifies that after a
// successful fetch the cache is hit on the very next call with the same key.
func TestCachingProvider_SameKeyAfterSuccessIsHit(t *testing.T) {
	inner := &countingProvider{
		subs: []Subscription{{ID: "s1"}, {ID: "s2"}},
	}
	cp := NewCachingProvider(inner, time.Hour, 0)

	first, _ := cp.ListSubscriptions(context.Background(), "a", "b", "c")
	second, _ := cp.ListSubscriptions(context.Background(), "a", "b", "c")

	if len(first) != 2 || len(second) != 2 {
		t.Errorf("unexpected subscription counts: first=%d second=%d", len(first), len(second))
	}
	if got := atomic.LoadInt64(&inner.calls); got != 1 {
		t.Errorf("inner called %d times, want 1", got)
	}
}

// TestCachingProvider_BackgroundPollRefreshesCache verifies that when a
// positive pollInterval is provided the background goroutine re-fetches known
// cache keys without requiring an explicit ListSubscriptions call.
func TestCachingProvider_BackgroundPollRefreshesCache(t *testing.T) {
	inner := &countingProvider{
		subs: []Subscription{{ID: "sub-1", Enabled: true}},
	}
	cp := NewCachingProvider(inner, time.Hour, 5*time.Millisecond)
	t.Cleanup(func() {
		if err := cp.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	// Trigger the initial lazy miss to populate the cache key.
	if _, err := cp.ListSubscriptions(context.Background(), "svc", "audit.write", ""); err != nil {
		t.Fatalf("initial fetch: %v", err)
	}
	afterFirst := atomic.LoadInt64(&inner.calls) // should be 1

	// Wait for several poll cycles. The background goroutine should have
	// re-fetched the subscription at least once more.
	time.Sleep(40 * time.Millisecond)

	if got := atomic.LoadInt64(&inner.calls); got <= afterFirst {
		t.Errorf("inner calls after poll wait = %d, want > %d (background goroutine should have polled)", got, afterFirst)
	}
}

// TestCachingProvider_CloseStopsPoller verifies that after Close returns the
// background goroutine has stopped and the inner provider is no longer called.
func TestCachingProvider_CloseStopsPoller(t *testing.T) {
	inner := &countingProvider{
		subs: []Subscription{{ID: "sub-1", Enabled: true}},
	}
	cp := NewCachingProvider(inner, time.Hour, 5*time.Millisecond)

	// Populate the cache so the poller has a key to refresh.
	if _, err := cp.ListSubscriptions(context.Background(), "svc", "audit.write", ""); err != nil {
		t.Fatalf("initial fetch: %v", err)
	}

	// Allow a few poll cycles to confirm the goroutine is running.
	time.Sleep(30 * time.Millisecond)

	if err := cp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Snapshot the call count immediately after Close.
	snapshot := atomic.LoadInt64(&inner.calls)

	// Wait another interval and verify that the count has not increased.
	time.Sleep(30 * time.Millisecond)
	if got := atomic.LoadInt64(&inner.calls); got != snapshot {
		t.Errorf("inner calls after Close: got %d, want %d (goroutine should be stopped)", got, snapshot)
	}
}

// TestCachingProvider_SingleflightCoalescesOnCacheMiss verifies that when many
// goroutines concurrently observe a cache miss for the same key, the inner
// provider is called exactly once (all callers share the single in-flight
// result via singleflight).
func TestCachingProvider_SingleflightCoalescesOnCacheMiss(t *testing.T) {
	const goroutines = 20

	// gate holds the inner call open until all goroutines have entered sf.Do.
	gate := make(chan struct{})
	inner := &gatedProvider{
		subs: []Subscription{{ID: "sf-sub", Enabled: true}},
		gate: gate,
	}
	cp := NewCachingProvider(inner, time.Hour, 0)

	// Release all goroutines at the same moment so they all race on the same
	// cache key before the first inner call has had a chance to return.
	ready := make(chan struct{})
	var wg sync.WaitGroup
	results := make([][]Subscription, goroutines)
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ready
			results[i], errs[i] = cp.ListSubscriptions(context.Background(), "svc", "audit.write", "")
		}()
	}

	close(ready)                      // release all goroutines simultaneously
	time.Sleep(20 * time.Millisecond) // let them pile up inside sf.Do
	close(gate)                       // allow the single in-flight call to complete

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: unexpected error: %v", i, err)
		}
	}
	for i, subs := range results {
		if len(subs) != 1 || subs[0].ID != "sf-sub" {
			t.Errorf("goroutine %d: unexpected result: %v", i, subs)
		}
	}
	// The inner provider should have been called exactly once despite the
	// many concurrent callers — that is the whole point of singleflight.
	if got := atomic.LoadInt64(&inner.calls); got != 1 {
		t.Errorf("inner called %d times, want 1 (singleflight should coalesce concurrent misses)", got)
	}
}

// gatedProvider is a test Provider that blocks every ListSubscriptions call
// until its gate channel is closed, allowing concurrent callers to pile up
// inside singleflight before the result is available.
type gatedProvider struct {
	calls int64
	subs  []Subscription
	gate  <-chan struct{}
}

func (p *gatedProvider) ListSubscriptions(_ context.Context, _, _, _ string) ([]Subscription, error) {
	atomic.AddInt64(&p.calls, 1)
	<-p.gate
	return p.subs, nil
}

// TestCachingProvider_PollErrorDoesNotEvictValidEntry verifies that when the
// background poller receives an error from the inner provider it does not
// evict or overwrite the existing valid cache entry.
func TestCachingProvider_PollErrorDoesNotEvictValidEntry(t *testing.T) {
	inner := &countingProvider{
		subs: []Subscription{{ID: "sub-1", Enabled: true}},
	}
	// Long TTL so the lazy entry remains valid throughout the test.
	cp := NewCachingProvider(inner, time.Hour, 5*time.Millisecond)
	t.Cleanup(func() {
		if err := cp.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	// Populate the cache.
	subs, err := cp.ListSubscriptions(context.Background(), "svc", "audit.write", "")
	if err != nil || len(subs) == 0 {
		t.Fatalf("initial fetch: err=%v subs=%v", err, subs)
	}

	// Make the inner provider return an error for all subsequent calls.
	inner.setError(errors.New("hq unavailable"))

	// Wait for at least one poll cycle to attempt a refresh and fail.
	time.Sleep(30 * time.Millisecond)

	// Clear the error so it does not interfere with the ListSubscriptions call below.
	inner.setError(nil)

	// The cache entry should still be valid (TTL=1h, error not cached).
	subs2, err := cp.ListSubscriptions(context.Background(), "svc", "audit.write", "")
	if err != nil {
		t.Fatalf("ListSubscriptions after poll error: %v", err)
	}
	if len(subs2) == 0 || subs2[0].ID != "sub-1" {
		t.Errorf("unexpected subscriptions after poll error: %v", subs2)
	}
}
