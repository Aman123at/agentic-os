package sandbox

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

// machine is a small Machine filesystem shaped like the real image.
func machine() fstest.MapFS {
	return fstest.MapFS{
		"usr/bin/bash":             {},
		"etc/hosts":                {},
		"run/secrets/openai":       {},
		"run/aos/aosd.sock":        {},
		"var/lib/aos/aos.db":       {},
		"var/lib/dpkg/status":      {},
		"var/log/apt.log":          {},
		"home/aos/.ssh/id_ed25519": {},
		"home/aos/.bashrc":         {},
		"home/aos/Documents/a.txt": {},
		"home/aos/Downloads/b.zip": {},
		"tmp/x":                    {},
	}
}

func defaultPolicy() Policy {
	return Policy{
		Hidden:    []string{"/run/secrets", "/var/lib/aos"},
		Protected: []string{"/home/aos/.ssh", "/home/aos/.bashrc"},
		Writable:  []string{"/home/aos", "/tmp"},
	}
}

func accessOf(t *testing.T, rs Ruleset, path string) Access {
	t.Helper()
	var a Access
	for _, g := range rs.Grants {
		if g.Path == path {
			a |= g.Access
		}
	}
	return a
}

func TestPlanHiddenPathsAreNotReadable(t *testing.T) {
	rs, err := Plan(defaultPolicy(), machine())
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"/usr", "/etc", "/run/aos", "/var/lib/dpkg", "/var/log"} {
		if accessOf(t, rs, p)&Read != Read {
			t.Errorf("%s: want readable, grants %v", p, rs.Grants)
		}
	}
	for _, p := range []string{"/", "/run", "/var", "/var/lib"} {
		if got := accessOf(t, rs, p); got != List {
			t.Errorf("%s: want list-only, got %v", p, got)
		}
	}
	for _, p := range []string{"/run/secrets", "/var/lib/aos"} {
		if got := accessOf(t, rs, p); got != 0 {
			t.Errorf("%s: hidden path must have no grant, got %v", p, got)
		}
	}
}

func TestRulesetIsStaleWhenASplitDirectoryGainsAnEntry(t *testing.T) {
	fsys := machine()
	rs, err := Plan(defaultPolicy(), fsys)
	if err != nil {
		t.Fatal(err)
	}

	fsys["home/aos/Documents/c.txt"] = &fstest.MapFile{}
	fsys["tmp/y"] = &fstest.MapFile{}
	if stale, err := rs.Stale(fsys); err != nil || stale {
		t.Fatalf("changes inside fully granted folders: stale=%v err=%v, want not stale", stale, err)
	}

	fsys["home/aos/Projects"] = &fstest.MapFile{Mode: fs.ModeDir}
	if stale, err := rs.Stale(fsys); err != nil || !stale {
		t.Fatalf("new top-level folder in home: stale=%v err=%v, want stale", stale, err)
	}
}

func TestPlanSkipsMissingWritableRoots(t *testing.T) {
	p := defaultPolicy()
	p.Writable = append(p.Writable, "/var/tmp")

	rs, err := Plan(p, machine())
	if err != nil {
		t.Fatal(err)
	}
	if got := accessOf(t, rs, "/var/tmp"); got != 0 {
		t.Errorf("/var/tmp does not exist: want no grant, got %v", got)
	}
}

func TestPlanNeverGrantsThroughSymlinks(t *testing.T) {
	fsys := machine()
	// Landlock resolves symlinks when a rule is added, so a grant on this link
	// would make the Protected ~/.ssh writable.
	fsys["home/aos/keys"] = &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte(".ssh")}

	rs, err := Plan(defaultPolicy(), fsys)
	if err != nil {
		t.Fatal(err)
	}
	if got := accessOf(t, rs, "/home/aos/keys"); got != 0 {
		t.Errorf("symlink must have no grant, got %v", got)
	}
}

func TestPlanHomeIsWritableExceptProtectedPaths(t *testing.T) {
	rs, err := Plan(defaultPolicy(), machine())
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"/home/aos/Documents", "/home/aos/Downloads", "/tmp"} {
		if got := accessOf(t, rs, p); got&Write == 0 {
			t.Errorf("%s: want writable, got %v", p, got)
		}
	}
	for _, p := range []string{"/home/aos/.ssh", "/home/aos/.bashrc"} {
		if got := accessOf(t, rs, p); got&(Write|Create) != 0 {
			t.Errorf("%s: protected path must not be writable, got %v", p, got)
		}
	}
	// New top-level folders can be created in home; they become writable once re-planned.
	if got := accessOf(t, rs, "/home/aos"); got != Create {
		t.Errorf("/home/aos: want create-only, got %v", got)
	}
}

func TestPlanCleansPolicyPaths(t *testing.T) {
	p := Policy{
		Hidden:    []string{"/run/secrets/"},
		Protected: []string{"/home/aos/.ssh/", "/home/aos//.bashrc"},
		Writable:  []string{"/home/aos/"},
	}

	rs, err := Plan(p, machine())
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]Access{
		"/home/aos":           Create,
		"/home/aos/Documents": Write,
		"/home/aos/.ssh":      0,
		"/home/aos/.bashrc":   0,
		"/run":                List,
		"/run/secrets":        0,
	} {
		if got := accessOf(t, rs, path) &^ Read; got != want {
			t.Errorf("%s: want %v, got %v (grants %v)", path, want, got, rs.Grants)
		}
	}
}

func TestPlanProtectsTheTargetOfASymlinkedProtectedPath(t *testing.T) {
	fsys := machine()
	delete(fsys, "home/aos/.bashrc")
	fsys["home/aos/dotfiles/bashrc"] = &fstest.MapFile{}
	fsys["home/aos/dotfiles/vimrc"] = &fstest.MapFile{}
	fsys["home/aos/.bashrc"] = &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte("dotfiles/bashrc")}

	rs, err := Plan(defaultPolicy(), fsys)
	if err != nil {
		t.Fatal(err)
	}
	if got := accessOf(t, rs, "/home/aos/dotfiles"); got&Write != 0 {
		t.Errorf("/home/aos/dotfiles holds the target of Protected ~/.bashrc: want no write, got %v", got)
	}
	if got := accessOf(t, rs, "/home/aos/dotfiles/vimrc"); got&Write == 0 {
		t.Errorf("/home/aos/dotfiles/vimrc: want writable, got %v", got)
	}
}
