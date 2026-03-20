package state_test

import (
	"strings"
	"testing"

	"github.com/RRussell11/AIISTECH-Backend/internal/state"
)

func TestStateRoot(t *testing.T) {
	root := state.StateRoot("local")
	if !strings.HasSuffix(root, "local") {
		t.Errorf("StateRoot = %q, expected to end with 'local'", root)
	}
	if !strings.Contains(root, "var/state") {
		t.Errorf("StateRoot = %q, expected to contain 'var/state'", root)
	}
}

func TestEventsDirUnderStateRoot(t *testing.T) {
	root := state.StateRoot("local")
	evDir := state.EventsDir("local")
	if !strings.HasPrefix(evDir, root) {
		t.Errorf("EventsDir %q should be under StateRoot %q", evDir, root)
	}
}

func TestArtifactsDirUnderStateRoot(t *testing.T) {
	root := state.StateRoot("local")
	artDir := state.ArtifactsDir("local")
	if !strings.HasPrefix(artDir, root) {
		t.Errorf("ArtifactsDir %q should be under StateRoot %q", artDir, root)
	}
}

func TestAuditDirUnderStateRoot(t *testing.T) {
	root := state.StateRoot("local")
	auditDir := state.AuditDir("local")
	if !strings.HasPrefix(auditDir, root) {
		t.Errorf("AuditDir %q should be under StateRoot %q", auditDir, root)
	}
}

func TestSiteIsolation(t *testing.T) {
	sites := []string{"local", "staging", "prod"}
	roots := make(map[string]string, len(sites))
	for _, s := range sites {
		roots[s] = state.StateRoot(s)
	}
	// Each site's root must be unique.
	seen := make(map[string]string)
	for s, root := range roots {
		if other, exists := seen[root]; exists {
			t.Errorf("StateRoot collision: sites %q and %q both map to %q", s, other, root)
		}
		seen[root] = s
	}
}
