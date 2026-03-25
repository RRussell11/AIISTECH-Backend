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

	stores := storage.NewRegistry()

	// Webhook dispatcher — optional. Configure via env vars:
	//   AIISTECH_WEBHOOK_BASE_URL          — PhaseMirror-HQ subscriptions base URL
	//   AIISTECH_WEBHOOK_TOKEN             — bearer token for subscription API (optional)
	//   AIISTECH_SERVICE_NAME              — logical service name (default: "aiistech-backend")
	//   AIISTECH_WEBHOOK_CACHE_TTL_SECONDS — subscription cache TTL in seconds (0 = no cache)
	var disp webhooks.Dispatcher
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
				provider = webhooks.NewCachingProvider(provider, ttl)
				slog.Info("webhook subscription caching enabled", "ttl", ttl)
			}
		}

		wd := webhooks.NewWorkerDispatcher(webhooks.Config{
			ServiceName: serviceName,
		}, provider)
		disp = wd
		slog.Info("webhook dispatcher started", "service", serviceName, "base_url", webhookBase)
	}

	addr := defaultAddr
	if v := os.Getenv("AIISTECH_ADDR"); v != "" {
		addr = v
	}

	router := sitehttp.NewRouter(reg, stores, disp)

	srv := &http.Server{
		Addr:    addr,
		Handler: router,
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

	if disp != nil {
		if err := disp.Close(); err != nil {
			slog.Error("webhook dispatcher close failed", "error", err)
		}
	}
	stores.CloseAll()
	slog.Info("server stopped")
}

