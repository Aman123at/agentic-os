package service

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/events"
	"github.com/Aman123at/agentic-os/internal/software"
	"github.com/Aman123at/agentic-os/internal/store"
)

func newSupervisor(t *testing.T, db *store.DB, dir string) *Supervisor {
	t.Helper()
	s := &Supervisor{DB: db, Ledger: &software.Ledger{DB: db}, Bus: events.New(), LogDir: filepath.Join(dir, "logs"),
		Procfs: filepath.Join(dir, "no-proc"), Backoff: 50 * time.Millisecond,
		Launch: func(d Definition) (*exec.Cmd, error) { return exec.Command("sh", "-c", d.Command), nil }}
	if err := s.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func setup(t *testing.T) (*Supervisor, *store.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return newSupervisor(t, db, dir), db, dir
}

// waitFor waits until a Service matches ok.
func waitFor(t *testing.T, s *Supervisor, name string, ok func(*aosv1.ServiceInfo) bool) *aosv1.ServiceInfo {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		i, err := s.Get(name)
		if err == nil && ok(i) {
			return i
		}
		if time.Now().After(deadline) {
			t.Fatalf("Service %s: %+v, %v", name, i, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func running(i *aosv1.ServiceInfo) bool { return i.State == aosv1.ServiceState_SERVICE_STATE_RUNNING }

func logs(t *testing.T, s *Supervisor, name string) string {
	t.Helper()
	l, err := s.Logs(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(l.Tail(0))
}

func TestAServiceRunsIsLoggedAndStopsAndStarts(t *testing.T) {
	s, _, _ := setup(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, Definition{Name: "hello", Command: "echo hello from the service; exec sleep 30", Autostart: true}, "agent"); err != nil {
		t.Fatal(err)
	}
	first := waitFor(t, s, "hello", running)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs(t, s, "hello"), "hello from the service") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if out := logs(t, s, "hello"); !strings.Contains(out, "[aos] started (pid") || !strings.Contains(out, "hello from the service") {
		t.Errorf("log %q", out)
	}
	stopped, err := s.Stop("hello")
	if err != nil || stopped.State != aosv1.ServiceState_SERVICE_STATE_STOPPED || stopped.Pid != 0 {
		t.Fatalf("after stop: %+v, %v", stopped, err)
	}
	if _, err := s.Start("hello"); err != nil {
		t.Fatal(err)
	}
	again := waitFor(t, s, "hello", running)
	if again.Pid == first.Pid {
		t.Error("the Service did not start a new process")
	}
	if _, err := s.Stop("missing"); !errors.Is(err, ErrNoService) {
		t.Errorf("stopping an unknown Service: %v", err)
	}
}

func TestRestartPoliciesDecideWhatHappensAfterAnExit(t *testing.T) {
	s, _, _ := setup(t)
	ctx := context.Background()
	for _, d := range []Definition{
		{Name: "crash", Command: "echo boom; exit 3"},                  // on-failure by default
		{Name: "done", Command: "exit 0"},                              // on-failure: a clean exit stays down
		{Name: "never", Command: "exit 4", Restart: Never},             // never
		{Name: "always", Command: "exit 0", Restart: Always, Env: nil}, // always, even after a clean exit
	} {
		if _, err := s.Create(ctx, d, "agent"); err != nil {
			t.Fatal(err)
		}
	}
	crash := waitFor(t, s, "crash", func(i *aosv1.ServiceInfo) bool { return i.Restarts >= 2 })
	if crash.LastExit != "exit 3" || crash.Restart != aosv1.RestartPolicy_RESTART_POLICY_ON_FAILURE {
		t.Errorf("crash %+v", crash)
	}
	if out := logs(t, s, "crash"); !strings.Contains(out, "[aos] exited: exit 3; restarting in 50ms") || !strings.Contains(out, "restarting in 100ms") {
		t.Errorf("crash log %q", out)
	}
	waitFor(t, s, "done", func(i *aosv1.ServiceInfo) bool {
		return i.State == aosv1.ServiceState_SERVICE_STATE_STOPPED && i.LastExit == "exit 0" && i.Restarts == 0
	})
	waitFor(t, s, "never", func(i *aosv1.ServiceInfo) bool { return i.State == aosv1.ServiceState_SERVICE_STATE_FAILED })
	waitFor(t, s, "always", func(i *aosv1.ServiceInfo) bool { return i.Restarts >= 1 })

	for _, bad := range []Definition{{Name: "Bad Name", Command: "true"}, {Name: "x", Command: " "}, {Name: "y", Command: "true", Restart: "sometimes"},
		{Name: "z", Command: "true", Env: map[string]string{"1X": "a"}}} {
		if _, err := s.Create(ctx, bad, "agent"); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
}

func TestCreatingIsRecordedInTheLedgerAndARestoreUndoesIt(t *testing.T) {
	s, db, dir := setup(t)
	ctx := context.Background()
	ledger := &software.Ledger{DB: db}
	cp, _ := ledger.CreateCheckpoint(ctx, "before the site", "", false)
	site := Definition{Name: "site", Command: "exec sleep 30", Autostart: true, TaskID: "t_1"}
	if _, err := s.Create(ctx, site, "agent"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, "site", running)
	ops, _ := ledger.After(ctx, cp.LedgerId)
	if len(ops) != 1 || ops[0].Action != "service" || ops[0].TaskID != "t_1" || ops[0].Actor != "agent" ||
		ops[0].Changes[0].Before != "" || !strings.Contains(ops[0].Changes[0].After, `"command":"exec sleep 30"`) {
		t.Fatalf("ledger %+v", ops)
	}

	// A Restore to the Checkpoint removes the Service without recording anything itself.
	for k, v := range software.Undo(ops) {
		if err := s.Restore(ctx, k.Name, v); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.List()) != 0 {
		t.Fatalf("after the Restore: %v", s.List())
	}
	// And undoing the Restore brings it back, running.
	if err := s.Restore(ctx, "site", site.JSON()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, "site", running)
	if ops, _ := ledger.After(ctx, cp.LedgerId); len(ops) != 1 {
		t.Errorf("a Restore recorded %d operations", len(ops)-1)
	}

	// Stored Services come back with the Machine, and so does their log.
	s.Close()
	again := newSupervisor(t, db, dir)
	if i, err := again.Get("site"); err != nil || i.State != aosv1.ServiceState_SERVICE_STATE_STOPPED {
		t.Fatalf("after loading: %+v, %v", i, err)
	}
	if !strings.Contains(logs(t, again, "site"), "[aos] stopped") {
		t.Errorf("the earlier log is gone: %q", logs(t, again, "site"))
	}
	again.StartAll()
	waitFor(t, again, "site", running)
	if err := again.Remove(ctx, "site", "user:cli", ""); err != nil {
		t.Fatal(err)
	}
	if err := again.Remove(ctx, "site", "user:cli", ""); !errors.Is(err, ErrNoService) {
		t.Errorf("removing twice: %v", err)
	}
	if ops, _ := ledger.After(ctx, cp.LedgerId); len(ops) != 2 || ops[1].Actor != "user:cli" || ops[1].Changes[0].After != "" {
		t.Errorf("ledger after the removal: %+v", ops)
	}
}
