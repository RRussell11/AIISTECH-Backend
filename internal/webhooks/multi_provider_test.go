package webhooks_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

// errProvider is a Provider that always returns an error.
type errProvider struct{ msg string }

func (e *errProvider) ListSubscriptions(_ context.Context, _, _, _ string) ([]webhooks.Subscription, error) {
	return nil, errors.New(e.msg)
}

// fixedProvider returns a fixed slice of subscriptions.
type fixedProvider struct {
	subs []webhooks.Subscription
}

func (f *fixedProvider) ListSubscriptions(_ context.Context, _, _, _ string) ([]webhooks.Subscription, error) {
	return f.subs, nil
}

func makeSub(id, url, tenantID string) webhooks.Subscription {
	return webhooks.Subscription{
		ID:       id,
		URL:      url,
		TenantID: tenantID,
		Enabled:  true,
	}
}

func TestMultiProvider_MergesTwoProviders(t *testing.T) {
	p1 := &fixedProvider{subs: []webhooks.Subscription{makeSub("s1", "https://a.example.com/hook", "")}}
	p2 := &fixedProvider{subs: []webhooks.Subscription{makeSub("s2", "https://b.example.com/hook", "")}}

	mp := webhooks.NewMultiProvider(p1, p2)
	subs, err := mp.ListSubscriptions(context.Background(), "svc", "", "")
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(subs) != 2 {
		t.Errorf("ListSubscriptions() = %d subs, want 2", len(subs))
	}
}

func TestMultiProvider_DeduplicatesByID(t *testing.T) {
	shared := makeSub("dup-id", "https://shared.example.com/hook", "")
	p1 := &fixedProvider{subs: []webhooks.Subscription{shared}}
	p2 := &fixedProvider{subs: []webhooks.Subscription{shared, makeSub("unique", "https://other.example.com/hook", "")}}

	mp := webhooks.NewMultiProvider(p1, p2)
	subs, err := mp.ListSubscriptions(context.Background(), "", "", "")
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(subs) != 2 {
		t.Errorf("ListSubscriptions() = %d subs after dedup, want 2", len(subs))
	}
	// Verify the shared sub appears only once.
	count := 0
	for _, s := range subs {
		if s.ID == "dup-id" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("dup-id appears %d times, want 1", count)
	}
}

func TestMultiProvider_DeduplicatesByURLAndTenant_WhenNoID(t *testing.T) {
	// Two subscriptions with no ID but the same URL+TenantID should dedup.
	sub := webhooks.Subscription{URL: "https://shared.example.com/hook", TenantID: "t1", Enabled: true}
	p1 := &fixedProvider{subs: []webhooks.Subscription{sub}}
	p2 := &fixedProvider{subs: []webhooks.Subscription{sub}}

	mp := webhooks.NewMultiProvider(p1, p2)
	subs, err := mp.ListSubscriptions(context.Background(), "", "", "")
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(subs) != 1 {
		t.Errorf("ListSubscriptions() = %d subs after URL dedup, want 1", len(subs))
	}
}

func TestMultiProvider_FirstProviderWinsOnDedupe(t *testing.T) {
	// Same ID appears in both providers; the first occurrence should win.
	s1 := makeSub("same-id", "https://first.example.com/hook", "")
	s2 := makeSub("same-id", "https://second.example.com/hook", "") // same ID, different URL

	p1 := &fixedProvider{subs: []webhooks.Subscription{s1}}
	p2 := &fixedProvider{subs: []webhooks.Subscription{s2}}

	mp := webhooks.NewMultiProvider(p1, p2)
	subs, err := mp.ListSubscriptions(context.Background(), "", "", "")
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("ListSubscriptions() = %d subs, want 1", len(subs))
	}
	if subs[0].URL != "https://first.example.com/hook" {
		t.Errorf("first provider should win: got URL %q, want %q", subs[0].URL, "https://first.example.com/hook")
	}
}

func TestMultiProvider_SkipsFailingProvider(t *testing.T) {
	good := &fixedProvider{subs: []webhooks.Subscription{makeSub("s1", "https://good.example.com/hook", "")}}
	bad := &errProvider{msg: "remote unavailable"}

	mp := webhooks.NewMultiProvider(bad, good)
	subs, err := mp.ListSubscriptions(context.Background(), "", "", "")
	if err != nil {
		t.Fatalf("ListSubscriptions() should not propagate provider error, got %v", err)
	}
	if len(subs) != 1 {
		t.Errorf("ListSubscriptions() = %d subs, want 1 (from good provider)", len(subs))
	}
}

func TestMultiProvider_AllProvidersFailReturnsEmpty(t *testing.T) {
	mp := webhooks.NewMultiProvider(
		&errProvider{msg: "err1"},
		&errProvider{msg: "err2"},
	)
	subs, err := mp.ListSubscriptions(context.Background(), "", "", "")
	if err != nil {
		t.Fatalf("ListSubscriptions() should not return error when all providers fail, got %v", err)
	}
	if len(subs) != 0 {
		t.Errorf("ListSubscriptions() = %d subs, want 0", len(subs))
	}
}

func TestMultiProvider_EmptyProviderList(t *testing.T) {
	mp := webhooks.NewMultiProvider()
	subs, err := mp.ListSubscriptions(context.Background(), "", "", "")
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(subs) != 0 {
		t.Errorf("ListSubscriptions() on empty MultiProvider = %d subs, want 0", len(subs))
	}
}

func TestMultiProvider_ManyProviders(t *testing.T) {
	const n = 10
	providers := make([]webhooks.Provider, n)
	for i := range n {
		providers[i] = &fixedProvider{subs: []webhooks.Subscription{
			makeSub(fmt.Sprintf("sub-%d", i), fmt.Sprintf("https://%d.example.com/hook", i), ""),
		}}
	}

	mp := webhooks.NewMultiProvider(providers...)
	subs, err := mp.ListSubscriptions(context.Background(), "", "", "")
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(subs) != n {
		t.Errorf("ListSubscriptions() = %d subs, want %d", len(subs), n)
	}
}
