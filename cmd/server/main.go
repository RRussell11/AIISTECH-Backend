package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	sitehttp "github.com/RRussell11/AIISTECH-Backend/internal/http"
	"github.com/RRussell11/AIISTECH-Backend/internal/site"
)

const defaultRegistryPath = "contracts/shared/sites.yaml"
const defaultAddr = ":8080"

func main() {
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

	slog.Info("starting server", "addr", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		slog.Error("server error", "error", err)
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}
