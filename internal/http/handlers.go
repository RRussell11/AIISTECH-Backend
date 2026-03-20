package http

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/RRussell11/AIISTECH-Backend/internal/site"
	"github.com/RRussell11/AIISTECH-Backend/internal/state"
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
// It reads the request body and writes it as a JSON event file under
// var/state/<site_id>/events/<timestamp>.json.
func PostEventHandler(w http.ResponseWriter, r *http.Request) {
	sc, ok := site.FromContext(r.Context())
	if !ok {
		http.Error(w, "site context missing", http.StatusInternalServerError)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB limit
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if !json.Valid(body) {
		http.Error(w, "request body must be valid JSON", http.StatusBadRequest)
		return
	}

	eventsDir := state.EventsDir(sc.SiteID)
	if err := os.MkdirAll(eventsDir, 0o755); err != nil {
		slog.Error("failed to create events dir", "site_id", sc.SiteID, "dir", eventsDir, "error", err)
		http.Error(w, "failed to create events directory", http.StatusInternalServerError)
		return
	}

	filename := fmt.Sprintf("%d.json", time.Now().UnixNano())
	destPath := filepath.Join(eventsDir, filename)

	if err := os.WriteFile(destPath, body, 0o644); err != nil {
		slog.Error("failed to write event file", "site_id", sc.SiteID, "path", destPath, "error", err)
		http.Error(w, "failed to write event", http.StatusInternalServerError)
		return
	}

	slog.Info("event written", "site_id", sc.SiteID, "path", destPath)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"status":  "created",
		"site_id": sc.SiteID,
		"file":    filename,
	})
}

// ListEventsHandler handles GET /sites/{site_id}/events.
// Returns a JSON array of event filenames sorted in ascending order.
func ListEventsHandler(w http.ResponseWriter, r *http.Request) {
	sc, ok := site.FromContext(r.Context())
	if !ok {
		http.Error(w, "site context missing", http.StatusInternalServerError)
		return
	}

	eventsDir := state.EventsDir(sc.SiteID)
	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		if os.IsNotExist(err) {
			// No events written yet — return empty list.
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"site_id": sc.SiteID,
				"events":  []string{},
			})
			return
		}
		slog.Error("failed to read events dir", "site_id", sc.SiteID, "dir", eventsDir, "error", err)
		http.Error(w, "failed to list events", http.StatusInternalServerError)
		return
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"site_id": sc.SiteID,
		"events":  names,
	})
}

// GetEventHandler handles GET /sites/{site_id}/events/{filename}.
// Returns the raw contents of the named event file.
func GetEventHandler(w http.ResponseWriter, r *http.Request) {
	sc, ok := site.FromContext(r.Context())
	if !ok {
		http.Error(w, "site context missing", http.StatusInternalServerError)
		return
	}

	filename := chi.URLParam(r, "filename")
	// Validate filename to prevent path traversal within the events dir.
	if err := site.Validate(filename); err != nil {
		http.Error(w, fmt.Sprintf("invalid filename: %v", err), http.StatusBadRequest)
		return
	}

	filePath := filepath.Join(state.EventsDir(sc.SiteID), filename)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "event not found", http.StatusNotFound)
			return
		}
		slog.Error("failed to read event file", "site_id", sc.SiteID, "file", filename, "error", err)
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
		sort.Strings(ids)

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
