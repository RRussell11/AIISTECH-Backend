package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

// ListSubscriptionsHandler handles GET /webhooks/subscriptions.
// Supports optional ?cursor= and ?limit= pagination parameters.
func ListSubscriptionsHandler(sp *webhooks.StoreProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cursor, limit, ok := parsePaginationParams(w, r)
		if !ok {
			return
		}

		subs, nextCursor, err := sp.ListPage(cursor, limit)
		if err != nil {
			slog.Error("webhooks: subscriptions: failed to list", "error", err)
			http.Error(w, "failed to list subscriptions", http.StatusInternalServerError)
			return
		}
		if subs == nil {
			subs = []webhooks.Subscription{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"subscriptions": subs,
			"next_cursor":   nextCursor,
		})
	}
}

// subscriptionCreateRequest is the request body for POST /webhooks/subscriptions.
type subscriptionCreateRequest struct {
	Service  string   `json:"service"`
	URL      string   `json:"url"`
	Enabled  *bool    `json:"enabled"`
	Events   []string `json:"events"`
	Secret   string   `json:"secret"`
	TenantID string   `json:"tenant_id"`
}

// CreateSubscriptionHandler handles POST /webhooks/subscriptions.
// Required fields: url, service.
// Returns 201 Created with the persisted Subscription.
func CreateSubscriptionHandler(sp *webhooks.StoreProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req subscriptionCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if req.URL == "" {
			http.Error(w, "url is required", http.StatusBadRequest)
			return
		}
		if req.Service == "" {
			http.Error(w, "service is required", http.StatusBadRequest)
			return
		}

		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}

		now := time.Now().UTC()
		sub := webhooks.Subscription{
			Service:   req.Service,
			URL:       req.URL,
			Enabled:   enabled,
			Events:    req.Events,
			Secret:    req.Secret,
			TenantID:  req.TenantID,
			CreatedAt: now,
			UpdatedAt: now,
		}
		// Leave sub.ID empty so Create auto-generates it. Create takes a pointer
		// so the generated ID is set on sub before returning.

		if err := sp.Create(&sub); err != nil {
			slog.Error("webhooks: subscriptions: failed to create", "error", err)
			http.Error(w, "failed to create subscription", http.StatusInternalServerError)
			return
		}

		slog.Info("webhooks: subscription created", "id", sub.ID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(sub) //nolint:errcheck
	}
}

// GetSubscriptionHandler handles GET /webhooks/subscriptions/{id}.
func GetSubscriptionHandler(sp *webhooks.StoreProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		sub, err := sp.Get(id)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				http.Error(w, "subscription not found", http.StatusNotFound)
				return
			}
			slog.Error("webhooks: subscriptions: failed to get", "id", id, "error", err)
			http.Error(w, "failed to get subscription", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sub) //nolint:errcheck
	}
}

// PatchSubscriptionHandler handles PATCH /webhooks/subscriptions/{id}.
// Applies a partial update. Only supplied fields are changed; omitted fields
// keep their current value.
func PatchSubscriptionHandler(sp *webhooks.StoreProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		var patch webhooks.SubscriptionPatch
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}

		updated, err := sp.Update(id, patch)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				http.Error(w, "subscription not found", http.StatusNotFound)
				return
			}
			slog.Error("webhooks: subscriptions: failed to update", "id", id, "error", err)
			http.Error(w, "failed to update subscription", http.StatusInternalServerError)
			return
		}

		slog.Info("webhooks: subscription updated", "id", id)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(updated) //nolint:errcheck
	}
}

// DeleteSubscriptionHandler handles DELETE /webhooks/subscriptions/{id}.
// Returns 204 No Content on success.
func DeleteSubscriptionHandler(sp *webhooks.StoreProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := sp.Delete(id); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				http.Error(w, "subscription not found", http.StatusNotFound)
				return
			}
			slog.Error("webhooks: subscriptions: failed to delete", "id", id, "error", err)
			http.Error(w, "failed to delete subscription", http.StatusInternalServerError)
			return
		}

		slog.Info("webhooks: subscription deleted", "id", id)
		w.WriteHeader(http.StatusNoContent)
	}
}
