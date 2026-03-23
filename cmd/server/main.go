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
	"strings"
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

	stores := storage.NewRegistry()

	// Webhook dispatcher — optional. Configure via env vars:
	//   AIISTECH_WEBHOOK_BASE_URL                — PhaseMirror-HQ subscriptions base URL
	//   AIISTECH_WEBHOOK_TOKEN                   — bearer token for subscription API (optional)
	//   AIISTECH_SERVICE_NAME                    — logical service name (default: "aiistech-backend")
	//   AIISTECH_WEBHOOK_CACHE_TTL_SECONDS       — subscription cache TTL in seconds (default: 30)
	//   AIISTECH_WEBHOOK_POLL_INTERVAL_SECONDS   — background subscription poll interval in seconds (default: 0 = disabled)
	var disp webhooks.Dispatcher
	var provider *webhooks.CachingProvider
	if webhookBase := os.Getenv("AIISTECH_WEBHOOK_BASE_URL"); webhookBase != "" {
		serviceName := os.Getenv("AIISTECH_SERVICE_NAME")
		if serviceName == "" {
			serviceName = "aiistech-backend"
		}
		cacheTTL := time.Duration(envInt64("AIISTECH_WEBHOOK_CACHE_TTL_SECONDS", 30)) * time.Second
		pollInterval := time.Duration(envInt64("AIISTECH_WEBHOOK_POLL_INTERVAL_SECONDS", 0)) * time.Second
		provider = webhooks.NewCachingProvider(
			webhooks.NewRemoteProvider(webhookBase, os.Getenv("AIISTECH_WEBHOOK_TOKEN"), 0),
			cacheTTL,
			pollInterval,
		)
		wd := webhooks.NewWorkerDispatcher(webhooks.Config{
			ServiceName: serviceName,
		}, provider)
		disp = wd
		slog.Info("webhook dispatcher started", "service", serviceName, "base_url", webhookBase,
			"cache_ttl", cacheTTL, "poll_interval", pollInterval)
	}

	addr := defaultAddr
	if v := os.Getenv("AIISTECH_ADDR"); v != "" {
		addr = v
	}

	// Ops middleware configuration.
	opsCfg := sitehttp.OpsConfig{
		MaxBodyBytes:   envInt64("AIISTECH_MAX_BODY_BYTES", 1<<20),
		RateLimitRPS:   envFloat64("AIISTECH_RATE_LIMIT_RPS", 10),
		RateLimitBurst: int(envInt64("AIISTECH_RATE_LIMIT_BURST", 20)),
	}
	if origins := os.Getenv("AIISTECH_CORS_ALLOW_ORIGINS"); origins != "" {
		for _, o := range strings.Split(origins, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				opsCfg.CORSAllowOrigins = append(opsCfg.CORSAllowOrigins, o)
			}
		}
	}

	router := sitehttp.NewRouter(reg, stores, disp, opsCfg)

	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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

	if provider != nil {
		if err := provider.Close(); err != nil {
			slog.Error("webhook subscription provider close failed", "error", err)
		}
	}
	if disp != nil {
		if err := disp.Close(); err != nil {
			slog.Error("webhook dispatcher close failed", "error", err)
		}
	}
	stores.CloseAll()
	slog.Info("server stopped")
}

// envInt64 reads an environment variable as int64, returning fallback on error or absence.
func envInt64(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil && n > 0 {
			return n
		}
		slog.Warn("invalid env value, using default", "key", key, "value", v)
	}
	return fallback
}

// envFloat64 reads an environment variable as float64, returning fallback on error or absence.
func envFloat64(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err == nil && f > 0 {
			return f
		}
		slog.Warn("invalid env value, using default", "key", key, "value", v)
	}
	return fallback
}
