package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/RRussell11/AIISTECH-Backend/internal/site"
	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

// NewRouter builds and returns the application HTTP router.
// disp may be nil; when non-nil it receives an "audit.write" webhook event
// for every state-mutating request processed by AuditMiddleware.
// dlqStore and dlqReplayer may be nil; when both are non-nil the DLQ management
// endpoints are mounted at /webhooks/dlq.
// storeProvider may be nil; when non-nil the subscription management endpoints
// are mounted at /webhooks/subscriptions.
// adminAPIKey gates the DLQ and subscription management endpoints via
// Authorization: Bearer when non-empty; if empty those routes are unrestricted.
func NewRouter(
	reg *site.Registry,
	stores *storage.Registry,
	disp webhooks.Dispatcher,
	dlqStore *webhooks.DLQStore,
	dlqReplayer webhooks.DLQReplayer,
	storeProvider *webhooks.StoreProvider,
	adminAPIKey string,
) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)      // injects X-Request-Id for audit traceability
	r.Use(MetricsMiddleware)         // global request counter
	r.Use(SecurityHeadersMiddleware) // OWASP-recommended response headers
	r.Use(MaxBytesMiddleware(maxRequestBodyBytes)) // guard against oversized payloads

	// Non-site-scoped routes
	r.Get("/healthz", HealthzHandler)           // backward-compatible liveness
	r.Get("/healthz/live", LivezHandler)        // explicit liveness probe
	r.Get("/healthz/ready", ReadyzHandler(reg)) // readiness probe: registry loaded
	r.Get("/metrics", MetricsHandler)
	r.Get("/sites", ListSitesHandler(reg))

	// DLQ management routes — only mounted when a DLQ store is configured.
	if dlqStore != nil && dlqReplayer != nil {
		r.Route("/webhooks/dlq", func(r chi.Router) {
			r.Use(AdminAuthMiddleware(adminAPIKey))
			r.Get("/", ListDLQHandler(dlqStore))
			r.Post("/replay-all", ReplayAllDLQHandler(dlqStore, dlqReplayer))
			r.Get("/{id}", GetDLQHandler(dlqStore))
			r.Delete("/{id}", DeleteDLQHandler(dlqStore))
			r.Post("/{id}/replay", ReplayDLQHandler(dlqStore, dlqReplayer))
		})
	}

	// Subscription management routes — only mounted when a StoreProvider is configured.
	if storeProvider != nil {
		r.Route("/webhooks/subscriptions", func(r chi.Router) {
			r.Use(AdminAuthMiddleware(adminAPIKey))
			r.Get("/", ListSubscriptionsHandler(storeProvider))
			r.Post("/", CreateSubscriptionHandler(storeProvider))
			r.Get("/{id}", GetSubscriptionHandler(storeProvider))
			r.Patch("/{id}", PatchSubscriptionHandler(storeProvider))
			r.Delete("/{id}", DeleteSubscriptionHandler(storeProvider))
		})
	}

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
	})

	return r
}

