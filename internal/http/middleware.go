package http

import (
	"context"
	"expvar"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"golang.org/x/time/rate"

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
//
// When the site config has Tenants configured (tenant mode), every request must
// include a valid X-Tenant-ID header and a matching Authorization: Bearer token.
// Unknown tenant IDs or missing/mismatched tokens are rejected immediately.
func SiteMiddleware(reg *site.AtomicRegistry, stores *storage.Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawID := chi.URLParam(r, "site_id")

			// Load the current registry snapshot for this request. A concurrent
			// SIGHUP reload will swap the pointer; future requests see the new
			// registry while in-flight requests continue with the old snapshot.
			siteID, err := site.Resolve(rawID, reg.Load())
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

			sc := site.SiteContext{SiteID: siteID, Store: store, APIKey: cfg.APIKey, Config: cfg}

			// Tenant mode: enforce per-tenant credentials on every request.
			if len(cfg.Tenants) > 0 {
				tenantID := strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
				if tenantID == "" {
					http.Error(w, "X-Tenant-ID header required", http.StatusBadRequest)
					return
				}
				tenantKey, found := lookupTenantKey(cfg.Tenants, tenantID)
				if !found {
					http.Error(w, "unknown tenant", http.StatusBadRequest)
					return
				}
				token, ok := bearerToken(r)
				if !ok || token != tenantKey {
					slog.Warn("tenant authentication failed", "site_id", siteID, "tenant_id", tenantID, "method", r.Method, "path", r.URL.Path)
					w.Header().Set("WWW-Authenticate", `Bearer realm="aiistech"`)
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				sc.TenantID = tenantID
			}

			ctx := site.NewContext(r.Context(), sc)
			slog.Info("request", "method", r.Method, "path", r.URL.Path, "site_id", siteID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// lookupTenantKey returns the APIKey for the given tenantID from the list of
// configured tenants. Returns ("", false) when the tenant is not found.
func lookupTenantKey(tenants []config.TenantConfig, tenantID string) (string, bool) {
	for _, t := range tenants {
		if t.TenantID == tenantID {
			return t.APIKey, true
		}
	}
	return "", false
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
				TenantID:  sc.TenantID,
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
					SiteID:    sc.SiteID,
					TenantID:  sc.TenantID,
					Type:      "audit.write",
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

// loggerKey is the unexported context key used to store the request-scoped
// *slog.Logger injected by TraceMiddleware.
type loggerKey struct{}

// LoggerFromContext returns the request-scoped *slog.Logger stored by
// TraceMiddleware. It falls back to slog.Default() when no logger has been
// stored in the context (e.g. unit tests that bypass the middleware chain).
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// TraceMiddleware enriches every request with a request-scoped *slog.Logger
// that carries the X-Request-Id (injected by chi's RequestID middleware) as a
// "trace_id" field. The logger is stored in the request context and can be
// retrieved via LoggerFromContext. When the handler returns, a single
// structured log line is emitted containing "method", "path", "status", and
// "latency_ms" alongside the trace_id.
func TraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		traceID := chimiddleware.GetReqID(r.Context())
		logger := slog.Default().With("trace_id", traceID)
		ctx := context.WithValue(r.Context(), loggerKey{}, logger)

		sr := newStatusRecorder(w)
		next.ServeHTTP(sr, r.WithContext(ctx))

		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sr.status,
			"latency_ms", time.Since(start).Milliseconds(),
		)
	})
}

// OpsConfig holds optional operational settings for the router.
// Zero values disable the corresponding middleware.
type OpsConfig struct {
	// CORSOrigins is a comma-separated list of allowed origins for CORS
	// pre-flight and actual cross-origin requests.  "*" permits all origins.
	// An empty string disables CORS header injection.
	CORSOrigins string

	// MaxBodyBytes, when > 0, limits the size of every request body.
	// Requests whose body exceeds the limit receive 413 Request Entity Too Large.
	MaxBodyBytes int64

	// RateLimitRPS is the maximum number of requests per second allowed from a
	// single remote IP address.  A zero or negative value disables rate limiting.
	RateLimitRPS float64

	// RateLimitBurst is the token-bucket burst allowance. Values ≤ 0 default to
	// max(1, int(RateLimitRPS)).
	RateLimitBurst int

	// DLQ is the dead-letter queue sink used by the router's DLQ HTTP handlers.
	// When non-nil, the GET/DELETE/POST-replay endpoints for
	// /sites/{site_id}/webhooks/dlq are registered.
	DLQ webhooks.DLQSink

	// ReplayClient is the HTTP client used by the DLQ replay handler to re-POST
	// failed webhook payloads. When nil, a default client with a 10 s timeout
	// is used. Ignored when DLQ is nil.
	ReplayClient *http.Client

	// LogLevel is the runtime-adjustable slog level variable wired at startup.
	// When non-nil, GET /debug/log-level returns the current level and
	// PUT /debug/log-level updates it without a process restart.
	// When nil, GET still works (returns "INFO") but PUT returns 501.
	LogLevel *slog.LevelVar
}

// CORSMiddleware adds CORS headers to every response and handles pre-flight
// OPTIONS requests.  allowedOrigins is a comma-separated list of allowed origins;
// use "*" to permit any origin.  When allowedOrigins is empty no CORS headers are
// injected and the returned middleware is a transparent pass-through.
func CORSMiddleware(allowedOrigins string) func(http.Handler) http.Handler {
	origins := make(map[string]bool)
	wildcard := false
	for _, o := range strings.Split(allowedOrigins, ",") {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if o == "*" {
			wildcard = true
		}
		origins[o] = true
	}
	enabled := wildcard || len(origins) > 0

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !enabled {
				next.ServeHTTP(w, r)
				return
			}

			origin := r.Header.Get("Origin")
			if origin != "" && (wildcard || origins[origin]) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Tenant-ID, X-Request-Id")
			}

			// Handle pre-flight without calling downstream handlers.
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// MaxBodyMiddleware limits every request body to maxBytes.  When a body exceeds
// the limit the handler is called but http.MaxBytesReader ensures a read error
// occurs; chi's Recoverer middleware will surface a 413.  If maxBytes ≤ 0 the
// returned middleware is a transparent pass-through.
func MaxBodyMiddleware(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if maxBytes <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

// ipLimiters holds per-IP token-bucket limiters for RateLimitMiddleware.
type ipLimiters struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	rps      rate.Limit
	burst    int
}

func (il *ipLimiters) get(ip string) *rate.Limiter {
	il.mu.Lock()
	defer il.mu.Unlock()
	if l, ok := il.limiters[ip]; ok {
		return l
	}
	l := rate.NewLimiter(il.rps, il.burst)
	il.limiters[ip] = l
	return l
}

// RateLimitMiddleware applies a per-remote-IP token-bucket rate limiter.
// rps is the steady-state rate (requests per second); burst is the maximum
// instantaneous burst.  When the limiter is exhausted the request receives
// 429 Too Many Requests.  If rps ≤ 0 the returned middleware is a transparent
// pass-through.
func RateLimitMiddleware(rps float64, burst int) func(http.Handler) http.Handler {
	if rps <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	if burst <= 0 {
		burst = max(1, int(rps))
	}
	il := &ipLimiters{
		limiters: make(map[string]*rate.Limiter),
		rps:      rate.Limit(rps),
		burst:    burst,
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := remoteIP(r)
			if !il.get(ip).Allow() {
				slog.Warn("rate limit exceeded", "ip", ip, "path", r.URL.Path)
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// remoteIP extracts the client IP from r.RemoteAddr, stripping the port.
// It does not trust X-Forwarded-For to avoid header-spoofing attacks; operators
// running behind a trusted proxy should add a forwarding-aware middleware earlier
// in the chain.
func remoteIP(r *http.Request) string {
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
}

