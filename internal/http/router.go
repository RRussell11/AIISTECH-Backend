package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/RRussell11/AIISTECH-Backend/internal/site"
)

// NewRouter builds and returns the application HTTP router.
func NewRouter(reg *site.Registry) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID) // injects X-Request-Id for audit traceability

	// Non-site-scoped routes
	r.Get("/healthz", HealthzHandler)
	r.Get("/sites", ListSitesHandler(reg))

	// Site-scoped routes
	r.Route("/sites/{site_id}", func(r chi.Router) {
		r.Use(SiteMiddleware(reg))
		r.Get("/", GetSiteHandler)
		r.Get("/healthz", SiteHealthzHandler)
		r.Get("/events", ListEventsHandler)
		r.Post("/events", PostEventHandler)
		r.Get("/events/{filename}", GetEventHandler)
	})

	return r
}
