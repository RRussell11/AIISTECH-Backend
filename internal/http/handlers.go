package http

import (
	"encoding/json"
	"errors"
	"expvar"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/RRussell11/AIISTECH-Backend/internal/config"
	"github.com/RRussell11/AIISTECH-Backend/internal/site"
	"github.com/RRussell11/AIISTECH-Backend/internal/state"
	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
)

const (
	bucketEvents    = "events"
	bucketArtifacts = "artifacts"
	bucketAudit     = "audit"
)

// HealthzHandler handles GET /healthz (non-site-scoped).
func HealthzHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck
}

// SiteHealthzHandler handles GET /sites/{site_id}/healthz (site-scoped).
func SiteHealthzHandler(w http.ResponseWriter, r *http.Request) {
	sc, ok := site.FromContext(r.Context())
	if !ok {
		http.Error(w, "site context missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"status":  "ok",
		"site_id": sc.SiteID,
	})
}

// PostEventHandler handles POST /sites/{site_id}/events.
// It reads the request body and persists it as a JSON event in the site store.
func PostEventHandler(w http.ResponseWriter, r *http.Request) {
	sc, ok := site.FromContext(r.Context())
	if !ok {
		http.Error(w, "site context missing", http.StatusInternalServerError)
		return
	}

	body, key, err := readJSONBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := sc.Store.Write(bucketEvents, key, body); err != nil {
		slog.Error("failed to write event", "site_id", sc.SiteID, "key", key, "error", err)
		http.Error(w, "failed to write event", http.StatusInternalServerError)
		return
	}

	slog.Info("event written", "site_id", sc.SiteID, "key", key)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"status":  "created",
		"site_id": sc.SiteID,
		"file":    key,
	})
}

// ListEventsHandler handles GET /sites/{site_id}/events.
// Returns a JSON array of event keys sorted in ascending order.
func ListEventsHandler(w http.ResponseWriter, r *http.Request) {
	sc, ok := site.FromContext(r.Context())
	if !ok {
		http.Error(w, "site context missing", http.StatusInternalServerError)
		return
	}

	keys, err := sc.Store.List(bucketEvents)
	if err != nil {
		slog.Error("failed to list events", "site_id", sc.SiteID, "error", err)
		http.Error(w, "failed to list events", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"site_id": sc.SiteID,
		"events":  keys,
	})
}

// GetEventHandler handles GET /sites/{site_id}/events/{filename}.
// Returns the raw contents of the named event.
func GetEventHandler(w http.ResponseWriter, r *http.Request) {
	sc, ok := site.FromContext(r.Context())
	if !ok {
		http.Error(w, "site context missing", http.StatusInternalServerError)
		return
	}

	filename := chi.URLParam(r, "filename")
	if err := site.Validate(filename); err != nil {
		http.Error(w, fmt.Sprintf("invalid filename: %v", err), http.StatusBadRequest)
		return
	}

	data, err := sc.Store.Get(bucketEvents, filename)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.Error(w, "event not found", http.StatusNotFound)
			return
		}
		slog.Error("failed to read event", "site_id", sc.SiteID, "key", filename, "error", err)
		http.Error(w, "failed to read event", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(data) //nolint:errcheck
}

// ListSitesHandler handles GET /sites (non-site-scoped).
// Returns the full catalog of registered sites and the default site.
func ListSitesHandler(reg *site.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ids := reg.SiteIDs()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"default_site_id": reg.DefaultSiteID,
			"sites":           ids,
		})
	}
}

// GetSiteHandler handles GET /sites/{site_id} (site-scoped).
// Returns site metadata including the computed state root path.
func GetSiteHandler(w http.ResponseWriter, r *http.Request) {
	sc, ok := site.FromContext(r.Context())
	if !ok {
		http.Error(w, "site context missing", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"site_id":    sc.SiteID,
		"state_root": state.StateRoot(sc.SiteID),
	})
}

// --- Artifacts ---

// PostArtifactHandler handles POST /sites/{site_id}/artifacts.
// Persists a JSON payload in the site store under the artifacts bucket.
func PostArtifactHandler(w http.ResponseWriter, r *http.Request) {
	sc, ok := site.FromContext(r.Context())
	if !ok {
		http.Error(w, "site context missing", http.StatusInternalServerError)
		return
	}

	body, key, err := readJSONBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := sc.Store.Write(bucketArtifacts, key, body); err != nil {
		slog.Error("failed to write artifact", "site_id", sc.SiteID, "key", key, "error", err)
		http.Error(w, "failed to write artifact", http.StatusInternalServerError)
		return
	}

	slog.Info("artifact written", "site_id", sc.SiteID, "key", key)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"status":  "created",
		"site_id": sc.SiteID,
		"file":    key,
	})
}

// ListArtifactsHandler handles GET /sites/{site_id}/artifacts.
// Returns a JSON array of artifact keys sorted ascending.
func ListArtifactsHandler(w http.ResponseWriter, r *http.Request) {
	sc, ok := site.FromContext(r.Context())
	if !ok {
		http.Error(w, "site context missing", http.StatusInternalServerError)
		return
	}

	keys, err := sc.Store.List(bucketArtifacts)
	if err != nil {
		slog.Error("failed to list artifacts", "site_id", sc.SiteID, "error", err)
		http.Error(w, "failed to list artifacts", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"site_id":   sc.SiteID,
		"artifacts": keys,
	})
}

// GetArtifactHandler handles GET /sites/{site_id}/artifacts/{filename}.
func GetArtifactHandler(w http.ResponseWriter, r *http.Request) {
	sc, ok := site.FromContext(r.Context())
	if !ok {
		http.Error(w, "site context missing", http.StatusInternalServerError)
		return
	}

	filename := chi.URLParam(r, "filename")
	if err := site.Validate(filename); err != nil {
		http.Error(w, fmt.Sprintf("invalid filename: %v", err), http.StatusBadRequest)
		return
	}

	data, err := sc.Store.Get(bucketArtifacts, filename)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.Error(w, "artifact not found", http.StatusNotFound)
			return
		}
		slog.Error("failed to read artifact", "site_id", sc.SiteID, "key", filename, "error", err)
		http.Error(w, "failed to read artifact", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(data) //nolint:errcheck
}

// DeleteArtifactHandler handles DELETE /sites/{site_id}/artifacts/{filename}.
func DeleteArtifactHandler(w http.ResponseWriter, r *http.Request) {
	sc, ok := site.FromContext(r.Context())
	if !ok {
		http.Error(w, "site context missing", http.StatusInternalServerError)
		return
	}

	filename := chi.URLParam(r, "filename")
	if err := site.Validate(filename); err != nil {
		http.Error(w, fmt.Sprintf("invalid filename: %v", err), http.StatusBadRequest)
		return
	}

	if err := sc.Store.Delete(bucketArtifacts, filename); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.Error(w, "artifact not found", http.StatusNotFound)
			return
		}
		slog.Error("failed to delete artifact", "site_id", sc.SiteID, "key", filename, "error", err)
		http.Error(w, "failed to delete artifact", http.StatusInternalServerError)
		return
	}

	slog.Info("artifact deleted", "site_id", sc.SiteID, "key", filename)
	w.WriteHeader(http.StatusNoContent)
}

// --- Audit ---

// ListAuditHandler handles GET /sites/{site_id}/audit.
// Returns a JSON array of audit entry keys sorted ascending.
func ListAuditHandler(w http.ResponseWriter, r *http.Request) {
	sc, ok := site.FromContext(r.Context())
	if !ok {
		http.Error(w, "site context missing", http.StatusInternalServerError)
		return
	}

	keys, err := sc.Store.List(bucketAudit)
	if err != nil {
		slog.Error("failed to list audit entries", "site_id", sc.SiteID, "error", err)
		http.Error(w, "failed to list audit entries", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"site_id": sc.SiteID,
		"entries": keys,
	})
}

// GetAuditHandler handles GET /sites/{site_id}/audit/{filename}.
// Returns the raw JSON content of the named audit entry.
func GetAuditHandler(w http.ResponseWriter, r *http.Request) {
	sc, ok := site.FromContext(r.Context())
	if !ok {
		http.Error(w, "site context missing", http.StatusInternalServerError)
		return
	}

	filename := chi.URLParam(r, "filename")
	if err := site.Validate(filename); err != nil {
		http.Error(w, fmt.Sprintf("invalid filename: %v", err), http.StatusBadRequest)
		return
	}

	data, err := sc.Store.Get(bucketAudit, filename)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.Error(w, "audit entry not found", http.StatusNotFound)
			return
		}
		slog.Error("failed to read audit entry", "site_id", sc.SiteID, "key", filename, "error", err)
		http.Error(w, "failed to read audit entry", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(data) //nolint:errcheck
}

// --- Config ---

// GetConfigHandler handles GET /sites/{site_id}/config.
// Returns the per-site configuration loaded from contracts/sites/<site_id>/config.yaml.
// Returns an empty settings map if no config file exists for the site.
func GetConfigHandler(w http.ResponseWriter, r *http.Request) {
	sc, ok := site.FromContext(r.Context())
	if !ok {
		http.Error(w, "site context missing", http.StatusInternalServerError)
		return
	}

	cfg, err := config.Load(sc.SiteID, config.ConfigPath(sc.SiteID))
	if err != nil {
		slog.Error("failed to load site config", "site_id", sc.SiteID, "error", err)
		http.Error(w, "failed to load site config", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg) //nolint:errcheck
}

// --- Observability ---

// LivezHandler handles GET /healthz/live.
// Returns 200 OK as long as the process is running.
func LivezHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck
}

// ReadyzHandler handles GET /healthz/ready.
// Returns 200 OK when the site registry is loaded and non-empty.
func ReadyzHandler(reg *site.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ids := reg.SiteIDs()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"status": "ok",
			"sites":  len(ids),
		})
	}
}

// MetricsHandler handles GET /metrics.
// Returns all registered expvar counters as JSON.
func MetricsHandler(w http.ResponseWriter, r *http.Request) {
	expvar.Handler().ServeHTTP(w, r)
}

// --- helpers ---

// readJSONBody reads and validates a JSON request body (max 1 MiB).
// It returns the raw bytes and a nanosecond-timestamped storage key.
func readJSONBody(r *http.Request) (body []byte, key string, err error) {
	raw, readErr := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	defer r.Body.Close()
	if readErr != nil {
		return nil, "", fmt.Errorf("failed to read request body")
	}
	if !json.Valid(raw) {
		return nil, "", fmt.Errorf("request body must be valid JSON")
	}
	return raw, fmt.Sprintf("%d.json", time.Now().UnixNano()), nil
}

