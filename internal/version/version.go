// Package version carries build metadata injected at release time via
// -ldflags "-X github.com/steveokay/janus-secrets/internal/version.Version=…".
package version

import "fmt"

// These are overridden at build time by goreleaser ldflags. Non-release
// builds report the "dev" defaults.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String renders a single human line: "<version> (commit <commit>, built <date>)".
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, Date)
}
