package webhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// RemoteProvider implements Provider by fetching webhook subscriptions from
// the PhaseMirror-HQ daemon API over HTTP.
//
// It is safe for concurrent use. Create instances with NewRemoteProvider.
type RemoteProvider struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewRemoteProvider constructs a RemoteProvider that calls
// <baseURL>/api/webhook-subscriptions with the given bearer token.
//
// timeout is the per-request HTTP timeout; pass 0 to use the default (10 s).
func NewRemoteProvider(baseURL, token string, timeout time.Duration) *RemoteProvider {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &RemoteProvider{
		baseURL: baseURL,
		token:   token,
		client:  &http.Client{Timeout: timeout},
	}
}

// ListSubscriptions calls GET <baseURL>/api/webhook-subscriptions with the
// provided filter parameters and returns the decoded subscription slice.
//
// Empty filter strings are omitted from the query string.
// A non-2xx response from the remote API is returned as an error.
func (p *RemoteProvider) ListSubscriptions(ctx context.Context, service, eventType, tenantID string) ([]Subscription, error) {
	u, err := url.Parse(p.baseURL + "/api/webhook-subscriptions")
	if err != nil {
		return nil, fmt.Errorf("webhooks: invalid base URL: %w", err)
	}

	q := u.Query()
	if service != "" {
		q.Set("service", service)
	}
	if eventType != "" {
		q.Set("event_type", eventType)
	}
	if tenantID != "" {
		q.Set("tenant_id", tenantID)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("webhooks: building subscription request: %w", err)
	}
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webhooks: fetching subscriptions: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("webhooks: subscription API returned status %d", resp.StatusCode)
	}

	var lr ListResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, fmt.Errorf("webhooks: decoding subscription response: %w", err)
	}
	return lr.Data, nil
}
