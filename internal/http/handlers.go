package http

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

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
