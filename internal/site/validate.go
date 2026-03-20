package site

import (
	"errors"
	"strings"
)

// ErrInvalidSiteID is returned when a site_id fails validation.
var ErrInvalidSiteID = errors.New("invalid site_id")

// Validate checks that siteID satisfies the ADR constraints:
//   - non-empty
//   - must not contain ".."
//   - must not contain path separators "/" or "\"
func Validate(siteID string) error {
	if siteID == "" {
		return errors.New("site_id must not be empty")
	}
	if strings.Contains(siteID, "..") {
		return errors.New("site_id must not contain '..'")
	}
	if strings.ContainsAny(siteID, `/\`) {
		return errors.New("site_id must not contain path separators")
	}
	return nil
}
