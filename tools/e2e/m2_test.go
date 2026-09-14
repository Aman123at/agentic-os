package e2e

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestM2Acceptance(t *testing.T) {
	if os.Getenv("AOS_E2E") == "" {
		t.Skip("set AOS_E2E=1 to run against Docker: go run ./tools/ci e2e")
	}
	m := startMachine(t, "aos-e2e-m2", "7795")
	var checkpoint string
	t.Run("InstallNginxAndServeASite", func(t *testing.T) { checkpoint = m.installNginx(t) })
	t.Run("ReplayReinstallsOfflineAfterDownAndUp", m.replayOffline)
	t.Run("RestoreRemovesNginxAndItsConfig", func(t *testing.T) { m.restore(t, checkpoint) })
	t.Run("KilledAosdLeavesTheTaskInterruptedAndResumable", m.interruptAndResume)
	t.Run("AFailingStepPausesAfterThreeRetries", m.retries)
}

const siteHTML = "<h1>Hello from the e2e site</h1>"

// "install nginx and serve ~/site on port 8081" works end to end.
func (m *machine) installNginx(t *testing.T) string {
	out, code := m.aos(t, "run", "--autonomy", "auto", "e2e-nginx: install nginx and serve ~/site on port 8081")
	if code != 0 {
		t.Fatalf("aos run: exit %d\n%s", code, out)
	}
	id := taskID(t, out)
	if got := m.sh(t, "root", "dpkg-query -W -f '${Status}' nginx"); got != "install ok installed" {
		t.Errorf("nginx is %q", got)
	}
	m.wantSite(t)
	if list, _ := m.aos(t, "software", "list"); !regexp.MustCompile(`(?m)^apt\s+nginx:\S+\s+\S+`).MatchString(list) {
		t.Errorf("aos software list:\n%s", list)
	}
	if list, _ := m.aos(t, "service", "list"); !regexp.MustCompile(`(?m)^site\s+running\s+\d+\s+8081\s`).MatchString(list) {
		t.Errorf("aos service list:\n%s", list)
	}
	ledger, _ := m.aos(t, "software", "ledger")
	for _, want := range []string{"Install nginx (apt)", "Create Service site", "file /etc/nginx"} {
		if !strings.Contains(ledger, want) {
			t.Errorf("the Ledger lacks %q:\n%s", want, ledger)
		}
	}
	m.wantAudit(t, id, row{"install_package", "allow", "autonomy"}, row{"manage_service", "allow", "autonomy"})
	show, _ := m.aos(t, "show", id)
	c := regexp.MustCompile(`Checkpoint before its software changes: (c_[0-9a-f]+)`).FindStringSubmatch(show)
	if c == nil {
		t.Fatalf("aos show %s names no Checkpoint:\n%s", id, show)
	}
	return c[1]
}

// wantSite waits until the site answers through aosd's forwarding, as a browser
// on the Host would reach it: http://8081.localhost:<port>/.
func (m *machine) wantSite(t *testing.T) {
	t.Helper()
	last := ""
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+m.port+"/", nil)
		req.Host = "8081.localhost:" + m.port
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			last = err.Error()
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK && strings.Contains(string(body), siteHTML) {
			return
		}
		last = resp.Status + ": " + string(body)
	}
	t.Fatalf("the site never answered at 8081.localhost:%s; last response: %s", m.port, last)
}

// After `docker compose down && up`, nginx is reinstalled offline and the
// Service runs again.
func (m *machine) replayOffline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if out, err := m.compose(ctx, "down").CombinedOutput(); err != nil {
		t.Fatalf("docker compose down: %v\n%s", err, out)
	}
	// The new container can't reach Ubuntu's package archives.
	offline := filepath.Join(m.dir, "compose.offline.yaml")
	hosts := "services:\n  aos:\n    extra_hosts:\n      - \"archive.ubuntu.com:127.0.0.1\"\n      - \"security.ubuntu.com:127.0.0.1\"\n      - \"ports.ubuntu.com:127.0.0.1\"\n"
	if err := os.WriteFile(offline, []byte(hosts), 0o644); err != nil {
		t.Fatal(err)
	}
	m.files = append(m.files, offline)
	if out, err := m.compose(ctx, "up", "-d").CombinedOutput(); err != nil {
		t.Fatalf("docker compose up: %v\n%s", err, out)
	}
	eventually(t, time.Minute, "aosd answers", func() bool {
		_, code := m.aos(t, "tasks")
		return code == 0
	})
	if _, code := m.run(t, "root", "curl", "-sS", "-m", "5", "-o", "/dev/null", "http://ports.ubuntu.com/"); code == 0 {
		t.Fatal("the package archive is reachable; the Replay check would prove nothing")
	}
	var doctor string
	eventually(t, 3*time.Minute, "Replay finishes", func() bool {
		doctor, _ = m.aos(t, "doctor")
		return strings.Contains(doctor, "Replay:") && !strings.Contains(doctor, "Replay:    in progress")
	})
	r := regexp.MustCompile(`Reinstalled (\d+) packages \((\d+) from the cache\)`).FindStringSubmatch(doctor)
	if r == nil || r[1] == "0" || r[1] != r[2] {
		t.Fatalf("Replay did not reinstall everything from the cache:\n%s", doctor)
	}
	if got := m.sh(t, "root", "dpkg-query -W -f '${Status}' nginx"); got != "install ok installed" {
		t.Errorf("after Replay, nginx is %q", got)
	}
	m.wantSite(t)
	if list, _ := m.aos(t, "service", "list"); !strings.Contains(list, "running") {
		t.Errorf("aos service list:\n%s", list)
	}
}

// Restoring to the pre-Task Checkpoint removes nginx and its config.
func (m *machine) restore(t *testing.T, checkpoint string) {
	if checkpoint == "" {
		t.Skip("no Checkpoint from the install")
	}
	out, code := m.aos(t, "checkpoint", "restore", checkpoint)
	if code != 0 {
		t.Fatalf("aos checkpoint restore: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "To undo this Restore: aos checkpoint restore c_") {
		t.Errorf("the Restore offers no way back:\n%s", out)
	}
	if got, _ := m.run(t, "root", "dpkg-query", "-W", "-f", "${Status}", "nginx"); strings.Contains(got, "install ok installed") {
		t.Errorf("nginx is still installed: %q", got)
	}
	for _, path := range []string{"/etc/nginx", "/usr/sbin/nginx"} {
		if _, code := m.run(t, "root", "test", "-e", path); code == 0 {
			t.Errorf("%s still exists", path)
		}
	}
	if list, _ := m.aos(t, "service", "list"); strings.Contains(list, "site") {
		t.Errorf("the Service survived the Restore:\n%s", list)
	}
	if list, _ := m.aos(t, "software", "list"); strings.Contains(list, "nginx") {
		t.Errorf("aos software list:\n%s", list)
	}
}

// Killing aosd mid-Task leaves the Task Interrupted and Resumable.
func (m *machine) interruptAndResume(t *testing.T) {
	var out syncBuffer
	cmd := m.compose(context.Background(), "exec", "-T", "aos", "aos", "run", "e2e-interrupt: wait for the restart")
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	m.waitForSleep(t, true)
	id := taskID(t, out.String())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if o, err := m.compose(ctx, "kill", "aos").CombinedOutput(); err != nil {
		t.Fatalf("docker compose kill: %v\n%s", err, o)
	}
	_ = cmd.Wait()
	if o, err := m.compose(ctx, "up", "-d").CombinedOutput(); err != nil {
		t.Fatalf("docker compose up: %v\n%s", err, o)
	}
	eventually(t, time.Minute, "aosd answers", func() bool {
		_, code := m.aos(t, "tasks")
		return code == 0
	})
	if show, _ := m.aos(t, "show", id); !strings.Contains(show, "interrupted") {
		t.Fatalf("aos show %s:\n%s", id, show)
	}
	resumed, code := m.aos(t, "resume", id)
	if code != 0 || !strings.Contains(resumed, "The restart interrupted the wait") {
		t.Fatalf("aos resume: exit %d\n%s", code, resumed)
	}
	if show, _ := m.aos(t, "show", id); !strings.Contains(show, "succeeded") || !strings.Contains(show, "Resumed after AOS restarted") {
		t.Errorf("aos show %s after resuming:\n%s", id, show)
	}
	m.wantAudit(t, id, row{"resume_task", "allow", ""})
}

// A deliberately failing step pauses after 3 Retries, and a hint lets it go on.
func (m *machine) retries(t *testing.T) {
	s := m.tty(t, "run", "e2e-retry: read my notes")
	shown := s.waitFor("answer:", 2*time.Minute)
	if !strings.Contains(shown, "tried the same step 4 times without success (AOS_MAX_RETRIES=3)") {
		t.Errorf("the pause does not explain itself:\n%s", shown)
	}
	id := taskID(t, shown)
	if show, _ := m.aos(t, "show", id); !strings.Contains(show, "awaiting user") {
		t.Errorf("aos show %s during the pause:\n%s", id, show)
	}
	audit, _ := m.aos(t, "audit", id)
	if n := strings.Count(audit, "run_command"); n != 4 {
		t.Errorf("%d run_command calls, want the first and three Retries:\n%s", n, audit)
	}
	s.send("the notes are in ~/docs\n")
	if code := s.exit(time.Minute); code != 0 {
		t.Fatalf("aos run after the hint: exit %d\n%s", code, s.shown())
	}
	if !strings.Contains(s.shown(), "Found them in ~/docs") {
		t.Errorf("the Agent did not continue:\n%s", s.shown())
	}
	m.wantAudit(t, id, row{"retry_guard", "answer", ""})
}
