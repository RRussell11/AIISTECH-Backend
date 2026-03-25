package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
)

// SubscriptionsBucket is the bbolt bucket name used to persist local webhook
// subscriptions written via the subscription management HTTP API (ADR-034).
const SubscriptionsBucket = "webhook_subscriptions"

// subSeq makes subscription storage keys unique even when UnixNano returns
// duplicate values (common in CI or on systems with low clock resolution).
var subSeq uint64

// StoreProvider is a bbolt-backed Provider that reads webhook subscriptions
// from a site-local store rather than calling the PhaseMirror-HQ daemon API.
//
// In addition to the read-only Provider interface it exposes Create, Get, and
// Delete methods used by the subscription management HTTP handlers so that the
// subscription store operations are centralised in one place.
//
// StoreProvider is safe for concurrent use.
type StoreProvider struct {
	store storage.Store
}

// NewStoreProvider returns a StoreProvider backed by store.
func NewStoreProvider(store storage.Store) *StoreProvider {
	return &StoreProvider{store: store}
}

// ListSubscriptions implements Provider.
//
// It returns all subscriptions in SubscriptionsBucket that match the supplied
// filters.  An empty string for service, eventType, or tenantID means "no
// filter on that field".  Entries that cannot be decoded are silently skipped.
func (p *StoreProvider) ListSubscriptions(_ context.Context, service, eventType, tenantID string) ([]Subscription, error) {
	keys, err := p.store.List(SubscriptionsBucket)
	if err != nil {
		return nil, fmt.Errorf("store_provider: list: %w", err)
	}

	subs := make([]Subscription, 0, len(keys))
	for _, key := range keys {
		raw, err := p.store.Get(SubscriptionsBucket, key)
		if err != nil {
			continue // tolerate races or corruption
		}
		var s Subscription
		if err := json.Unmarshal(raw, &s); err != nil {
			continue
		}
		if service != "" && s.Service != service {
			continue
		}
		if tenantID != "" && s.TenantID != tenantID {
			continue
		}
		if eventType != "" && !containsEvent(s.Events, eventType) {
			continue
		}
		subs = append(subs, s)
	}
	return subs, nil
}

// Create assigns an ID, CreatedAt, and UpdatedAt to sub, persists it in
// SubscriptionsBucket, and returns the stored subscription.
//
// The generated ID has the form "<zero-padded-nanoseconds>-<seq>.json",
// matching the DLQ key format so that lexicographic order tracks insertion
// order.
func (p *StoreProvider) Create(_ context.Context, sub Subscription) (Subscription, error) {
	ns := time.Now().UnixNano()
	seq := atomic.AddUint64(&subSeq, 1)
	id := fmt.Sprintf("%020d-%d.json", ns, seq)

	now := time.Now().UTC()
	sub.ID = id
	sub.CreatedAt = now
	sub.UpdatedAt = now

	data, err := json.Marshal(sub)
	if err != nil {
		return Subscription{}, fmt.Errorf("store_provider: marshal: %w", err)
	}
	if err := p.store.Write(SubscriptionsBucket, id, data); err != nil {
		return Subscription{}, fmt.Errorf("store_provider: write: %w", err)
	}
	return sub, nil
}

// Get returns the subscription identified by id.
// Returns storage.ErrNotFound (wrapped) when no such subscription exists.
func (p *StoreProvider) Get(_ context.Context, id string) (Subscription, error) {
	raw, err := p.store.Get(SubscriptionsBucket, id)
	if err != nil {
		return Subscription{}, fmt.Errorf("store_provider: get %q: %w", id, err)
	}
	var sub Subscription
	if err := json.Unmarshal(raw, &sub); err != nil {
		return Subscription{}, fmt.Errorf("store_provider: unmarshal %q: %w", id, err)
	}
	return sub, nil
}

// Delete removes the subscription identified by id.
// Returns storage.ErrNotFound (wrapped) when no such subscription exists.
func (p *StoreProvider) Delete(_ context.Context, id string) error {
	if err := p.store.Delete(SubscriptionsBucket, id); err != nil {
		return fmt.Errorf("store_provider: delete %q: %w", id, err)
	}
	return nil
}

// SubscriptionPatch carries the optional fields accepted by Update.
// A nil pointer field means "no change"; a non-nil pointer field
// (or non-empty slice) replaces the existing value.
type SubscriptionPatch struct {
	URL      *string   // nil = keep existing
	Service  *string   // nil = keep existing
	Events   []string  // nil = keep existing; non-nil (even empty) replaces
	Secret   *string   // nil = keep existing
	TenantID *string   // nil = keep existing
	Enabled  *bool     // nil = keep existing
}

// Update reads the subscription identified by id, applies the non-nil fields
// from patch, bumps UpdatedAt, and overwrites the stored record in-place.
// The ID and CreatedAt are always preserved.
// Returns storage.ErrNotFound (wrapped) when no such subscription exists.
func (p *StoreProvider) Update(ctx context.Context, id string, patch SubscriptionPatch) (Subscription, error) {
	existing, err := p.Get(ctx, id)
	if err != nil {
		return Subscription{}, fmt.Errorf("store_provider: update get %q: %w", id, err)
	}

	if patch.URL != nil {
		existing.URL = *patch.URL
	}
	if patch.Service != nil {
		existing.Service = *patch.Service
	}
	if patch.Events != nil {
		existing.Events = patch.Events
	}
	if patch.Secret != nil {
		existing.Secret = *patch.Secret
	}
	if patch.TenantID != nil {
		existing.TenantID = *patch.TenantID
	}
	if patch.Enabled != nil {
		existing.Enabled = *patch.Enabled
	}
	existing.UpdatedAt = time.Now().UTC()

	data, err := json.Marshal(existing)
	if err != nil {
		return Subscription{}, fmt.Errorf("store_provider: update marshal %q: %w", id, err)
	}
	if err := p.store.Write(SubscriptionsBucket, id, data); err != nil {
		return Subscription{}, fmt.Errorf("store_provider: update write %q: %w", id, err)
	}
	return existing, nil
}

// containsEvent reports whether eventType appears in the events slice.
func containsEvent(events []string, eventType string) bool {
	for _, e := range events {
		if e == eventType {
			return true
		}
	}
	return false
}

// IsNotFound reports whether err wraps storage.ErrNotFound.
// Convenience helper for callers that want to translate the error to HTTP 404.
func IsNotFound(err error) bool {
	return errors.Is(err, storage.ErrNotFound)
}

// compile-time check that *StoreProvider satisfies Provider.
var _ Provider = (*StoreProvider)(nil)
