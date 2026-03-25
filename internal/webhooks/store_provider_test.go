package webhooks_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

// newStoreProvider opens a fresh per-site bbolt store in a temp directory and
// returns a StoreProvider backed by it.  The store is closed when the test
// finishes.
func newStoreProvider(t *testing.T) *webhooks.StoreProvider {
	t.Helper()
	t.Chdir(t.TempDir())
	stores := openTestStores(t)
	t.Cleanup(func() { stores.CloseAll() })
	store, err := stores.Open("local")
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	return webhooks.NewStoreProvider(store)
}

func TestStoreProvider_CreateAndList(t *testing.T) {
	ctx := context.Background()
	p := newStoreProvider(t)

	sub := webhooks.Subscription{
		URL:     "https://example.com/hook",
		Service: "svc-a",
		Events:  []string{"audit.write"},
		Enabled: true,
	}
	created, err := p.Create(ctx, sub)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID == "" {
		t.Error("Create() returned subscription with empty ID")
	}
	if created.CreatedAt.IsZero() {
		t.Error("Create() did not set CreatedAt")
	}
	if created.UpdatedAt.IsZero() {
		t.Error("Create() did not set UpdatedAt")
	}

	// ListSubscriptions with no filters should return the subscription.
	subs, err := p.ListSubscriptions(ctx, "", "", "")
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("ListSubscriptions() returned %d subscriptions, want 1", len(subs))
	}
	if subs[0].URL != "https://example.com/hook" {
		t.Errorf("URL = %q, want https://example.com/hook", subs[0].URL)
	}
}

func TestStoreProvider_CreateSetsID(t *testing.T) {
	ctx := context.Background()
	p := newStoreProvider(t)

	s1, _ := p.Create(ctx, webhooks.Subscription{URL: "https://a.example.com", Service: "svc", Events: []string{"x"}})
	s2, _ := p.Create(ctx, webhooks.Subscription{URL: "https://b.example.com", Service: "svc", Events: []string{"x"}})

	if s1.ID == "" || s2.ID == "" {
		t.Fatal("IDs must be non-empty")
	}
	if s1.ID == s2.ID {
		t.Errorf("two Create calls produced the same ID: %q", s1.ID)
	}
}

func TestStoreProvider_GetByID(t *testing.T) {
	ctx := context.Background()
	p := newStoreProvider(t)

	created, _ := p.Create(ctx, webhooks.Subscription{
		URL:     "https://example.com/hook",
		Service: "svc",
		Events:  []string{"audit.write"},
	})

	got, err := p.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("Get() ID = %q, want %q", got.ID, created.ID)
	}
	if got.URL != created.URL {
		t.Errorf("Get() URL = %q, want %q", got.URL, created.URL)
	}
}

func TestStoreProvider_GetNotFound(t *testing.T) {
	ctx := context.Background()
	p := newStoreProvider(t)

	_, err := p.Get(ctx, "nonexistent.json")
	if err == nil {
		t.Fatal("Get() with unknown ID should return an error")
	}
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Get() error should wrap storage.ErrNotFound, got %v", err)
	}
}

func TestStoreProvider_DeleteRemovesEntry(t *testing.T) {
	ctx := context.Background()
	p := newStoreProvider(t)

	created, _ := p.Create(ctx, webhooks.Subscription{
		URL:     "https://example.com/hook",
		Service: "svc",
		Events:  []string{"e"},
	})

	if err := p.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	subs, _ := p.ListSubscriptions(ctx, "", "", "")
	if len(subs) != 0 {
		t.Errorf("ListSubscriptions() after delete = %d, want 0", len(subs))
	}
}

func TestStoreProvider_DeleteNotFound(t *testing.T) {
	ctx := context.Background()
	p := newStoreProvider(t)

	err := p.Delete(ctx, "nonexistent.json")
	if err == nil {
		t.Fatal("Delete() with unknown ID should return an error")
	}
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Delete() error should wrap storage.ErrNotFound, got %v", err)
	}
}

func TestStoreProvider_ListSubscriptions_FilterByService(t *testing.T) {
	ctx := context.Background()
	p := newStoreProvider(t)

	p.Create(ctx, webhooks.Subscription{URL: "https://a.example.com", Service: "svc-a", Events: []string{"e"}})
	p.Create(ctx, webhooks.Subscription{URL: "https://b.example.com", Service: "svc-b", Events: []string{"e"}})

	subs, err := p.ListSubscriptions(ctx, "svc-a", "", "")
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("ListSubscriptions(svc-a) = %d, want 1", len(subs))
	}
	if subs[0].Service != "svc-a" {
		t.Errorf("Service = %q, want svc-a", subs[0].Service)
	}
}

func TestStoreProvider_ListSubscriptions_FilterByEventType(t *testing.T) {
	ctx := context.Background()
	p := newStoreProvider(t)

	p.Create(ctx, webhooks.Subscription{URL: "https://a.example.com", Service: "svc", Events: []string{"audit.write"}})
	p.Create(ctx, webhooks.Subscription{URL: "https://b.example.com", Service: "svc", Events: []string{"other.event"}})

	subs, err := p.ListSubscriptions(ctx, "", "audit.write", "")
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("ListSubscriptions(audit.write) = %d, want 1", len(subs))
	}
	if subs[0].URL != "https://a.example.com" {
		t.Errorf("URL = %q, want https://a.example.com", subs[0].URL)
	}
}

func TestStoreProvider_ListSubscriptions_FilterByTenantID(t *testing.T) {
	ctx := context.Background()
	p := newStoreProvider(t)

	p.Create(ctx, webhooks.Subscription{URL: "https://a.example.com", Service: "svc", Events: []string{"e"}, TenantID: "tenant-1"})
	p.Create(ctx, webhooks.Subscription{URL: "https://b.example.com", Service: "svc", Events: []string{"e"}, TenantID: "tenant-2"})

	subs, err := p.ListSubscriptions(ctx, "", "", "tenant-1")
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("ListSubscriptions(tenant-1) = %d, want 1", len(subs))
	}
	if subs[0].TenantID != "tenant-1" {
		t.Errorf("TenantID = %q, want tenant-1", subs[0].TenantID)
	}
}

func TestStoreProvider_IsNotFound(t *testing.T) {
	if !webhooks.IsNotFound(storage.ErrNotFound) {
		t.Error("IsNotFound(storage.ErrNotFound) = false, want true")
	}
	if webhooks.IsNotFound(nil) {
		t.Error("IsNotFound(nil) = true, want false")
	}
	if webhooks.IsNotFound(errors.New("unrelated error")) {
		t.Error("IsNotFound(unrelated) = true, want false")
	}
}

func TestStoreProvider_MultipleCreates_LexicographicOrder(t *testing.T) {
	ctx := context.Background()
	p := newStoreProvider(t)

	const n = 5
	ids := make([]string, n)
	for i := range n {
		sub, err := p.Create(ctx, webhooks.Subscription{
			URL:     "https://example.com/hook",
			Service: "svc",
			Events:  []string{"e"},
		})
		if err != nil {
			t.Fatalf("Create() #%d error = %v", i, err)
		}
		ids[i] = sub.ID
		// tiny sleep so nanosecond timestamps differ reliably
		time.Sleep(time.Microsecond)
	}

	subs, err := p.ListSubscriptions(ctx, "", "", "")
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(subs) != n {
		t.Fatalf("ListSubscriptions() = %d, want %d", len(subs), n)
	}
	// bbolt returns keys in lexicographic (= chronological for zero-padded ns) order.
	for i := 1; i < len(subs); i++ {
		if subs[i].ID <= subs[i-1].ID {
			t.Errorf("subscriptions not in order: subs[%d].ID=%q <= subs[%d].ID=%q", i, subs[i].ID, i-1, subs[i-1].ID)
		}
	}
}
