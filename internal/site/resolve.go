package site

import (
	"fmt"
	"log/slog"
	"os"
)

const envOverrideKey = "AIISTECH_SITE_ID"

// Resolve determines the effective site_id using the following precedence:
//  1. explicit (non-empty string provided by caller, e.g. from URL param)
//  2. AIISTECH_SITE_ID environment variable
//  3. default_site_id from registry
//
// The resolved ID is validated and must exist in the registry.
// A log message is emitted indicating the resolution source.
func Resolve(explicit string, reg *Registry) (string, error) {
	var siteID string
	var source string

	switch {
	case explicit != "":
		siteID = explicit
		source = "explicit"
	case os.Getenv(envOverrideKey) != "":
		siteID = os.Getenv(envOverrideKey)
		source = "env"
	default:
		siteID = reg.DefaultSiteID
		source = "registry_default"
	}

	if err := Validate(siteID); err != nil {
		return "", fmt.Errorf("resolved site_id %q (source=%s): %w", siteID, source, err)
	}

	if !reg.Contains(siteID) {
		return "", fmt.Errorf("site_id %q (source=%s) not found in registry", siteID, source)
	}

	slog.Debug("resolved site_id", "site_id", siteID, "resolution_source", source)
	return siteID, nil
}
