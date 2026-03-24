package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/RRussell11/AIISTECH-Backend/internal/site"
	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

// OpsConfig holds operational middleware configuration read from environment
// variables (or test defaults) and passed to NewRouter.
type OpsConfig struct {
	// CORSAllowOrigins is the list of allowed CORS origins.
	// When empty, CORS headers are not written.
	CORSAllowOrigins []string

	// MaxBodyBytes is the maximum request body size in bytes for mutating
	// requests. Defaults to 1 MiB (1048576) when zero.
	MaxBodyBytes int64

	// RateLimitRPS is the sustained rate (requests per second) for the
	// per-IP rate limiter applied to mutating requests.
	// Defaults to 10 when zero.
	RateLimitRPS float64

	// RateLimitBurst is the maximum burst for the rate limiter.
	// Defaults to 20 when zero.
	RateLimitBurst int
}

// NewRouter builds and returns the application HTTP router.
// disp may be nil; when non-nil it receives an "audit.write" webhook event
// for every state-mutating request processed by AuditMiddleware.
func NewRouter(reg *site.Registry, stores *storage.Registry, disp webhooks.Dispatcher, opts ...OpsConfig) http.Handler {
	cfg := OpsConfig{
		MaxBodyBytes:   1 << 20, // 1 MiB
		RateLimitRPS:   10,
		RateLimitBurst: 20,
	}
	if len(opts) > 0 {
		if opts[0].MaxBodyBytes > 0 {
			cfg.MaxBodyBytes = opts[0].MaxBodyBytes
		}
		if opts[0].RateLimitRPS > 0 {
			cfg.RateLimitRPS = opts[0].RateLimitRPS
		}
		if opts[0].RateLimitBurst > 0 {
			cfg.RateLimitBurst = opts[0].RateLimitBurst
		}
		cfg.CORSAllowOrigins = opts[0].CORSAllowOrigins
	}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID) // injects X-Request-Id for audit traceability
	r.Use(MetricsMiddleware)   // global request counter
	r.Use(CORSMiddleware(cfg.CORSAllowOrigins))
	r.Use(MaxBodyMiddleware(cfg.MaxBodyBytes))
	r.Use(RateLimitMiddleware(cfg.RateLimitRPS, cfg.RateLimitBurst))

	// Non-site-scoped routes
	r.Get("/healthz", HealthzHandler)           // backward-compatible liveness
	r.Get("/healthz/live", LivezHandler)        // explicit liveness probe
	r.Get("/healthz/ready", ReadyzHandler(reg)) // readiness probe: registry loaded
	r.Get("/metrics", MetricsHandler)
	r.Get("/sites", ListSitesHandler(reg))
	r.Get("/version", VersionHandler)

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

