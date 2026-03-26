package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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

// ListPage returns up to limit subscriptions starting strictly after cursor.
// Pass cursor="" to start from the beginning.
// nextCursor is the last key returned; pass it as cursor on the next call.
// nextCursor is "" when there are no further subscriptions.
func (p *StoreProvider) ListPage(cursor string, limit int) ([]Subscription, string, error) {
	keys, nextCursor, err := p.store.ListPage(subscriptionsBucket, cursor, limit)
	if err != nil {
		return nil, "", fmt.Errorf("webhooks: store: listing subscriptions page: %w", err)
	}

	out := make([]Subscription, 0, len(keys))
	for _, key := range keys {
		data, err := p.store.Get(subscriptionsBucket, key)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				continue // raced with a concurrent delete
			}
			return nil, "", fmt.Errorf("webhooks: store: reading subscription %q: %w", key, err)
		}
		var sub Subscription
		if err := json.Unmarshal(data, &sub); err != nil {
			return nil, "", fmt.Errorf("webhooks: store: decoding subscription %q: %w", key, err)
		}
		out = append(out, sub)
	}
	return out, nextCursor, nil
}

// Create persists sub in the local store, keyed by sub.ID.
// If sub.ID is empty a nanosecond-timestamped key is generated and assigned to
// sub.ID before writing. Sets CreatedAt and UpdatedAt to now if they are zero.
func (p *StoreProvider) Create(sub *Subscription) error {
	if sub.ID == "" {
		sub.ID = fmt.Sprintf("sub_%d", time.Now().UnixNano())
	}
	now := time.Now().UTC()
	if sub.CreatedAt.IsZero() {
		sub.CreatedAt = now
	}
	if sub.UpdatedAt.IsZero() {
		sub.UpdatedAt = now
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

// SubscriptionPatch carries the fields to update in a PATCH request.
// A nil pointer field means "keep the current value". A non-nil pointer
// field replaces the current value. Events is handled specially: a nil
// slice means "keep existing events"; a non-nil (even empty) slice replaces.
type SubscriptionPatch struct {
	URL      *string  `json:"url,omitempty"`
	Enabled  *bool    `json:"enabled,omitempty"`
	Events   []string `json:"events"`
	Secret   *string  `json:"secret,omitempty"`
	TenantID *string  `json:"tenant_id,omitempty"`

	// eventsPresent is set by UnmarshalJSON to distinguish "events": null
	// (absent / not provided) from "events": [] (explicitly empty).
	eventsPresent bool
}

// SetEvents marks the Events field as explicitly present (as if "events" was
// included in the JSON body), causing Update to replace the existing events
// list. This helper is intended for use in Go code rather than via JSON
// deserialisation.
func (p *SubscriptionPatch) SetEvents(events []string) {
	p.Events = events
	p.eventsPresent = true
}

// UnmarshalJSON customises decoding so that an absent "events" key is
// treated as "keep existing" (eventsPresent=false), while a present "events"
// key (even null or []) triggers a replacement (eventsPresent=true).
func (p *SubscriptionPatch) UnmarshalJSON(b []byte) error {
	// Use an alias to avoid infinite recursion.
	type alias struct {
		URL      *string          `json:"url,omitempty"`
		Enabled  *bool            `json:"enabled,omitempty"`
		Events   *json.RawMessage `json:"events"`
		Secret   *string          `json:"secret,omitempty"`
		TenantID *string          `json:"tenant_id,omitempty"`
	}
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	p.URL = a.URL
	p.Enabled = a.Enabled
	p.Secret = a.Secret
	p.TenantID = a.TenantID
	if a.Events != nil {
		p.eventsPresent = true
		var events []string
		if err := json.Unmarshal(*a.Events, &events); err != nil {
			return fmt.Errorf("webhooks: patch: decoding events: %w", err)
		}
		p.Events = events
	}
	return nil
}

// Update applies patch to the subscription identified by id and persists the
// result. The ID and CreatedAt fields are always preserved. Returns the updated
// Subscription or storage.ErrNotFound when id does not exist.
func (p *StoreProvider) Update(id string, patch SubscriptionPatch) (Subscription, error) {
	sub, err := p.Get(id)
	if err != nil {
		return Subscription{}, err
	}

	if patch.URL != nil {
		sub.URL = *patch.URL
	}
	if patch.Enabled != nil {
		sub.Enabled = *patch.Enabled
	}
	if patch.eventsPresent {
		sub.Events = patch.Events
	}
	if patch.Secret != nil {
		sub.Secret = *patch.Secret
	}
	if patch.TenantID != nil {
		sub.TenantID = *patch.TenantID
	}
	sub.UpdatedAt = time.Now().UTC()

	data, err := json.Marshal(sub)
	if err != nil {
		return Subscription{}, fmt.Errorf("webhooks: store: encoding subscription: %w", err)
	}
	if err := p.store.Write(subscriptionsBucket, sub.ID, data); err != nil {
		return Subscription{}, err
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

