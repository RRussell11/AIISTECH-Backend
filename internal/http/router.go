package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/RRussell11/AIISTECH-Backend/internal/site"
	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
)

// NewRouter builds and returns the application HTTP router.
func NewRouter(reg *site.Registry, stores *storage.Registry) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID) // injects X-Request-Id for audit traceability
	r.Use(MetricsMiddleware)   // global request counter

	// Non-site-scoped routes
	r.Get("/healthz", HealthzHandler)           // backward-compatible liveness
	r.Get("/healthz/live", LivezHandler)        // explicit liveness probe
	r.Get("/healthz/ready", ReadyzHandler(reg)) // readiness probe: registry loaded
	r.Get("/metrics", MetricsHandler)
	r.Get("/sites", ListSitesHandler(reg))

	// Site-scoped routes
	r.Route("/sites/{site_id}", func(r chi.Router) {
		r.Use(SiteMiddleware(reg, stores))
		r.Use(AuditMiddleware) // auto-audit all mutating requests
		r.Get("/", GetSiteHandler)
		r.Get("/healthz", SiteHealthzHandler)
		r.Get("/config", GetConfigHandler)
		r.Get("/events", ListEventsHandler)
		r.Post("/events", PostEventHandler)
		r.Get("/events/{filename}", GetEventHandler)
		r.Get("/artifacts", ListArtifactsHandler)
		r.Post("/artifacts", PostArtifactHandler)
		r.Get("/artifacts/{filename}", GetArtifactHandler)
		r.Delete("/artifacts/{filename}", DeleteArtifactHandler)
		r.Get("/audit", ListAuditHandler)
		r.Get("/audit/{filename}", GetAuditHandler)
	})

	return r
}

