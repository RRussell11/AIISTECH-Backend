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
	"github.com/RRussell11/AIISTECH-Backend/internal/storage"
	"github.com/RRussell11/AIISTECH-Backend/internal/webhooks"
)

const defaultRegistryPath = "contracts/shared/sites.yaml"
const defaultAddr = ":8080"

// defaultWebhookSubscriptionsDB is the path used for the webhook subscriptions
// bbolt database when AIISTECH_WEBHOOK_SUBSCRIPTIONS_DB is not set.
const defaultWebhookSubscriptionsDB = "var/state/webhooks/subscriptions.db"

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

	// Webhook dispatcher — optional.  Configure via env vars:
	//
	//   AIISTECH_WEBHOOK_BASE_URL            — PhaseMirror-HQ subscriptions base URL
	//                                          (enables RemoteProvider)
	//   AIISTECH_WEBHOOK_TOKEN               — bearer token for subscription API (optional)
	//   AIISTECH_SERVICE_NAME                — logical service name (default: "aiistech-backend")
	//   AIISTECH_WEBHOOK_STORE_PROVIDER=true — enable StoreProvider (local bbolt subscriptions)
	//   AIISTECH_WEBHOOK_SUBSCRIPTIONS_DB    — path to bbolt db for local subscriptions
	//                                          (default: var/state/webhooks/subscriptions.db)
	//
	// Provider selection rules:
	//   - Both URL and STORE_PROVIDER set → MultiProvider (union, deduplicated)
	//   - Only URL set                    → RemoteProvider
	//   - Only STORE_PROVIDER=true        → StoreProvider
	//   - Neither                         → no dispatcher (webhooks disabled)
	serviceName := os.Getenv("AIISTECH_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "aiistech-backend"
	}

	webhookBase := os.Getenv("AIISTECH_WEBHOOK_BASE_URL")
	useStore := os.Getenv("AIISTECH_WEBHOOK_STORE_PROVIDER") == "true"

	var disp webhooks.Dispatcher
	var dlqStore *webhooks.DLQStore
	var storeProvider *webhooks.StoreProvider

	switch {
	case webhookBase != "" && useStore:
		// MultiProvider: query both sources and deliver to all active subscriptions.
		subsStore := openWebhookSubscriptionsStore()
		remote := webhooks.NewRemoteProvider(webhookBase, os.Getenv("AIISTECH_WEBHOOK_TOKEN"), 0)
		storeProvider = webhooks.NewStoreProvider(subsStore)
		provider := webhooks.NewMultiProvider(storeProvider, remote)
		dlqStore = webhooks.NewDLQStore(subsStore)
		wd := webhooks.NewWorkerDispatcher(webhooks.Config{
			ServiceName: serviceName,
			DLQStore:    dlqStore,
		}, provider)
		disp = wd
		slog.Info("webhook dispatcher started (multi-provider)",
			"service", serviceName,
			"base_url", webhookBase,
			"subscriptions_db", subsDBPath(),
		)

	case webhookBase != "":
		// RemoteProvider only (backward-compatible default).
		provider := webhooks.NewRemoteProvider(webhookBase, os.Getenv("AIISTECH_WEBHOOK_TOKEN"), 0)
		wd := webhooks.NewWorkerDispatcher(webhooks.Config{ServiceName: serviceName}, provider)
		disp = wd
		slog.Info("webhook dispatcher started (remote-provider)",
			"service", serviceName,
			"base_url", webhookBase,
		)

	case useStore:
		// StoreProvider only: local bbolt subscriptions, no remote.
		subsStore := openWebhookSubscriptionsStore()
		storeProvider = webhooks.NewStoreProvider(subsStore)
		dlqStore = webhooks.NewDLQStore(subsStore)
		wd := webhooks.NewWorkerDispatcher(webhooks.Config{
			ServiceName: serviceName,
			DLQStore:    dlqStore,
		}, storeProvider)
		disp = wd
		slog.Info("webhook dispatcher started (store-provider)",
			"service", serviceName,
			"subscriptions_db", subsDBPath(),
		)
	}

	addr := defaultAddr
	if v := os.Getenv("AIISTECH_ADDR"); v != "" {
		addr = v
	}

	// Wire the DLQ replayer: WorkerDispatcher implements DLQReplayer when
	// cast via the Dispatcher interface. We need the concrete type.
	var dlqReplayer webhooks.DLQReplayer
	if wd, ok := disp.(*webhooks.WorkerDispatcher); ok {
		dlqReplayer = wd
	}

	// Admin API key for the DLQ and subscription management endpoints.
	// When unset those routes are still accessible (backward compat), but a
	// warning is logged so operators are aware.
	adminAPIKey := os.Getenv("AIISTECH_ADMIN_API_KEY")
	if adminAPIKey == "" && (dlqStore != nil || storeProvider != nil) {
		slog.Warn("AIISTECH_ADMIN_API_KEY is not set; /webhooks/dlq and /webhooks/subscriptions are unauthenticated")
	}

	router := sitehttp.NewRouter(reg, stores, disp, dlqStore, dlqReplayer, storeProvider, adminAPIKey)

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

// subsDBPath returns the configured webhook subscriptions database path,
// falling back to defaultWebhookSubscriptionsDB when the env var is not set.
func subsDBPath() string {
	if v := os.Getenv("AIISTECH_WEBHOOK_SUBSCRIPTIONS_DB"); v != "" {
		return v
	}
	return defaultWebhookSubscriptionsDB
}

// openWebhookSubscriptionsStore opens the bbolt database for webhook
// subscriptions, creating parent directories as needed.
// It exits the process on error.
func openWebhookSubscriptionsStore() storage.Store {
	path := subsDBPath()
	s, err := storage.Open(path)
	if err != nil {
		slog.Error("failed to open webhook subscriptions store", "path", path, "error", err)
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
	return s
}


