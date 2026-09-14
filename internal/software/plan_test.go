package software

import (
	"strings"
	"testing"
)

func TestPlanningAptInstallsRemovesAndKeepsNewerVersionsOnReplay(t *testing.T) {
	targets := map[string]string{
		"nginx:arm64":      pkg("1.24.0"),
		"nginx-common:all": `{"version":"1.24.0","auto":true}`,
		"libc6:arm64":      pkg("2.39-1"),
		"curl:arm64":       pkg("8.5.0"),
		"oldtool:arm64":    "",
		"gone:arm64":       "",
	}
	installed := Packages{"libc6:arm64": {Version: "2.39-2"}, "curl:arm64": {Version: "8.5.0"}, "oldtool:arm64": {Version: "1.0"}}
	newer := func(a, b string) bool { return a > b }

	replay := planApt(targets, installed, newer)
	if strings.Join(replay.remove, " ") != "oldtool:arm64" {
		t.Errorf("remove %v", replay.remove)
	}
	if len(replay.install) != 2 || replay.install["nginx:arm64"] != "1.24.0" || replay.install["nginx-common:all"] != "1.24.0" {
		t.Errorf("install %v", replay.install)
	}
	if len(replay.notes) != 1 || !strings.Contains(replay.notes[0], "kept libc6:arm64 2.39-2") {
		t.Errorf("notes %v", replay.notes)
	}
	if !replay.auto["nginx-common:all"] || replay.auto["nginx:arm64"] {
		t.Errorf("auto %v", replay.auto)
	}
	// A Restore goes back to the recorded version, even an older one.
	restore := planApt(targets, installed, nil)
	if restore.install["libc6:arm64"] != "2.39-1" {
		t.Errorf("restore install %v", restore.install)
	}
}

func TestReplayWritesOnlyFilesTheImageLeftAlone(t *testing.T) {
	f := func(blob string) string { return `{"type":"file","blob":"` + blob + `","mode":420,"uid":0,"gid":0}` }
	now := Snapshot{
		"/etc/a.conf": {Type: "file", Blob: "a0", Mode: 420},
		"/etc/b.conf": {Type: "file", Blob: "b-new-image", Mode: 420},
		"/etc/c.conf": {Type: "file", Blob: "c1", Mode: 420},
	}
	final := map[string]string{"/etc/a.conf": f("a1"), "/etc/b.conf": f("b1"), "/etc/c.conf": f("c1"), "/etc/new.conf": f("n1"), "/etc/gone.conf": ""}
	first := map[string]string{"/etc/a.conf": f("a0"), "/etc/b.conf": f("b0"), "/etc/c.conf": f("c0"), "/etc/new.conf": "", "/etc/gone.conf": f("g0")}
	write, notes := replayFiles(final, first, now)
	if len(write) != 2 || write["/etc/a.conf"] != f("a1") || write["/etc/new.conf"] != f("n1") {
		t.Errorf("write %v", write)
	}
	// gone.conf is already absent; c.conf is already right; b.conf changed in the new image.
	if len(notes) != 1 || !strings.Contains(notes[0], "/etc/b.conf") {
		t.Errorf("notes %v", notes)
	}
}

func TestPackageNamesAreCheckedBeforeTheyReachACommandLine(t *testing.T) {
	good := map[string][]string{
		"apt":  {"nginx", "libc6:arm64", "nginx=1.24.0-2ubuntu7", "g++", "python3.12"},
		"pipx": {"httpie", "black==24.1.0", "poetry[plugin]"},
		"npm":  {"cowsay", "@angular/cli", "typescript@5.4.2"},
	}
	for mgr, names := range good {
		if err := validPackages(mgr, names); err != nil {
			t.Errorf("%s %v: %v", mgr, names, err)
		}
	}
	for mgr, name := range map[string]string{"apt": "-o=APT::Foo", "pipx": "--index-url=http://evil", "npm": "x; rm -rf ~"} {
		if err := validPackages(mgr, []string{name}); err == nil {
			t.Errorf("%s accepted %q", mgr, name)
		}
	}
	if err := validPackages("brew", []string{"x"}); err == nil {
		t.Error("an unknown package manager was accepted")
	}
}

func TestOutcomesDescribeTopLevelPackagesAndCountTheRest(t *testing.T) {
	o := Outcome{Op: &Op{Changes: []Change{
		{Kind: KindPackage, Manager: "apt", Name: "nginx:arm64", After: pkg("1.24.0")},
		{Kind: KindPackage, Manager: "apt", Name: "nginx-common:all", After: `{"version":"1.24.0","auto":true}`},
		{Kind: KindPackage, Manager: "apt", Name: "vim-tiny:arm64", Before: pkg("9.1")},
		{Kind: KindFile, Name: "/etc/nginx"},
		{Kind: KindFile, Name: "/etc/nginx/nginx.conf"},
	}}}
	got := strings.Join(o.Describe(), "|")
	if got != "installed nginx:arm64 1.24.0 (apt)|removed vim-tiny:arm64 9.1 (apt)|1 dependencies changed|2 paths under /etc changed" {
		t.Errorf("describe %q", got)
	}
}
