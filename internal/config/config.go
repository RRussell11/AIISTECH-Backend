package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const contractsBase = "contracts/sites"

// TenantConfig holds per-tenant credentials for a site operating in tenant mode.
type TenantConfig struct {
	TenantID string `yaml:"tenant_id" json:"tenant_id"`
	// APIKey is the bearer token required for requests from this tenant.
	// Excluded from JSON to prevent accidental key exposure.
	APIKey string `yaml:"api_key" json:"-"`
}

// SchemaConfig defines simple required-field validation rules for a resource
// type. All named fields must be present as top-level keys in the JSON body;
// missing fields cause a 422 Unprocessable Entity response.
type SchemaConfig struct {
	// Required is the list of top-level JSON field names that must be present
	// in every POST body for this resource type.
	Required []string `yaml:"required" json:"required"`
}

// SiteConfig holds per-site configuration loaded from contracts/sites/<site_id>/config.yaml.
type SiteConfig struct {
	SiteID   string            `yaml:"site_id"  json:"site_id"`
	Settings map[string]string `yaml:"settings" json:"settings"`
	// APIKey is the bearer token required for mutating requests to this site.
	// When empty, authentication is disabled for the site.
	// This field is intentionally excluded from JSON serialisation to prevent
	// accidental key exposure via the /config endpoint.
	APIKey string `yaml:"api_key" json:"-"`
	// Tenants enables tenant mode when non-empty. Each entry maps a tenant ID
	// to its dedicated API key. All requests must include a matching
	// X-Tenant-ID header and Authorization: Bearer <tenant_api_key>.
	Tenants []TenantConfig `yaml:"tenants" json:"tenants"`
	// EventSchema defines optional required-field validation for POST /events.
	// When nil, no schema validation is performed.
	EventSchema *SchemaConfig `yaml:"event_schema" json:"event_schema,omitempty"`
	// ArtifactSchema defines optional required-field validation for POST /artifacts.
	// When nil, no schema validation is performed.
	ArtifactSchema *SchemaConfig `yaml:"artifact_schema" json:"artifact_schema,omitempty"`
}

// ConfigPath returns the conventional path for a site's config file, relative to CWD.
func ConfigPath(siteID string) string {
	return filepath.Join(contractsBase, siteID, "config.yaml")
}

// Load reads and parses the config file at path for siteID.
// If the file does not exist, an empty SiteConfig is returned without error.
func Load(siteID, path string) (SiteConfig, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return SiteConfig{SiteID: siteID, Settings: map[string]string{}}, nil
	}
	if err != nil {
		return SiteConfig{}, fmt.Errorf("reading site config %q: %w", path, err)
	}

	var cfg SiteConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return SiteConfig{}, fmt.Errorf("parsing site config %q: %w", path, err)
	}
	if cfg.SiteID == "" {
		cfg.SiteID = siteID
	}
	if cfg.Settings == nil {
		cfg.Settings = map[string]string{}
	}
	return cfg, nil
}
