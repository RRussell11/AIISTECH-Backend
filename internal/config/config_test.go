package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/RRussell11/AIISTECH-Backend/internal/config"
)

func TestLoad_NoFile(t *testing.T) {
	cfg, err := config.Load("local", "/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SiteID != "local" {
		t.Errorf("SiteID = %q, want %q", cfg.SiteID, "local")
	}
	if cfg.Settings == nil {
		t.Error("Settings should not be nil")
	}
	if len(cfg.Settings) != 0 {
		t.Errorf("Settings should be empty, got %v", cfg.Settings)
	}
}

func TestLoad_WithFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	content := "site_id: staging\nsettings:\n  env: staging\n  log_level: info\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := config.Load("staging", p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SiteID != "staging" {
		t.Errorf("SiteID = %q, want %q", cfg.SiteID, "staging")
	}
	if cfg.Settings["env"] != "staging" {
		t.Errorf("settings.env = %q, want %q", cfg.Settings["env"], "staging")
	}
	if cfg.Settings["log_level"] != "info" {
		t.Errorf("settings.log_level = %q, want %q", cfg.Settings["log_level"], "info")
	}
}

func TestLoad_DefaultsSiteIDWhenAbsentFromFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("settings:\n  x: y\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := config.Load("prod", p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SiteID != "prod" {
		t.Errorf("SiteID = %q, want %q (should default to passed siteID)", cfg.SiteID, "prod")
	}
}

func TestLoad_NilSettingsNormalizedToEmptyMap(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("site_id: local\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := config.Load("local", p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Settings == nil {
		t.Error("Settings should not be nil even when absent from file")
	}
}
