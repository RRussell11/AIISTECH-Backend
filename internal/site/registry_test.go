package site_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/RRussell11/AIISTECH-Backend/internal/site"
)

func writeTempRegistry(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "sites.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("writing temp registry: %v", err)
	}
	return p
}

func TestLoadRegistry_Valid(t *testing.T) {
	p := writeTempRegistry(t, `
default_site_id: local
sites:
  - site_id: local
  - site_id: staging
`)
	reg, err := site.LoadRegistry(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.DefaultSiteID != "local" {
		t.Errorf("default_site_id = %q, want %q", reg.DefaultSiteID, "local")
	}
	if !reg.Contains("local") {
		t.Error("registry should contain 'local'")
	}
	if !reg.Contains("staging") {
		t.Error("registry should contain 'staging'")
	}
	if reg.Contains("unknown") {
		t.Error("registry should not contain 'unknown'")
	}
}

func TestLoadRegistry_MissingDefault(t *testing.T) {
	p := writeTempRegistry(t, `
sites:
  - site_id: local
`)
	_, err := site.LoadRegistry(p)
	if err == nil {
		t.Fatal("expected error for missing default_site_id")
	}
}

func TestLoadRegistry_DefaultNotInList(t *testing.T) {
	p := writeTempRegistry(t, `
default_site_id: missing
sites:
  - site_id: local
`)
	_, err := site.LoadRegistry(p)
	if err == nil {
		t.Fatal("expected error when default_site_id not in sites list")
	}
}

func TestLoadRegistry_EmptySites(t *testing.T) {
	p := writeTempRegistry(t, `
default_site_id: local
sites: []
`)
	_, err := site.LoadRegistry(p)
	if err == nil {
		t.Fatal("expected error for empty sites list")
	}
}

func TestLoadRegistry_InvalidSiteID(t *testing.T) {
	p := writeTempRegistry(t, `
default_site_id: local
sites:
  - site_id: local
  - site_id: "bad/id"
`)
	_, err := site.LoadRegistry(p)
	if err == nil {
		t.Fatal("expected error for invalid site_id in registry")
	}
}

func TestLoadRegistry_FileNotFound(t *testing.T) {
	_, err := site.LoadRegistry("/nonexistent/path/sites.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
