package daemon

import (
	"slices"
	"strings"
	"testing"

	"github.com/Aman123at/agentic-os/internal/config"
	"github.com/Aman123at/agentic-os/internal/realm"
	"github.com/Aman123at/agentic-os/internal/sandbox"
	"github.com/Aman123at/agentic-os/internal/service"
)

// rootDaemon is a Daemon running the Root Realm, with the agent identity resolved
// to root as init() does (M7.4).
func rootDaemon() *Daemon {
	d := vpsDaemon("host")
	d.realm = realm.Root
	d.agentUID, d.agentGID, d.agentHome, d.agentUser = 0, 0, "/root", "root"
	return d
}

// TestRootPolicyReadsAndWritesEverythingButAOSState is the M7.4 acceptance at the
// ruleset level (asserted against a fake VPS like M6.8): a root Agent writes
// /etc, /root, another user's home and /opt, but AOS's own state stays Hidden and
// its binary and unit read-only. It runs regardless of the install shape, since
// Root Mode replaces the Standard policy entirely.
func TestRootPolicyReadsAndWritesEverythingButAOSState(t *testing.T) {
	d := rootDaemon()
	rs, err := sandbox.Plan(d.agentPolicy(), vpsMachine())
	if err != nil {
		t.Fatal(err)
	}

	// Everything is writable — root is unlocked — including paths the Standard
	// Realm keeps Protected: /etc, /root, another user's home.
	for _, p := range []string{
		"/opt/thing/data",
		"/etc/nginx.conf",
		"/root/.bashrc",
		"/home/ubuntu/.ssh/authorized_keys",
		"/home/aos/project/main.go",
	} {
		if granted(rs, p)&sandbox.Write == 0 {
			t.Errorf("%s: want writable in Root Mode, grants %v", p, rs.Grants)
		}
	}

	// AOS's own binary and unit are read-only, so a root Agent cannot rewrite the
	// Daemon out from under itself.
	for _, p := range []string{"/usr/local/bin/aosd", "/usr/local/bin/aos", "/usr/local/lib/aos/agent-bin/rm", "/etc/systemd/system/aos.service"} {
		if a := granted(rs, p); a&sandbox.Write != 0 {
			t.Errorf("%s: want read-only in Root Mode, got %v", p, a)
		}
		if granted(rs, p)&sandbox.Read == 0 {
			t.Errorf("%s: want readable in Root Mode, grants %v", p, rs.Grants)
		}
	}

	// AOS's state stays Hidden — neither the Realm databases and keys under
	// /var/lib/aos, nor the config and secret stores, are readable.
	for _, p := range []string{"/var/lib/aos/aos.db", "/etc/aos/config.yml", "/run/secrets/openai", "/run/aos/sessions/keep"} {
		if a := granted(rs, p); a&sandbox.Read != 0 {
			t.Errorf("%s: want Hidden in Root Mode, got %v", p, a)
		}
	}
}

// TestBrowserProfileIsPerRealm guards the M7.5 rule that the Browser gets its own
// profile directory per Realm, so cookies and logins never leak between them,
// while both stay under aos's home (the Browser runs as aos in either Realm).
func TestBrowserProfileIsPerRealm(t *testing.T) {
	std := &Daemon{layout: sandbox.DefaultLayout(), realm: realm.Standard}
	root := &Daemon{layout: sandbox.DefaultLayout(), realm: realm.Root}
	if std.browserProfile() == root.browserProfile() {
		t.Fatalf("both Realms share the Browser profile %q", std.browserProfile())
	}
	for _, p := range []string{std.browserProfile(), root.browserProfile()} {
		if !strings.HasPrefix(p, "/home/aos/") {
			t.Errorf("Browser profile %q is not under aos's home", p)
		}
	}
}

// TestLaunchServiceRunsAsRootInRootMode is the M7.5 acceptance for Services: a
// Service that was created as an ordinary (non-Privileged) one still runs as root
// in Root Mode, with root's home, because the whole Machine runs as root.
func TestLaunchServiceRunsAsRootInRootMode(t *testing.T) {
	d := rootDaemon()
	d.cfg = config.Config{Filesystem: "host"} // no Landlock in the test → an empty ruleset
	cmd, err := d.launchService(service.Definition{Name: "site", Command: "sleep 1"})
	if err != nil {
		t.Fatal(err)
	}
	if cred := cmd.SysProcAttr.Credential; cred == nil || cred.Uid != 0 || cred.Gid != 0 {
		t.Errorf("Service credential = %+v, want uid/gid 0", cmd.SysProcAttr.Credential)
	}
	if !slices.Contains(cmd.Env, "HOME=/root") || !slices.Contains(cmd.Env, "USER=root") {
		t.Errorf("Service env = %v, want HOME=/root and USER=root", cmd.Env)
	}
}
