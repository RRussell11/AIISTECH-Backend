package http

import (
	"encoding/json"
	"errors"
	"expvar"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
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

	// Schema validation: check required fields when event_schema is configured.
	if sc.Config.EventSchema != nil && len(sc.Config.EventSchema.Required) > 0 {
		if missing := validateJSONFields(body, sc.Config.EventSchema.Required); len(missing) > 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"error":          "schema validation failed",
				"missing_fields": missing,
			})
			return
		}
	}

	// storageKey is the full tenant-namespaced key used in the store.
	// key (bare) is returned to the client so it can be used in subsequent GET requests.
	storageKey := tenantKey(sc.TenantID, key)

	if err := sc.Store.Write(bucketEvents, storageKey, body); err != nil {
		slog.Error("failed to write event", "site_id", sc.SiteID, "key", storageKey, "error", err)
		http.Error(w, "failed to write event", http.StatusInternalServerError)
		return
	}

	slog.Info("event written", "site_id", sc.SiteID, "key", storageKey)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"status":  "created",
		"site_id": sc.SiteID,
		"file":    key,
	})
}

// ListEventsHandler handles GET /sites/{site_id}/events.
// Supports optional ?cursor= and ?limit= query parameters for cursor-based pagination.
// Segment 11: also supports filtering via since_ns/until_ns/prefix/contains.
// Returns a JSON object with the event keys for the current page plus a next_cursor
// value that can be used to fetch the next page (empty string when no more pages exist).
func ListEventsHandler(w http.ResponseWriter, r *http.Request) {
	sc, ok := site.FromContext(r.Context())
	if !ok {
		http.Error(w, "site context missing", http.StatusInternalServerError)
		return
	}

	cursor, limit, ok := parsePaginationParams(w, r)
	if !ok {
		return
	}

	filter, ok := parseFilterParams(w, r)
	if !ok {
		return
	}

	cursor, filter, tenantPrefix := applyTenantFilter(sc.TenantID, cursor, filter)
	keys, nextCursor, err := listFilteredPage(sc.Store, bucketEvents, cursor, limit, filter)
	if err != nil {
		slog.Error("failed to list events", "site_id", sc.SiteID, "error", err)
		http.Error(w, "failed to list events", http.StatusInternalServerError)
		return
	}
	keys, nextCursor = stripTenantPrefix(tenantPrefix, keys, nextCursor)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"site_id":     sc.SiteID,
		"events":      keys,
		"next_cursor": nextCursor,
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

	data, err := sc.Store.Get(bucketEvents, tenantKey(sc.TenantID, filename))
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

	// Schema validation: check required fields when artifact_schema is configured.
	if sc.Config.ArtifactSchema != nil && len(sc.Config.ArtifactSchema.Required) > 0 {
		if missing := validateJSONFields(body, sc.Config.ArtifactSchema.Required); len(missing) > 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"error":          "schema validation failed",
				"missing_fields": missing,
			})
			return
		}
	}

	storageKey := tenantKey(sc.TenantID, key)

	if err := sc.Store.Write(bucketArtifacts, storageKey, body); err != nil {
		slog.Error("failed to write artifact", "site_id", sc.SiteID, "key", storageKey, "error", err)
		http.Error(w, "failed to write artifact", http.StatusInternalServerError)
		return
	}

	slog.Info("artifact written", "site_id", sc.SiteID, "key", storageKey)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"status":  "created",
		"site_id": sc.SiteID,
		"file":    key,
	})
}

// ListArtifactsHandler handles GET /sites/{site_id}/artifacts.
// Supports optional ?cursor= and ?limit= query parameters for cursor-based pagination.
// Segment 11: also supports filtering via since_ns/until_ns/prefix/contains.
func ListArtifactsHandler(w http.ResponseWriter, r *http.Request) {
	sc, ok := site.FromContext(r.Context())
	if !ok {
		http.Error(w, "site context missing", http.StatusInternalServerError)
		return
	}

	cursor, limit, ok := parsePaginationParams(w, r)
	if !ok {
		return
	}

	filter, ok := parseFilterParams(w, r)
	if !ok {
		return
	}

	cursor, filter, tenantPrefix := applyTenantFilter(sc.TenantID, cursor, filter)
	keys, nextCursor, err := listFilteredPage(sc.Store, bucketArtifacts, cursor, limit, filter)
	if err != nil {
		slog.Error("failed to list artifacts", "site_id", sc.SiteID, "error", err)
		http.Error(w, "failed to list artifacts", http.StatusInternalServerError)
		return
	}
	keys, nextCursor = stripTenantPrefix(tenantPrefix, keys, nextCursor)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"site_id":     sc.SiteID,
		"artifacts":   keys,
		"next_cursor": nextCursor,
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

	data, err := sc.Store.Get(bucketArtifacts, tenantKey(sc.TenantID, filename))
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

	if err := sc.Store.Delete(bucketArtifacts, tenantKey(sc.TenantID, filename)); err != nil {
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
// Supports optional ?cursor= and ?limit= query parameters for cursor-based pagination.
// Segment 11: also supports filtering via since_ns/until_ns/prefix/contains.
func ListAuditHandler(w http.ResponseWriter, r *http.Request) {
	sc, ok := site.FromContext(r.Context())
	if !ok {
		http.Error(w, "site context missing", http.StatusInternalServerError)
		return
	}

	cursor, limit, ok := parsePaginationParams(w, r)
	if !ok {
		return
	}

	filter, ok := parseFilterParams(w, r)
	if !ok {
		return
	}

	cursor, filter, tenantPrefix := applyTenantFilter(sc.TenantID, cursor, filter)
	keys, nextCursor, err := listFilteredPage(sc.Store, bucketAudit, cursor, limit, filter)
	if err != nil {
		slog.Error("failed to list audit entries", "site_id", sc.SiteID, "error", err)
		http.Error(w, "failed to list audit entries", http.StatusInternalServerError)
		return
	}
	keys, nextCursor = stripTenantPrefix(tenantPrefix, keys, nextCursor)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"site_id":     sc.SiteID,
		"entries":     keys,
		"next_cursor": nextCursor,
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

	data, err := sc.Store.Get(bucketAudit, tenantKey(sc.TenantID, filename))
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

const (
	defaultPageLimit = 50
	maxPageLimit     = 200
)

// parsePaginationParams reads ?cursor= and ?limit= from r.
// limit defaults to defaultPageLimit when absent; values above maxPageLimit are
// clamped to maxPageLimit. An invalid (non-integer or negative) limit value
// causes a 400 response and returns ok=false.
func parsePaginationParams(w http.ResponseWriter, r *http.Request) (cursor string, limit int, ok bool) {
	cursor = r.URL.Query().Get("cursor")
	limit = defaultPageLimit

	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			http.Error(w, "limit must be a positive integer", http.StatusBadRequest)
			return "", 0, false
		}
		if n > maxPageLimit {
			n = maxPageLimit
		}
		limit = n
	}
	return cursor, limit, true
}

type listFilter struct {
	SinceNS   *int64
	UntilNS   *int64
	Prefix    string
	Contains  string
	hasBounds bool
}

func parseFilterParams(w http.ResponseWriter, r *http.Request) (f listFilter, ok bool) {
	q := r.URL.Query()

	if raw := q.Get("since_ns"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			http.Error(w, "since_ns must be a non-negative integer", http.StatusBadRequest)
			return listFilter{}, false
		}
		f.SinceNS = &n
		f.hasBounds = true
	}
	if raw := q.Get("until_ns"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			http.Error(w, "until_ns must be a non-negative integer", http.StatusBadRequest)
			return listFilter{}, false
		}
		f.UntilNS = &n
		f.hasBounds = true
	}

	if f.SinceNS != nil && f.UntilNS != nil && *f.SinceNS > *f.UntilNS {
		http.Error(w, "since_ns must be <= until_ns", http.StatusBadRequest)
		return listFilter{}, false
	}

	f.Prefix = q.Get("prefix")
	f.Contains = q.Get("contains")
	return f, true
}

// keyNS extracts the nanosecond integer from keys like "<ns>.json".
func keyNS(key string) (int64, bool) {
	trim := strings.TrimSuffix(key, ".json")
	if trim == key {
		return 0, false
	}
	n, err := strconv.ParseInt(trim, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func keyMatchesFilter(key string, f listFilter) bool {
	if f.Prefix != "" && !strings.HasPrefix(key, f.Prefix) {
		return false
	}
	if f.Contains != "" && !strings.Contains(key, f.Contains) {
		return false
	}

	if f.hasBounds {
		ns, ok := keyNS(key)
		if !ok {
			// If we cannot parse ns from the key, exclude it from ns-bounded results.
			return false
		}
		if f.SinceNS != nil && ns < *f.SinceNS {
			return false
		}
		if f.UntilNS != nil && ns > *f.UntilNS {
			return false
		}
	}

	return true
}

// listFilteredPage applies Segment 11 filtering BEFORE pagination.
// cursor semantics remain: it means "start strictly after this key" in the filtered order.
func listFilteredPage(store storage.Store, bucket, cursor string, limit int, f listFilter) (keys []string, nextCursor string, err error) {
	all, err := store.List(bucket)
	if err != nil {
		return nil, "", err
	}

	// Move start index strictly after cursor in filtered order.
	startIdx := 0
	if cursor != "" {
		// Find cursor in filtered list; start after it. If cursor is not found,
		// behave like starting from the beginning (consistent with current behavior).
		for i, k := range all {
			if !keyMatchesFilter(k, f) {
				continue
			}
			if k == cursor {
				startIdx = i + 1
				break
			}
		}
	}

	out := make([]string, 0, limit)

	// Iterate from startIdx across raw keys, but only count filtered keys.
	for i := startIdx; i < len(all) && len(out) < limit; i++ {
		k := all[i]
		if !keyMatchesFilter(k, f) {
			continue
		}
		out = append(out, k)
	}

	// Determine nextCursor: it's the last returned key IF there exists at least one more filtered key after it.
	if len(out) == 0 {
		return []string{}, "", nil
	}

	last := out[len(out)-1]
	// scan for any additional filtered key after 'last' in raw order
	foundLast := false
	hasMore := false
	for _, k := range all {
		if !keyMatchesFilter(k, f) {
			continue
		}
		if !foundLast {
			if k == last {
				foundLast = true
			}
			continue
		}
		// any further filtered key means more pages
		hasMore = true
		break
	}

	if hasMore {
		return out, last, nil
	}
	return out, "", nil
}

// --- Tenant-scoped storage helpers (Segment 19) ---

// tenantKey prefixes key with the tenant namespace when tenantID is non-empty,
// ensuring events, artifacts, and audit entries are physically separated per
// tenant within the same site store bucket.
// Returns key unchanged when tenantID is empty (legacy mode).
func tenantKey(tenantID, key string) string {
	if tenantID == "" {
		return key
	}
	return tenantID + "/" + key
}

// applyTenantFilter adjusts cursor and filter for tenant-scoped list operations.
// It returns the storage-level cursor (tenant-prefixed when non-empty), the
// updated filter with the tenant prefix injected, and the tenant prefix string
// that must be stripped from keys and nextCursor before they are returned to
// the caller.
func applyTenantFilter(tenantID, cursor string, f listFilter) (adjustedCursor string, adjustedFilter listFilter, tenantPrefix string) {
	if tenantID == "" {
		return cursor, f, ""
	}
	prefix := tenantID + "/"
	// Re-add tenant prefix to the incoming cursor so it matches storage keys.
	if cursor != "" {
		cursor = prefix + cursor
	}
	// Prepend tenant prefix to any user-supplied prefix filter.
	if f.Prefix == "" {
		f.Prefix = prefix
	} else {
		f.Prefix = prefix + f.Prefix
	}
	return cursor, f, prefix
}

// stripTenantPrefix removes tenantPrefix from every key in keys and from
// nextCursor, producing the bare client-visible names without the tenant
// namespace. When tenantPrefix is empty the slices are returned unchanged.
func stripTenantPrefix(tenantPrefix string, keys []string, nextCursor string) ([]string, string) {
	if tenantPrefix == "" {
		return keys, nextCursor
	}
	for i, k := range keys {
		keys[i] = strings.TrimPrefix(k, tenantPrefix)
	}
	return keys, strings.TrimPrefix(nextCursor, tenantPrefix)
}

// --- Schema validation helpers (Segment 20) ---

// validateJSONFields checks that every field name in required is present as a
// top-level key in the JSON object body. It returns the names of any missing
// fields. A non-object body (array, scalar) returns nil (no missing fields) so
// that callers fall through to the existing JSON-validity check.
func validateJSONFields(body []byte, required []string) []string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		// Body is not a JSON object — let the caller surface a 400.
		return nil
	}
	var missing []string
	for _, field := range required {
		if _, ok := m[field]; !ok {
			missing = append(missing, field)
		}
	}
	return missing
}