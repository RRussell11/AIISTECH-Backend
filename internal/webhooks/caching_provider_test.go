package webhooks

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// countingProvider is a test double that records the number of
// ListSubscriptions calls and returns a fixed result or error.
type countingProvider struct {
	calls int64
	subs  []Subscription
	err   error
}

func (p *countingProvider) ListSubscriptions(_ context.Context, _, _, _ string) ([]Subscription, error) {
	atomic.AddInt64(&p.calls, 1)
	return p.subs, p.err
}

// TestCachingProvider_CachesOnHit verifies that repeated calls with the same
// arguments only invoke the inner provider once (cache hit for calls 2+).
func TestCachingProvider_CachesOnHit(t *testing.T) {
	inner := &countingProvider{
		subs: []Subscription{{ID: "sub-1", Enabled: true}},
	}
	cp := NewCachingProvider(inner, 30*time.Second)

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
	cp := NewCachingProvider(inner, 10*time.Millisecond) // very short TTL

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
	cp := NewCachingProvider(inner, 30*time.Second)

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
	cp := NewCachingProvider(inner, 30*time.Second)

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
	cp := NewCachingProvider(inner, 0)
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
	cp := NewCachingProvider(inner, time.Hour)

	first, _ := cp.ListSubscriptions(context.Background(), "a", "b", "c")
	second, _ := cp.ListSubscriptions(context.Background(), "a", "b", "c")

	if len(first) != 2 || len(second) != 2 {
		t.Errorf("unexpected subscription counts: first=%d second=%d", len(first), len(second))
	}
	if got := atomic.LoadInt64(&inner.calls); got != 1 {
		t.Errorf("inner called %d times, want 1", got)
	}
}
