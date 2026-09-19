// Package realm names the two ways the Machine can run (PLAN.md M7). The
// Standard Realm is the ordinary one: Agents, the Terminal and Finder act as the
// unprivileged `aos` user, confined by Landlock, and Protected Paths are
// enforced. The Root Realm — Root Mode — runs the whole Machine as root, with
// every file unlocked and `sudo` available.
//
// Each Realm keeps its own history (Tasks, Audit Log, Memory, Notifications,
// Services, window layout, Browser profile) in its own database file, so nothing
// done in one Realm is visible from the other. The Machine runs one Realm at a
// time, chosen at start from `root_mode:` in config.yml; every switch restarts
// aosd into the other Realm (M7.3, M7.7).
//
// The Daemon resolves the Realm once at start and hands this value to everything
// that needs it, so no other package reads the `root_mode` key itself.
package realm

// Realm is which of the two ways the Machine is running.
type Realm int

const (
	// Standard is the ordinary Realm: unprivileged, Landlock-confined.
	Standard Realm = iota
	// Root is Root Mode: the Machine runs as root, everything unlocked.
	Root
)

// Of returns the Realm the `root_mode` key selects.
func Of(rootMode bool) Realm {
	if rootMode {
		return Root
	}
	return Standard
}

// IsRoot reports whether this is Root Mode.
func (r Realm) IsRoot() bool { return r == Root }

// String is the wire and CLI name: "standard" or "root".
func (r Realm) String() string {
	if r == Root {
		return "root"
	}
	return "standard"
}
