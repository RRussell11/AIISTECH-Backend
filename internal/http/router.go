package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/RRussell11/AIISTECH-Backend/internal/site"
	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

// NewRouter builds and returns the application HTTP router.
// disp may be nil; when non-nil it receives an "audit.write" webhook event
// for every state-mutating request processed by AuditMiddleware.
// replayClient is the HTTP client used by the DLQ replay endpoint; when nil a
// default client with a 10-second timeout is used (ADR-016, Segment 16).
func NewRouter(reg *site.Registry, stores *storage.Registry, disp webhooks.Dispatcher, replayClient *http.Client) http.Handler {
	if replayClient == nil {
		replayClient = &http.Client{Timeout: 10 * time.Second}
	}

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
		r.Use(AuthMiddleware)          // enforces Bearer token for mutating requests when site has api_key
		r.Use(AuditMiddleware(disp))   // auto-audit all mutating requests; dispatches webhook when disp != nil
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
		r.Get("/webhooks/dlq", ListDLQHandler)
		r.Get("/webhooks/dlq/{id}", GetDLQEntryHandler)
		r.Delete("/webhooks/dlq/{id}", DeleteDLQEntryHandler)
		r.Post("/webhooks/dlq/{id}/replay", ReplayDLQEntryHandler(replayClient))
	})

	return r
}

