package sandbox

import (
	"os"
	"path/filepath"
)

// DefaultPolicy is the Agent Session policy from PLAN.md §7.2–7.3 for a Machine
// whose home folder is home and whose Shared Folder is shared.
//
// Protected Paths outside the Writable trees (/etc, /usr, …) need no entry: they
// are read-only because nothing grants write there. "Any .env file" and "Git
// working trees with uncommitted changes" cannot be expressed as Landlock rules
// and are enforced by policy checks on Tool calls only.
func DefaultPolicy(home, shared string) Policy {
	return Policy{
		Hidden: []string{"/run/secrets", "/var/lib/aos"},
		Protected: []string{
			filepath.Join(home, ".ssh"),
			filepath.Join(home, ".gnupg"),
			filepath.Join(home, ".config"),
			filepath.Join(home, ".bashrc"),
			filepath.Join(home, ".profile"),
			shared,
		},
		// /dev holds /dev/null, /dev/tty and the Session's PTY.
		Writable: []string{home, "/tmp", "/var/tmp", "/dev"},
	}
}

// RootFS is the Machine's filesystem rooted at "/", for Plan and Ruleset.Stale.
func RootFS() FS {
	return os.DirFS("/").(FS)
}
