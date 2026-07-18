// Package version exposes build-time metadata injected via ldflags.
package version

import "fmt"

// These are overridden at build time via:
// -ldflags "-X github.com/oziie/ocpgate/pkg/version.Version=1.0.0 ..."
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

// String returns a human-readable version summary.
func String() string {
	return fmt.Sprintf("ocpgate %s (commit %s, built %s)", Version, Commit, BuildDate)
}
