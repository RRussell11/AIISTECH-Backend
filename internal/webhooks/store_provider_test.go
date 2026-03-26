package webhooks_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

// openTestStore opens (or creates) a temporary bbolt database for testing
// and registers cleanup to close it after the test.
func openTestStore(t *testing.T) storage.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newTestSubscription(id, service, url string, events []string) webhooks.Subscription {
	return webhooks.Subscription{
		ID:        id,
		Service:   service,
		URL:       url,
		Enabled:   true,
		Events:    events,
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}
}

func TestStoreProvider_CreateAndGet(t *testing.T) {
	p := webhooks.NewStoreProvider(openTestStore(t))

	sub := newTestSubscription("sub-1", "svc", "https://example.com/hook", []string{"audit.write"})
	if err := p.Create(sub); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := p.Get(sub.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != sub.ID {
		t.Errorf("Get() ID = %q, want %q", got.ID, sub.ID)
	}
	if got.URL != sub.URL {
		t.Errorf("Get() URL = %q, want %q", got.URL, sub.URL)
	}
}

func TestStoreProvider_Create_EmptyIDError(t *testing.T) {
	p := webhooks.NewStoreProvider(openTestStore(t))
	sub := newTestSubscription("", "svc", "https://example.com/hook", nil)
	err := p.Create(sub)
	if err == nil {
		t.Error("Create() with empty ID: expected error, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "ID must not be empty") {
		t.Errorf("Create() with empty ID: error %q does not mention 'ID must not be empty'", err)
	}
}

func TestStoreProvider_Delete(t *testing.T) {
	p := webhooks.NewStoreProvider(openTestStore(t))
	sub := newTestSubscription("sub-del", "svc", "https://example.com/hook", nil)
	if err := p.Create(sub); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := p.Delete(sub.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, err := p.Get(sub.ID)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Get() after Delete: want ErrNotFound, got %v", err)
	}
}

func TestStoreProvider_Delete_NotFound(t *testing.T) {
	p := webhooks.NewStoreProvider(openTestStore(t))
	if err := p.Delete("nonexistent"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Delete() nonexistent: want ErrNotFound, got %v", err)
	}
}

func TestStoreProvider_ListSubscriptions_ServiceFilter(t *testing.T) {
	p := webhooks.NewStoreProvider(openTestStore(t))

	_ = p.Create(newTestSubscription("s1", "svc-a", "https://a.example.com/hook", nil))
	_ = p.Create(newTestSubscription("s2", "svc-b", "https://b.example.com/hook", nil))

	subs, err := p.ListSubscriptions(context.Background(), "svc-a", "", "")
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("ListSubscriptions() returned %d subs, want 1", len(subs))
	}
	if subs[0].ID != "s1" {
		t.Errorf("ListSubscriptions() ID = %q, want %q", subs[0].ID, "s1")
	}
}

func TestStoreProvider_ListSubscriptions_NoFilter(t *testing.T) {
	p := webhooks.NewStoreProvider(openTestStore(t))

	_ = p.Create(newTestSubscription("s1", "svc-a", "https://a.example.com/hook", nil))
	_ = p.Create(newTestSubscription("s2", "svc-b", "https://b.example.com/hook", nil))

	subs, err := p.ListSubscriptions(context.Background(), "", "", "")
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(subs) != 2 {
		t.Errorf("ListSubscriptions() returned %d subs, want 2", len(subs))
	}
}

func TestStoreProvider_ListSubscriptions_TenantFilter(t *testing.T) {
	p := webhooks.NewStoreProvider(openTestStore(t))

	s1 := newTestSubscription("s1", "svc", "https://a.example.com/hook", nil)
	s1.TenantID = "tenant-x"
	s2 := newTestSubscription("s2", "svc", "https://b.example.com/hook", nil)
	s2.TenantID = "tenant-y"
	_ = p.Create(s1)
	_ = p.Create(s2)

	subs, err := p.ListSubscriptions(context.Background(), "svc", "", "tenant-x")
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(subs) != 1 || subs[0].ID != "s1" {
		t.Errorf("ListSubscriptions() tenantID filter: got %v, want [s1]", subs)
	}
}

func TestStoreProvider_ListSubscriptions_Empty(t *testing.T) {
	p := webhooks.NewStoreProvider(openTestStore(t))
	subs, err := p.ListSubscriptions(context.Background(), "svc", "", "")
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(subs) != 0 {
		t.Errorf("ListSubscriptions() on empty store: got %d subs, want 0", len(subs))
	}
}

func TestStoreProvider_ListSubscriptions_TempFile(t *testing.T) {
	// Ensure the store works with a tmp file path and cleans up properly.
	f, err := os.CreateTemp(t.TempDir(), "*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	_ = os.Remove(f.Name()) // bbolt will recreate it

	s, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer s.Close()

	p := webhooks.NewStoreProvider(s)
	_ = p.Create(newTestSubscription("x", "svc", "https://x.example.com/hook", nil))

	subs, err := p.ListSubscriptions(context.Background(), "svc", "", "")
	if err != nil {
		t.Fatalf("ListSubscriptions: %v", err)
	}
	if len(subs) != 1 {
		t.Errorf("want 1 sub, got %d", len(subs))
	}
}
