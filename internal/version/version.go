// Package version exposes build-time version metadata for the server.
// The variables are set at link time via -ldflags when building a release
// binary; the defaults ("dev", "none", "") are used during local development
// and CI test runs.
package version

// Version is the human-readable release tag (e.g. "v1.2.3").
var Version = "dev"

// Commit is the short Git commit SHA that produced this build.
var Commit = "none"

// BuildTime is the RFC3339 timestamp at which the binary was built.
var BuildTime = ""
