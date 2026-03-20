package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	sitehttp "github.com/RRussell11/AIISTECH-Backend/internal/http"
	"github.com/RRussell11/AIISTECH-Backend/internal/site"
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

	addr := defaultAddr
	if v := os.Getenv("AIISTECH_ADDR"); v != "" {
		addr = v
	}

	router := sitehttp.NewRouter(reg)

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
	slog.Info("server stopped")
}
