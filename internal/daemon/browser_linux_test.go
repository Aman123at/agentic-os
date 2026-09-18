package daemon

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Aman123at/agentic-os/internal/sandbox"
)

// browserMachine is a small Machine filesystem with the Browser's profile and
// Downloads folders already present, as ensureBrowserDirs leaves them before
// planning.
func browserMachine() fstest.MapFS {
	return fstest.MapFS{
		"usr/bin/bash":       {},
		"etc/nginx.conf":     {},
		"run/secrets/openai": {},
		"var/lib/aos/aos.db": {},
		"home/aos/.local/share/aos-browser/Default/Preferences": {},
		"home/aos/Downloads/.keep":                              {},
		"home/aos/Documents/report.txt":                         {},
		"home/.aos-protected/ssh/id_ed25519":                    {},
		"tmp/x":                                                 {},
	}
}

// effective returns the access Landlock would grant at path: the grant on the
// deepest directory that encloses it, since a rule applies to its whole subtree.
func effective(rs sandbox.Ruleset, path string) sandbox.Access {
	var (
		best     sandbox.Access
		bestPath string
	)
	for _, g := range rs.Grants {
		encloses := g.Path == path || path == "/" && g.Path == "/" || strings.HasPrefix(path, strings.TrimSuffix(g.Path, "/")+"/")
		if encloses && len(g.Path) >= len(bestPath) {
			best, bestPath = g.Access, g.Path
		}
	}
	return best
}

// TestBrowserPolicyWritesOnlyProfileAndDownloads is the M6.7 acceptance in a
// unit: the Browser's own ruleset (ADR-0008) lets Chromium write its profile and
// ~/Downloads but refuses a write to /etc or to home outside those two, so M6.8's
// widening of the Agent policy never reaches an unsandboxed browser.
func TestBrowserPolicyWritesOnlyProfileAndDownloads(t *testing.T) {
	d := &Daemon{
		layout: sandbox.Layout{Home: "/home/aos", Protected: "/home/.aos-protected"},
		locks:  &locks{home: "/home/aos"},
	}
	rs, err := sandbox.Plan(d.browserPolicy(), browserMachine())
	if err != nil {
		t.Fatal(err)
	}

	writable := []string{
		"/home/aos/.local/share/aos-browser/Default/Preferences",
		"/home/aos/Downloads/report.pdf",
	}
	for _, p := range writable {
		if effective(rs, p)&sandbox.Write == 0 {
			t.Errorf("%s: want writable, grants %v", p, rs.Grants)
		}
	}

	// The whole point of the narrow ruleset: no write beyond the two folders.
	refused := []string{
		"/etc/nginx.conf",                 // the server's config
		"/home/aos/Documents/report.txt",  // home, outside Downloads
		"/home/aos/.local/share/other.db", // the profile's siblings
		"/usr/bin/bash",                   // AOS's own tools live under here
	}
	for _, p := range refused {
		if a := effective(rs, p); a&sandbox.Write != 0 {
			t.Errorf("%s: want no write, got %v", p, a)
		}
	}

	// It still reads the Machine like an Agent, or Chromium can't load its libraries.
	for _, p := range []string{"/etc/nginx.conf", "/usr/bin/bash"} {
		if effective(rs, p)&sandbox.Read == 0 {
			t.Errorf("%s: want readable, grants %v", p, rs.Grants)
		}
	}
}
