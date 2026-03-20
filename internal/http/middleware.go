package http

import (
	"expvar"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	auditpkg "github.com/RRussell11/AIISTECH-Backend/internal/audit"
	"github.com/RRussell11/AIISTECH-Backend/internal/site"
	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
)

var (
	metricsReqsBySite = expvar.NewMap("requests_by_site")
	metricsReqsTotal  = expvar.NewInt("requests_total")
)

// SiteMiddleware extracts {site_id} from the URL, resolves it against the
// registry, opens the site's store from the StoreRegistry, and attaches a
// SiteContext (including the Store) to the request context.
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

			sc := site.SiteContext{SiteID: siteID, Store: store}
			ctx := site.NewContext(r.Context(), sc)
			slog.Info("request", "method", r.Method, "path", r.URL.Path, "site_id", siteID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
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
// state-mutating (POST, PUT, PATCH, DELETE) site-scoped request.
// It must be placed after SiteMiddleware so that the site context is available.
func AuditMiddleware(next http.Handler) http.Handler {
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
	})
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
