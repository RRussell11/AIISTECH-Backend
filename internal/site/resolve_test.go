package site_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/RRussell11/AIISTECH-Backend/internal/site"
)

func makeRegistry(t *testing.T) *site.Registry {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "sites.yaml")
	content := `
default_site_id: local
sites:
  - site_id: local
  - site_id: staging
`
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("writing registry: %v", err)
	}
	reg, err := site.LoadRegistry(p)
	if err != nil {
		t.Fatalf("loading registry: %v", err)
	}
	return reg
}

func TestResolve_Explicit(t *testing.T) {
	reg := makeRegistry(t)
	id, err := site.Resolve("staging", reg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "staging" {
		t.Errorf("got %q, want %q", id, "staging")
	}
}

func TestResolve_EnvOverride(t *testing.T) {
	reg := makeRegistry(t)
	t.Setenv("AIISTECH_SITE_ID", "staging")
	id, err := site.Resolve("", reg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "staging" {
		t.Errorf("got %q, want %q", id, "staging")
	}
}

func TestResolve_Default(t *testing.T) {
	reg := makeRegistry(t)
	// ensure env var is unset
	t.Setenv("AIISTECH_SITE_ID", "")
	id, err := site.Resolve("", reg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "local" {
		t.Errorf("got %q, want %q", id, "local")
	}
}

func TestResolve_UnknownExplicit(t *testing.T) {
	reg := makeRegistry(t)
	_, err := site.Resolve("unknown", reg)
	if err == nil {
		t.Fatal("expected error for unknown site_id")
	}
}

func TestResolve_InvalidExplicit(t *testing.T) {
	reg := makeRegistry(t)
	_, err := site.Resolve("bad/id", reg)
	if err == nil {
		t.Fatal("expected error for invalid site_id")
	}
}
