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

func TestLoad_APIKey(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	content := "site_id: staging\napi_key: secret-key-abc\nsettings:\n  env: staging\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := config.Load("staging", p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIKey != "secret-key-abc" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "secret-key-abc")
	}
}

func TestLoad_NoAPIKey(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	content := "site_id: local\nsettings:\n  env: development\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := config.Load("local", p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIKey != "" {
		t.Errorf("APIKey = %q, want empty (no auth for local)", cfg.APIKey)
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

// TestLoad_EventSchema verifies that event_schema.required is parsed correctly.
func TestLoad_EventSchema(t *testing.T) {
dir := t.TempDir()
p := filepath.Join(dir, "config.yaml")
yaml := `site_id: local
event_schema:
  required:
    - type
    - source
artifact_schema:
  required:
    - name
`
if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
t.Fatalf("WriteFile: %v", err)
}

cfg, err := config.Load("local", p)
if err != nil {
t.Fatalf("Load: %v", err)
}
if cfg.EventSchema == nil {
t.Fatal("EventSchema should not be nil")
}
if len(cfg.EventSchema.Required) != 2 {
t.Errorf("EventSchema.Required len = %d, want 2", len(cfg.EventSchema.Required))
}
if cfg.ArtifactSchema == nil {
t.Fatal("ArtifactSchema should not be nil")
}
if cfg.ArtifactSchema.Required[0] != "name" {
t.Errorf("ArtifactSchema.Required[0] = %q, want %q", cfg.ArtifactSchema.Required[0], "name")
}
}

// TestLoad_NoSchema verifies that omitting schemas leaves them nil.
func TestLoad_NoSchema(t *testing.T) {
dir := t.TempDir()
p := filepath.Join(dir, "config.yaml")
if err := os.WriteFile(p, []byte("site_id: local\n"), 0o600); err != nil {
t.Fatalf("WriteFile: %v", err)
}

cfg, err := config.Load("local", p)
if err != nil {
t.Fatalf("Load: %v", err)
}
if cfg.EventSchema != nil {
t.Error("EventSchema should be nil when not configured")
}
if cfg.ArtifactSchema != nil {
t.Error("ArtifactSchema should be nil when not configured")
}
}
