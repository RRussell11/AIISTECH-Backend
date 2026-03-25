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

// ---- AtomicRegistry tests ----

func TestAtomicRegistry_LoadAndContains(t *testing.T) {
	p := writeTempRegistry(t, `
default_site_id: local
sites:
  - site_id: local
  - site_id: staging
`)
	reg, err := site.LoadRegistry(p)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	ar := site.NewAtomicRegistry(reg)

	if !ar.Contains("local") {
		t.Error("should contain 'local'")
	}
	if !ar.Contains("staging") {
		t.Error("should contain 'staging'")
	}
	if ar.Contains("unknown") {
		t.Error("should not contain 'unknown'")
	}
	if ar.DefaultSiteID() != "local" {
		t.Errorf("DefaultSiteID = %q, want %q", ar.DefaultSiteID(), "local")
	}
}

func TestAtomicRegistry_HotSwap(t *testing.T) {
	p1 := writeTempRegistry(t, `
default_site_id: local
sites:
  - site_id: local
`)
	reg1, err := site.LoadRegistry(p1)
	if err != nil {
		t.Fatalf("LoadRegistry p1: %v", err)
	}
	ar := site.NewAtomicRegistry(reg1)

	// Verify initial state.
	if !ar.Contains("local") {
		t.Error("initial: should contain 'local'")
	}
	if ar.Contains("staging") {
		t.Error("initial: should not contain 'staging'")
	}

	// Swap to a registry that has 'staging' but not 'local'.
	p2 := writeTempRegistry(t, `
default_site_id: staging
sites:
  - site_id: staging
`)
	reg2, err := site.LoadRegistry(p2)
	if err != nil {
		t.Fatalf("LoadRegistry p2: %v", err)
	}
	ar.Store(reg2)

	// Verify swapped state.
	if ar.Contains("local") {
		t.Error("after swap: should not contain 'local'")
	}
	if !ar.Contains("staging") {
		t.Error("after swap: should contain 'staging'")
	}
	if ar.DefaultSiteID() != "staging" {
		t.Errorf("after swap: DefaultSiteID = %q, want %q", ar.DefaultSiteID(), "staging")
	}
}

func TestAtomicRegistry_SiteIDs(t *testing.T) {
	p := writeTempRegistry(t, `
default_site_id: alpha
sites:
  - site_id: alpha
  - site_id: beta
`)
	reg, err := site.LoadRegistry(p)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	ar := site.NewAtomicRegistry(reg)
	ids := ar.SiteIDs()
	if len(ids) != 2 {
		t.Errorf("SiteIDs len = %d, want 2", len(ids))
	}
}
