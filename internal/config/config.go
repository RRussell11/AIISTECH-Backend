package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const contractsBase = "contracts/sites"

// SiteConfig holds per-site configuration loaded from contracts/sites/<site_id>/config.yaml.
type SiteConfig struct {
	SiteID   string            `yaml:"site_id"  json:"site_id"`
	Settings map[string]string `yaml:"settings" json:"settings"`
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
