package site

import (
	"fmt"
	"os"
	"sync/atomic"

	"gopkg.in/yaml.v3"
)

// RegistryEntry represents a single site entry in the registry file.
type RegistryEntry struct {
	SiteID string `yaml:"site_id"`
}

// registryFile is the raw structure parsed from sites.yaml.
type registryFile struct {
	DefaultSiteID string          `yaml:"default_site_id"`
	Sites         []RegistryEntry `yaml:"sites"`
}

// Registry holds the parsed site registry.
type Registry struct {
	DefaultSiteID string
	sites         map[string]struct{}
}

// LoadRegistry reads and parses the given sites.yaml file.
func LoadRegistry(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading site registry %q: %w", path, err)
	}

	var rf registryFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return nil, fmt.Errorf("parsing site registry %q: %w", path, err)
	}

	if rf.DefaultSiteID == "" {
		return nil, fmt.Errorf("site registry %q: default_site_id is required", path)
	}
	if len(rf.Sites) == 0 {
		return nil, fmt.Errorf("site registry %q: sites list is empty", path)
	}

	reg := &Registry{
		DefaultSiteID: rf.DefaultSiteID,
		sites:         make(map[string]struct{}, len(rf.Sites)),
	}
	for _, entry := range rf.Sites {
		if err := Validate(entry.SiteID); err != nil {
			return nil, fmt.Errorf("site registry %q: entry %q: %w", path, entry.SiteID, err)
		}
		reg.sites[entry.SiteID] = struct{}{}
	}

	if _, ok := reg.sites[reg.DefaultSiteID]; !ok {
		return nil, fmt.Errorf("site registry %q: default_site_id %q not found in sites list", path, reg.DefaultSiteID)
	}

	return reg, nil
}

// Contains reports whether siteID exists in the registry.
func (r *Registry) Contains(siteID string) bool {
	_, ok := r.sites[siteID]
	return ok
}

// SiteIDs returns all registered site IDs.
func (r *Registry) SiteIDs() []string {
	ids := make([]string, 0, len(r.sites))
	for id := range r.sites {
		ids = append(ids, id)
	}
	return ids
}

// AtomicRegistry is a concurrency-safe wrapper around *Registry that supports
// atomic hot-swapping of the underlying registry without restarting the server.
// All middleware and handler closures that capture an *AtomicRegistry will
// automatically see the new registry after a Store call (e.g. triggered by
// SIGHUP).
type AtomicRegistry struct {
	p atomic.Pointer[Registry]
}

// NewAtomicRegistry creates an AtomicRegistry initialised with r.
// r must not be nil.
func NewAtomicRegistry(r *Registry) *AtomicRegistry {
	ar := &AtomicRegistry{}
	ar.p.Store(r)
	return ar
}

// Load returns the current *Registry.  The returned pointer is always non-nil.
func (ar *AtomicRegistry) Load() *Registry { return ar.p.Load() }

// Store atomically replaces the current registry with r.
// r must not be nil.
func (ar *AtomicRegistry) Store(r *Registry) { ar.p.Store(r) }

// Contains reports whether siteID exists in the current registry.
func (ar *AtomicRegistry) Contains(siteID string) bool { return ar.p.Load().Contains(siteID) }

// SiteIDs returns all registered site IDs from the current registry.
func (ar *AtomicRegistry) SiteIDs() []string { return ar.p.Load().SiteIDs() }

// DefaultSiteID returns the default site ID from the current registry.
func (ar *AtomicRegistry) DefaultSiteID() string { return ar.p.Load().DefaultSiteID }
