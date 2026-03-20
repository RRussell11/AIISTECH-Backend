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

// ArtifactsDir returns the directory for artifact files belonging to siteID.
func ArtifactsDir(siteID string) string {
	return filepath.Join(StateRoot(siteID), "artifacts")
}

// AuditDir returns the directory for audit log entries belonging to siteID.
func AuditDir(siteID string) string {
	return filepath.Join(StateRoot(siteID), "audit")
}

// DBPath returns the path to the bbolt database file for siteID.
func DBPath(siteID string) string {
	return filepath.Join(StateRoot(siteID), "data.db")
}
