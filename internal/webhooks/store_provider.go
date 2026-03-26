package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
)

const subscriptionsBucket = "webhook_subscriptions"

// StoreProvider implements Provider by reading webhook subscriptions from a
// local bbolt-backed Store. It is useful for self-managed subscription
// registration without a PhaseMirror-HQ daemon.
//
// Subscriptions are stored as JSON values in the "webhook_subscriptions" bucket,
// keyed by Subscription.ID.
//
// Create instances with NewStoreProvider. The zero value is not usable.
type StoreProvider struct {
	store storage.Store
}

// NewStoreProvider returns a StoreProvider backed by the given Store.
func NewStoreProvider(store storage.Store) *StoreProvider {
	return &StoreProvider{store: store}
}

// ListSubscriptions returns all subscriptions from the local store that match
// the supplied filters. Empty filter values are treated as "no constraint on
// that field".
//
// Filtering is performed in-process after scanning the full bucket. If the
// caller supplies an eventType filter the client-side matchesEventType helper
// in worker_dispatcher.go handles the final check; this method returns all
// subscriptions matching service and tenantID so the dispatcher can apply the
// shared matchesEventType logic uniformly.
func (p *StoreProvider) ListSubscriptions(_ context.Context, service, _ /*eventType*/, tenantID string) ([]Subscription, error) {
	keys, err := p.store.List(subscriptionsBucket)
	if err != nil {
		return nil, fmt.Errorf("webhooks: store: listing subscriptions: %w", err)
	}

	var out []Subscription
	for _, key := range keys {
		data, err := p.store.Get(subscriptionsBucket, key)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				continue // raced with a concurrent delete; skip silently
			}
			return nil, fmt.Errorf("webhooks: store: reading subscription %q: %w", key, err)
		}

		var sub Subscription
		if err := json.Unmarshal(data, &sub); err != nil {
			return nil, fmt.Errorf("webhooks: store: decoding subscription %q: %w", key, err)
		}

		if service != "" && sub.Service != service {
			continue
		}
		if tenantID != "" && sub.TenantID != tenantID {
			continue
		}
		out = append(out, sub)
	}
	return out, nil
}

// Create persists sub in the local store, keyed by sub.ID.
// Returns an error if sub.ID is empty.
func (p *StoreProvider) Create(sub Subscription) error {
	if sub.ID == "" {
		return fmt.Errorf("webhooks: store: subscription ID must not be empty")
	}
	data, err := json.Marshal(sub)
	if err != nil {
		return fmt.Errorf("webhooks: store: encoding subscription: %w", err)
	}
	return p.store.Write(subscriptionsBucket, sub.ID, data)
}

// Get retrieves the subscription with the given id.
// Returns storage.ErrNotFound when no matching subscription exists.
func (p *StoreProvider) Get(id string) (Subscription, error) {
	data, err := p.store.Get(subscriptionsBucket, id)
	if err != nil {
		return Subscription{}, err
	}
	var sub Subscription
	if err := json.Unmarshal(data, &sub); err != nil {
		return Subscription{}, fmt.Errorf("webhooks: store: decoding subscription %q: %w", id, err)
	}
	return sub, nil
}

// Delete removes the subscription with the given id.
// Returns storage.ErrNotFound when no matching subscription exists.
func (p *StoreProvider) Delete(id string) error {
	return p.store.Delete(subscriptionsBucket, id)
}

// compile-time check that *StoreProvider satisfies Provider.
var _ Provider = (*StoreProvider)(nil)
