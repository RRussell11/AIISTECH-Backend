package http

import (
	"context"
	"expvar"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	auditpkg "github.com/RRussell11/AIISTECH-Backend/internal/audit"
	"github.com/RRussell11/AIISTECH-Backend/internal/config"
	"github.com/RRussell11/AIISTECH-Backend/internal/site"
	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

var (
	metricsReqsBySite = expvar.NewMap("requests_by_site")
	metricsReqsTotal  = expvar.NewInt("requests_total")
)

// dispatchTimeout is the maximum time AuditMiddleware will wait for
// Dispatcher.Dispatch to accept an event into its internal queue. Delivery
// itself is asynchronous and is not bounded by this timeout.
const dispatchTimeout = 2 * time.Second

// SiteMiddleware extracts {site_id} from the URL, resolves it against the
// registry, opens the site's store, loads the per-site config (to obtain the
// APIKey), and attaches a SiteContext to the request context.
// Requests with invalid or unknown site IDs are rejected with 400/404.
func SiteMiddleware(reg *site.Registry, stores *storage.Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawID := chi.URLParam(r, "site_id")

			siteID, err := site.Resolve(rawID, reg)
			if err != nil {
				slog.Warn("site resolution failed", "raw_site_id", rawID, "error", err)
				status := http.StatusNotFound
				if rawID == "" {
					status = http.StatusBadRequest
				}
				http.Error(w, fmt.Sprintf("invalid or unknown site_id: %v", err), status)
				return
			}

			store, err := stores.Open(siteID)
			if err != nil {
				slog.Error("failed to open site store", "site_id", siteID, "error", err)
				http.Error(w, "failed to open site store", http.StatusInternalServerError)
				return
			}

			cfg, err := config.Load(siteID, config.ConfigPath(siteID))
			if err != nil {
				slog.Error("failed to load site config", "site_id", siteID, "error", err)
				http.Error(w, "failed to load site config", http.StatusInternalServerError)
				return
			}

			tenantID := r.Header.Get("X-Tenant-ID")
			sc := site.SiteContext{SiteID: siteID, Store: store, APIKey: cfg.APIKey, TenantID: tenantID}
			ctx := site.NewContext(r.Context(), sc)
			slog.Info("request", "method", r.Method, "path", r.URL.Path, "site_id", siteID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AuthMiddleware enforces per-site API-key authentication for state-mutating
// requests (POST, PUT, PATCH, DELETE).  Read-only requests (GET, HEAD, OPTIONS)
// are always permitted.
//
// When a site has an APIKey configured, the caller must supply:
//
//	Authorization: Bearer <api_key>
//
// Missing, empty, or mismatched keys result in 401 Unauthorized.
// Requests to sites with no APIKey configured are always allowed.
//
// AuthMiddleware must run after SiteMiddleware so that the SiteContext
// (including APIKey) is available in the request context.
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only mutating methods require authentication.
		if r.Method != http.MethodPost && r.Method != http.MethodPut &&
			r.Method != http.MethodPatch && r.Method != http.MethodDelete {
			next.ServeHTTP(w, r)
			return
		}

		sc, ok := site.FromContext(r.Context())
		if !ok {
			// SiteMiddleware must run before AuthMiddleware.
			http.Error(w, "site context missing", http.StatusInternalServerError)
			return
		}

		// If no API key is configured for this site, authentication is disabled.
		if sc.APIKey == "" {
			next.ServeHTTP(w, r)
			return
		}

		token, ok := bearerToken(r)
		if !ok || token != sc.APIKey {
			slog.Warn("authentication failed", "site_id", sc.SiteID, "method", r.Method, "path", r.URL.Path)
			w.Header().Set("WWW-Authenticate", `Bearer realm="aiistech"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// bearerToken extracts the token from the Authorization: Bearer <token> header.
// Returns ("", false) if the header is absent or not in Bearer format.
func bearerToken(r *http.Request) (string, bool) {
	hdr := r.Header.Get("Authorization")
	if hdr == "" {
		return "", false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(hdr, prefix) {
		return "", false
	}
	token := strings.TrimPrefix(hdr, prefix)
	if token == "" {
		return "", false
	}
	return token, true
}

// statusRecorder wraps http.ResponseWriter to capture the HTTP status code written by a handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func newStatusRecorder(w http.ResponseWriter) *statusRecorder {
	return &statusRecorder{ResponseWriter: w, status: http.StatusOK}
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

// AuditMiddleware automatically writes a structured audit entry for every
// state-mutating (POST, PUT, PATCH, DELETE) site-scoped request and, when d
// is non-nil, dispatches a webhooks.Event of type "audit.write" for each
// successful entry so that external subscribers are notified.
// It must be placed after SiteMiddleware so that the site context is available.
func AuditMiddleware(d webhooks.Dispatcher) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost && r.Method != http.MethodPut &&
				r.Method != http.MethodPatch && r.Method != http.MethodDelete {
				next.ServeHTTP(w, r)
				return
			}
			sr := newStatusRecorder(w)
			next.ServeHTTP(sr, r)

			sc, ok := site.FromContext(r.Context())
			if !ok {
				return
			}
			entry := auditpkg.Entry{
				RequestID: chimiddleware.GetReqID(r.Context()),
				SiteID:    sc.SiteID,
				Method:    r.Method,
				Path:      r.URL.Path,
				Status:    sr.status,
				Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			}
			if err := auditpkg.Write(entry, sc.Store); err != nil {
				slog.Error("failed to write audit entry", "site_id", sc.SiteID, "error", err)
			}

			if d != nil {
				evt := webhooks.Event{
					ID:        entry.RequestID,
					Type:      "audit.write",
					TenantID:  sc.TenantID,
					CreatedAt: time.Now().UTC(),
					Data:      entry,
				}
				dispCtx, cancel := context.WithTimeout(context.Background(), dispatchTimeout)
				defer cancel()
				if err := d.Dispatch(dispCtx, evt); err != nil {
					slog.Warn("failed to dispatch audit webhook", "site_id", sc.SiteID, "error", err)
				}
			}
		})
	}
}

// MetricsMiddleware increments per-request expvar counters for every request.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		metricsReqsTotal.Add(1)
		if sc, ok := site.FromContext(r.Context()); ok {
			metricsReqsBySite.Add(sc.SiteID, 1)
		}
	})
}

