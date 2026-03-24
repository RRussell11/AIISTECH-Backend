// Package version exposes build-time metadata injected via -ldflags.
//
// Example build command:
//
//	go build -ldflags "-X github.com/RRussell11/AIISTECH-Backend/internal/version.Version=v1.2.3 \
//	  -X github.com/RRussell11/AIISTECH-Backend/internal/version.Commit=abc1234 \
//	  -X github.com/RRussell11/AIISTECH-Backend/internal/version.BuildTime=2024-01-01T00:00:00Z" \
//	  ./cmd/server
package version

// These variables are set at build time via -ldflags.
// They default to empty strings when built without the flags.
var (
	Version   = ""
	Commit    = ""
	BuildTime = ""
)
