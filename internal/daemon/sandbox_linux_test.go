package daemon

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Aman123at/agentic-os/internal/config"
	"github.com/Aman123at/agentic-os/internal/sandbox"
)

// vpsMachine is a fake native VPS: AOS's home and Protected tree, another user's
// home with the authorized_keys that must stay unwritable, AOS's own binaries and
// unit, the boot and pseudo filesystems, and an /opt an Agent should be able to
// write after the filesystem is widened (M6.8).
func vpsMachine() fstest.MapFS {
	return fstest.MapFS{
		"boot/grub/grub.cfg":                 {},
		"proc/1/status":                      {},
		"sys/kernel/hostname":                {},
		"snap/core/current":                  {},
		"root/.bashrc":                       {},
		"opt/thing/data":                     {},
		"etc/nginx.conf":                     {},
		"etc/aos/config.yml":                 {},
		"etc/systemd/system/aos.service":     {},
		"usr/bin/bash":                       {},
		"usr/local/bin/aosd":                 {},
		"usr/local/bin/aos":                  {},
		"usr/local/lib/aos/agent-bin/rm":     {},
		"var/lib/aos/aos.db":                 {},
		"run/secrets/openai":                 {},
		"run/aos/sessions/keep":              {},
		"home/aos/project/main.go":           {},
		"home/.aos-protected/ssh/id_ed25519": {},
		"home/ubuntu/.ssh/authorized_keys":   {},
		"tmp/x":                              {},
	}
}

// granted unions the access from every grant enclosing path, matching Landlock's
// additive rules: a right is allowed if any rule on an ancestor grants it. The
// widened ruleset needs this rather than the deepest-grant effective(), because
// Read is carved from `/` while Write is carved a level deeper.
func granted(rs sandbox.Ruleset, path string) sandbox.Access {
	var a sandbox.Access
	for _, g := range rs.Grants {
		if g.Path == "/" || path == g.Path || strings.HasPrefix(path, strings.TrimSuffix(g.Path, "/")+"/") {
			a |= g.Access
		}
	}
	return a
}

func vpsDaemon(fs string) *Daemon {
	return &Daemon{
		cfg:    config.Config{Filesystem: fs},
		layout: sandbox.Layout{Home: "/home/aos", Protected: "/home/.aos-protected"},
		locks:  &locks{home: "/home/aos"},
		exe:    "/usr/local/bin/aosd",
		homes:  func() []string { return []string{"/home/ubuntu"} },
	}
}

// TestHostFilesystemWidensWritableToTheVPSMinusProtected is the M6.8 acceptance at
// the ruleset level: with filesystem=host an Agent writes /opt but is refused at
// every Protected root, the Hidden config, AOS's own files and another user's
// home, while still reading all of `/`.
func TestHostFilesystemWidensWritableToTheVPSMinusProtected(t *testing.T) {
	d := vpsDaemon("host")
	rs, err := sandbox.Plan(d.agentPolicy(), vpsMachine())
	if err != nil {
		t.Fatal(err)
	}

	// The point of widening: /opt (and the rest of /, e.g. a split /etc) is writable.
	for _, p := range []string{"/opt/thing/data", "/etc/nginx.conf", "/home/aos/project/main.go"} {
		if granted(rs, p)&sandbox.Write == 0 {
			t.Errorf("%s: want writable after widening, grants %v", p, rs.Grants)
		}
	}

	// The explicit Protected list, the Hidden config, and ~/.ssh stay unwritable.
	refused := []string{
		"/boot/grub/grub.cfg",               // the boot loader
		"/root/.bashrc",                     // root's home
		"/snap/core/current",                // snap
		"/proc/1/status",                    // pseudo filesystems
		"/sys/kernel/hostname",              //
		"/usr/local/bin/aosd",               // AOS's own binaries
		"/usr/local/bin/aos",                //
		"/usr/local/lib/aos/agent-bin/rm",   //
		"/etc/systemd/system/aos.service",   // AOS's unit
		"/etc/aos/config.yml",               // Hidden
		"/home/ubuntu/.ssh/authorized_keys", // another user's home
		"/home/.aos-protected/ssh/id_ed25519",
	}
	for _, p := range refused {
		if a := granted(rs, p); a&sandbox.Write != 0 {
			t.Errorf("%s: want no write, got %v", p, a)
		}
	}

	// Reading is unchanged — all of `/` stays readable except the Hidden trees.
	for _, p := range []string{"/boot/grub/grub.cfg", "/usr/local/bin/aosd", "/root/.bashrc"} {
		if granted(rs, p)&sandbox.Read == 0 {
			t.Errorf("%s: want readable, grants %v", p, rs.Grants)
		}
	}
	for _, p := range []string{"/etc/aos/config.yml", "/var/lib/aos/aos.db", "/run/secrets/openai"} {
		if granted(rs, p)&sandbox.Read != 0 {
			t.Errorf("%s: want hidden, got %v", p, granted(rs, p))
		}
	}

	// The hard rule that bounds the walk: no exclusion may live under /proc or /sys,
	// or carve would descend into a pseudo-filesystem.
	p := d.agentPolicy()
	for _, x := range append(append([]string{}, p.Protected...), p.Hidden...) {
		if strings.HasPrefix(x, "/proc/") || strings.HasPrefix(x, "/sys/") {
			t.Errorf("exclusion %q lives under /proc or /sys", x)
		}
	}
}

// TestHomeFilesystemKeepsAgentsInHome is the Compose default (unchanged, M6.8): an
// Agent writes home but nothing outside it.
func TestHomeFilesystemKeepsAgentsInHome(t *testing.T) {
	d := vpsDaemon("home")
	rs, err := sandbox.Plan(d.agentPolicy(), vpsMachine())
	if err != nil {
		t.Fatal(err)
	}
	if granted(rs, "/home/aos/project/main.go")&sandbox.Write == 0 {
		t.Error("home should stay writable")
	}
	if granted(rs, "/opt/thing/data")&sandbox.Write != 0 {
		t.Error("/opt should not be writable without widening")
	}
}
