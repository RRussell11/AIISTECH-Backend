package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	sitehttp "github.com/RRussell11/AIISTECH-Backend/internal/http"
	"github.com/RRussell11/AIISTECH-Backend/internal/site"
	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

const defaultRegistryPath = "contracts/shared/sites.yaml"
const defaultAddr = ":8080"

func main() {
	// Configure structured logging level from AIISTECH_LOG_LEVEL (DEBUG/INFO/WARN/ERROR).
	logLevel := new(slog.LevelVar) // defaults to INFO
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))
	if v := os.Getenv("AIISTECH_LOG_LEVEL"); v != "" {
		var l slog.Level
		if err := l.UnmarshalText([]byte(v)); err != nil {
			slog.Warn("invalid AIISTECH_LOG_LEVEL, using INFO", "value", v)
		} else {
			logLevel.Set(l)
		}
	}

	registryPath := defaultRegistryPath
	if v := os.Getenv("AIISTECH_REGISTRY_PATH"); v != "" {
		registryPath = v
	}

	reg, err := site.LoadRegistry(registryPath)
	if err != nil {
		slog.Error("failed to load site registry", "path", registryPath, "error", err)
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
	slog.Info("site registry loaded", "default_site_id", reg.DefaultSiteID, "sites", reg.SiteIDs())

	// AtomicRegistry wraps the registry so that a SIGHUP can hot-swap it
	// without restarting the process or draining in-flight requests.
	atomicReg := site.NewAtomicRegistry(reg)

	stores := storage.NewRegistry()

	// DLQ sink — always created so that the webhook DLQ HTTP endpoints work
	// regardless of whether the outbound webhook dispatcher is configured.
	dlqSink := webhooks.NewStoreDLQSink(stores)

	// Webhook dispatcher — optional. Configure via env vars:
	//   AIISTECH_WEBHOOK_BASE_URL             — PhaseMirror-HQ subscriptions base URL
	//   AIISTECH_WEBHOOK_TOKEN                — bearer token for subscription API (optional)
	//   AIISTECH_SERVICE_NAME                 — logical service name (default: "aiistech-backend")
	//   AIISTECH_WEBHOOK_CACHE_TTL_SECONDS    — subscription cache TTL in seconds (0 = no cache)
	//   AIISTECH_WEBHOOK_POLL_INTERVAL_SECONDS — background refresh interval (0 = lazy TTL only)
	var disp webhooks.Dispatcher
	var cachingProvider *webhooks.CachingProvider // non-nil only when caching is enabled
	if webhookBase := os.Getenv("AIISTECH_WEBHOOK_BASE_URL"); webhookBase != "" {
		serviceName := os.Getenv("AIISTECH_SERVICE_NAME")
		if serviceName == "" {
			serviceName = "aiistech-backend"
		}
		var provider webhooks.Provider = webhooks.NewRemoteProvider(webhookBase, os.Getenv("AIISTECH_WEBHOOK_TOKEN"), 0)

		// Wrap with a caching layer when AIISTECH_WEBHOOK_CACHE_TTL_SECONDS is set.
		if ttlStr := os.Getenv("AIISTECH_WEBHOOK_CACHE_TTL_SECONDS"); ttlStr != "" {
			if ttlSec, err := strconv.Atoi(ttlStr); err != nil || ttlSec <= 0 {
				slog.Warn("invalid AIISTECH_WEBHOOK_CACHE_TTL_SECONDS, subscription caching disabled", "value", ttlStr)
			} else {
				ttl := time.Duration(ttlSec) * time.Second

				// Optional background poll interval.
				var pollInterval time.Duration
				if piStr := os.Getenv("AIISTECH_WEBHOOK_POLL_INTERVAL_SECONDS"); piStr != "" {
					if piSec, err := strconv.Atoi(piStr); err != nil || piSec <= 0 {
						slog.Warn("invalid AIISTECH_WEBHOOK_POLL_INTERVAL_SECONDS, background polling disabled", "value", piStr)
					} else {
						pollInterval = time.Duration(piSec) * time.Second
					}
				}

				cp := webhooks.NewCachingProvider(provider, ttl, pollInterval)
				provider = cp
				cachingProvider = cp
				if pollInterval > 0 {
					slog.Info("webhook subscription caching enabled with background polling", "ttl", ttl, "poll_interval", pollInterval)
				} else {
					slog.Info("webhook subscription caching enabled", "ttl", ttl)
				}
			}
		}

		wd := webhooks.NewWorkerDispatcher(webhooks.Config{
			ServiceName: serviceName,
			DLQ:         dlqSink, // failed deliveries go to per-site webhook_dlq bucket
		}, provider)
		disp = wd
		slog.Info("webhook dispatcher started with DLQ", "service", serviceName, "base_url", webhookBase)
	}

	addr := defaultAddr
	if v := os.Getenv("AIISTECH_ADDR"); v != "" {
		addr = v
	}

	// Ops middleware — all optional, disabled when env vars are absent.
	//   AIISTECH_CORS_ALLOW_ORIGINS — comma-separated allowed origins; "*" = any
	//   AIISTECH_MAX_BODY_BYTES     — hard cap on request body size (bytes)
	//   AIISTECH_RATE_LIMIT_RPS     — steady-state requests/second per remote IP
	//   AIISTECH_RATE_LIMIT_BURST   — token-bucket burst (defaults to max(1,RPS))
	ops := sitehttp.OpsConfig{
		CORSOrigins: os.Getenv("AIISTECH_CORS_ALLOW_ORIGINS"),
		// DLQ sink — wired unconditionally so that the GET/DELETE/replay endpoints
		// work even without a webhook dispatcher configured.
		DLQ: dlqSink,
		// LogLevel — the same *slog.LevelVar used at startup so that
		// PUT /debug/log-level takes effect process-wide immediately.
		LogLevel: logLevel,
	}
	if v := os.Getenv("AIISTECH_MAX_BODY_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err != nil || n <= 0 {
			slog.Warn("invalid AIISTECH_MAX_BODY_BYTES, body-size limit disabled", "value", v)
		} else {
			ops.MaxBodyBytes = n
			slog.Info("request body size limit enabled", "max_bytes", n)
		}
	}
	if v := os.Getenv("AIISTECH_RATE_LIMIT_RPS"); v != "" {
		if rps, err := strconv.ParseFloat(v, 64); err != nil || rps <= 0 {
			slog.Warn("invalid AIISTECH_RATE_LIMIT_RPS, rate limiting disabled", "value", v)
		} else {
			ops.RateLimitRPS = rps
			if bv := os.Getenv("AIISTECH_RATE_LIMIT_BURST"); bv != "" {
				if burst, err := strconv.Atoi(bv); err != nil || burst <= 0 {
					slog.Warn("invalid AIISTECH_RATE_LIMIT_BURST, using default", "value", bv)
				} else {
					ops.RateLimitBurst = burst
				}
			}
			slog.Info("rate limiting enabled", "rps", rps, "burst", ops.RateLimitBurst)
		}
	}
	if ops.CORSOrigins != "" {
		slog.Info("CORS enabled", "origins", ops.CORSOrigins)
	}

	router := sitehttp.NewRouter(atomicReg, stores, disp, ops)

	srv := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// SIGHUP triggers a hot-reload of the site registry from disk.
	// In-flight requests continue with the old registry; new requests see the
	// updated one as soon as Store completes.
	sighupCh := make(chan os.Signal, 1)
	signal.Notify(sighupCh, syscall.SIGHUP)
	go func() {
		for range sighupCh {
			slog.Info("SIGHUP received, reloading site registry", "path", registryPath)
			newReg, err := site.LoadRegistry(registryPath)
			if err != nil {
				slog.Error("hot-reload failed: keeping existing registry", "path", registryPath, "error", err)
				continue
			}
			atomicReg.Store(newReg)
			slog.Info("site registry hot-reloaded", "default_site_id", newReg.DefaultSiteID, "sites", newReg.SiteIDs())
		}
	}()

	go func() {
		slog.Info("starting server", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutdown signal received, draining connections")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}

	if cachingProvider != nil {
		cachingProvider.Close()
	}
	if disp != nil {
		if err := disp.Close(); err != nil {
			slog.Error("webhook dispatcher close failed", "error", err)
		}
	}
	stores.CloseAll()
	slog.Info("server stopped")
}

