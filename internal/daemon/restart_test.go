package daemon

import (
	"errors"
	"reflect"
	"testing"
)

// TestRestarterForChoosesByInstallShape: a native install restarts through
// systemd, non-blocking, as one job; a Compose install has no systemd, so it
// drains and exits for `restart: unless-stopped` to bring the container back.
func TestRestarterForChoosesByInstallShape(t *testing.T) {
	var gotName string
	var gotArgs []string
	run := func(name string, args ...string) error { gotName, gotArgs = name, args; return nil }

	sysd := restarterFor(true, run, func() { t.Fatal("a native install must not use the Compose drain") })
	if _, ok := sysd.(systemdRestarter); !ok {
		t.Fatalf("native install got %T, want systemdRestarter", sysd)
	}
	if err := sysd.Restart(); err != nil {
		t.Fatal(err)
	}
	if gotName != "systemctl" || !reflect.DeepEqual(gotArgs, []string{"restart", "--no-block", "aos"}) {
		t.Errorf("systemd restarter ran %q %v, want systemctl restart --no-block aos", gotName, gotArgs)
	}

	drained := false
	comp := restarterFor(false, func(string, ...string) error {
		t.Fatal("a Compose install must not run systemctl")
		return nil
	}, func() { drained = true })
	if _, ok := comp.(composeRestarter); !ok {
		t.Fatalf("Compose install got %T, want composeRestarter", comp)
	}
	if err := comp.Restart(); err != nil {
		t.Fatal(err)
	}
	if !drained {
		t.Error("the Compose restarter did not trigger the drain")
	}
}

// TestComposeRestartRepliesBeforeItTearsDown: the Compose restarter only
// triggers the drain, which the serve loop turns into a graceful shutdown — it
// never exits inside Restart, so the RPC reply always flushes first (M7.3).
func TestComposeRestartRepliesBeforeItTearsDown(t *testing.T) {
	triggered := false
	r := restarterFor(false, nil, func() { triggered = true })
	// Restart returns without error and without blocking; the actual teardown is
	// left to the serve loop draining after the reply has gone out.
	if err := r.Restart(); err != nil {
		t.Fatalf("Restart returned %v, want nil so the reply can flush", err)
	}
	if !triggered {
		t.Error("Restart did not trigger the drain")
	}
}

// TestSystemdRestartFailureKeepsAOSUp: if systemctl fails, the error surfaces so
// the RPC reports it and aosd stays up rather than going down half-restarted.
func TestSystemdRestartFailureKeepsAOSUp(t *testing.T) {
	boom := errors.New("systemctl: no such unit aos.service")
	r := restarterFor(true, func(string, ...string) error { return boom }, nil)
	if err := r.Restart(); !errors.Is(err, boom) {
		t.Fatalf("Restart error %v, want %v", err, boom)
	}
}
