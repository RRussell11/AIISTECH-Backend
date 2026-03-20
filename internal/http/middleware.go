package http

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/RRussell11/AIISTECH-Backend/internal/site"
)

// SiteMiddleware extracts {site_id} from the URL, resolves it against the
// registry, and attaches a SiteContext to the request context.
// Requests with invalid or unknown site IDs are rejected with 400/404.
func SiteMiddleware(reg *site.Registry) func(http.Handler) http.Handler {
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

			sc := site.SiteContext{SiteID: siteID}
			ctx := site.NewContext(r.Context(), sc)
			slog.Info("request", "method", r.Method, "path", r.URL.Path, "site_id", siteID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
