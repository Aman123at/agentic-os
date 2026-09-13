package sandbox

import (
	"fmt"

	"github.com/landlock-lsm/go-landlock/landlock"
	ll "github.com/landlock-lsm/go-landlock/landlock/syscall"
	"golang.org/x/sys/unix"
)

// ABI returns the kernel's Landlock ABI version, or 0 when Landlock is unavailable.
func ABI() int {
	v, err := ll.LandlockGetABIVersion()
	if err != nil {
		return 0
	}
	return v
}

// config handles every filesystem right and IPC scope go-landlock knows, but no
// network rights: Agents need the internet. BestEffort drops what the kernel lacks.
var config = landlock.Config{
	HandledAccessFS: landlock.V10.HandledAccessFS,
	Scoped:          landlock.V10.Scoped,
}.BestEffort()

func rights(a Access) landlock.AccessFSSet {
	var r landlock.AccessFSSet
	if a&List != 0 {
		r |= ll.AccessFSReadDir
	}
	if a&Read != 0 {
		r |= ll.AccessFSReadFile | ll.AccessFSReadDir | ll.AccessFSExecute | ll.AccessFSResolveUnix
	}
	if a&Create != 0 {
		r |= ll.AccessFSMakeDir | ll.AccessFSMakeReg
	}
	if a&Write != 0 {
		r |= config.HandledAccessFS
	}
	return r
}

// Enforce sets no_new_privs and restricts the calling process, and everything it
// later executes, to rs. It cannot be undone.
func Enforce(rs Ruleset) error {
	// Set explicitly so setuid binaries (sudo) are neutralised even without Landlock.
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("prctl(PR_SET_NO_NEW_PRIVS): %w", err)
	}
	rules := make([]landlock.Rule, 0, len(rs.Grants))
	for _, g := range rs.Grants {
		rules = append(rules, landlock.PathAccess(rights(g.Access), g.Path).IgnoreIfMissing())
	}
	if err := config.Restrict(rules...); err != nil {
		return fmt.Errorf("landlock: %w", err)
	}
	return nil
}
