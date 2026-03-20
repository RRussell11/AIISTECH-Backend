package state

import "path/filepath"

const stateBase = "var/state"

// StateRoot returns the root directory for all state belonging to siteID.
func StateRoot(siteID string) string {
	return filepath.Join(stateBase, siteID)
}

// EventsDir returns the directory for event files belonging to siteID.
func EventsDir(siteID string) string {
	return filepath.Join(StateRoot(siteID), "events")
}
