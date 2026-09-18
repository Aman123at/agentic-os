package sandbox

import (
	"io/fs"
	"os"
)

// Layout names the Machine's folders that the Agent ruleset is built around
// (PLAN.md §7.2, ADR-0004).
type Layout struct {
	Home      string // the home folder: root:aos 1775, fully writable for Agents
	Protected string // where Protected dotfiles live, behind root-owned symlinks in Home
}

// DefaultLayout is the Machine image's layout.
func DefaultLayout() Layout {
	return Layout{Home: "/home/aos", Protected: "/home/.aos-protected"}
}

// ProtectedEntry is a dotfile in Home that is a symlink to Target in Layout.Protected.
type ProtectedEntry struct {
	Link   string // name in Home, e.g. ".ssh"
	Target string // name in Layout.Protected, e.g. "ssh"
	// Dir and Seed describe how a missing Target is created: a folder with Mode, a
	// copy of Seed, or nothing (the symlink only stops Agents planting the file).
	Dir  bool
	Mode fs.FileMode
	Seed string
}

// ProtectedEntries are the default Protected dotfiles (PLAN.md §7.3).
var ProtectedEntries = []ProtectedEntry{
	{Link: ".ssh", Target: "ssh", Dir: true, Mode: 0o700},
	{Link: ".gnupg", Target: "gnupg", Dir: true, Mode: 0o700},
	{Link: ".config", Target: "config", Dir: true, Mode: 0o755},
	{Link: ".bashrc", Target: "bashrc", Mode: 0o644, Seed: "/etc/skel/.bashrc"},
	{Link: ".profile", Target: "profile", Mode: 0o644, Seed: "/etc/skel/.profile"},
	{Link: ".bash_logout", Target: "bash_logout", Mode: 0o644, Seed: "/etc/skel/.bash_logout"},
	{Link: ".bash_profile", Target: "bash_profile"},
	{Link: ".bash_login", Target: "bash_login"},
	{Link: ".inputrc", Target: "inputrc"},
}

// Policy is the Agent Session policy for this layout.
//
// Nothing in Home needs a Protected entry: the Protected dotfiles are outside it,
// and so are system folders, which are read-only because nothing grants write
// there. Protected is listed anyway so that a layout placing it in a Writable
// tree still protects it. Paths the user locks are appended by the caller. "Any
// .env file" and dirty git working trees are enforced by policy only.
func (l Layout) Policy() Policy {
	return Policy{
		Hidden:    []string{"/run/secrets", "/var/lib/aos"},
		Protected: []string{l.Protected},
		// /dev holds /dev/null, /dev/tty and the Session's PTY.
		Writable: []string{l.Home, "/tmp", "/var/tmp", "/dev"},
	}
}

// RootFS is the Machine's filesystem rooted at "/", for Plan and Ruleset.Stale.
func RootFS() FS {
	return os.DirFS("/").(FS)
}
