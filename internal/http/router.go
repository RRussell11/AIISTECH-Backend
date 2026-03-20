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

	// Non-site-scoped routes
	r.Get("/healthz", HealthzHandler)

	// Site-scoped routes
	r.Route("/sites/{site_id}", func(r chi.Router) {
		r.Use(SiteMiddleware(reg))
		r.Get("/healthz", SiteHealthzHandler)
		r.Post("/events", PostEventHandler)
	})

	return r
}
