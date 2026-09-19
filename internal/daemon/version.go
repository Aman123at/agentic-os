// Version, kept here (no build constraint) so `aos --version` reads it on any
// platform and the release stage can stamp it into the Linux binary (M6.18).
package daemon

// Version is what the Desktop shows in Settings ▸ Status and About This
// Machine, and what `aos --version` prints.
//
// It is a var, not a const, on purpose: the release stage stamps a tagged
// build with `go build -ldflags "-X …/internal/daemon.Version=<tag>"`, and the
// linker silently cannot write a const (M6.18). A development build keeps the
// milestone-suffixed default below; a tagged release overwrites it with the
// clean tag (v0.1.0 → 0.1.0). version_linux_test.go ties the -m<n> suffix to the
// plan's newest milestone and keeps the base a clean semver, so the two schemes
// stay reconciled.
var Version = "0.1.0-m5"
