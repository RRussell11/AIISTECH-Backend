package webhooks

import (
	"context"
	"log/slog"
)

// MultiProvider composes multiple Provider implementations and returns a
// deduplicated union of all subscriptions they return (ADR-037).
//
// Deduplication key is the subscription ID when non-empty; otherwise the
// combination of URL and TenantID is used as a fallback. The first occurrence
// of a duplicate (in the order providers were registered) wins.
//
// If a provider returns an error it is logged and skipped; the remaining
// providers are still queried. This means MultiProvider degrades gracefully
// when one source is temporarily unavailable.
//
// Create instances with NewMultiProvider. The zero value is not usable.
type MultiProvider struct {
	providers []Provider
}

// NewMultiProvider returns a MultiProvider that aggregates subscriptions from
// all given providers.
func NewMultiProvider(providers ...Provider) *MultiProvider {
	return &MultiProvider{providers: providers}
}

// ListSubscriptions queries all configured providers, merges their results,
// and deduplicates. A provider that returns an error is logged and skipped;
// the successfully fetched subscriptions from other providers are returned
// without propagating the error.
func (m *MultiProvider) ListSubscriptions(ctx context.Context, service, eventType, tenantID string) ([]Subscription, error) {
	seen := make(map[string]struct{})
	var out []Subscription

	for i, p := range m.providers {
		subs, err := p.ListSubscriptions(ctx, service, eventType, tenantID)
		if err != nil {
			slog.Warn("webhooks: multi_provider: provider error, skipping",
				"provider_index", i,
				"error", err,
			)
			continue
		}
		for _, sub := range subs {
			key := multiDedupeKey(sub)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, sub)
		}
	}

	return out, nil
}

// multiDedupeKey returns a stable string key for deduplication. The
// subscription ID is preferred; URL+TenantID is used when ID is absent.
func multiDedupeKey(sub Subscription) string {
	if sub.ID != "" {
		return "id:" + sub.ID
	}
	return "url:" + sub.URL + "|tenant:" + sub.TenantID
}

// compile-time check that *MultiProvider satisfies Provider.
var _ Provider = (*MultiProvider)(nil)
