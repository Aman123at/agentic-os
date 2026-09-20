package api

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/audit"
	"github.com/Aman123at/agentic-os/internal/config"
	"github.com/Aman123at/agentic-os/internal/policy"
	"github.com/Aman123at/agentic-os/internal/settings"
	"github.com/Aman123at/agentic-os/internal/store"
)

// fakeSwitcher stands in for the Task Manager's queue gate (M7.7): it reports the
// Tasks it is told block the switch and, when none do, runs commit exactly as the
// real SwitchRealm would.
type fakeSwitcher struct {
	active  []*aosv1.Task
	commits int
}

func (f *fakeSwitcher) SwitchRealm(_ context.Context, commit func() error) ([]*aosv1.Task, error) {
	if len(f.active) > 0 {
		return f.active, nil
	}
	f.commits++
	if err := commit(); err != nil {
		return nil, err
	}
	return nil, nil
}

// rootRig is a Server wired for the Root Mode switch, with a real password, a
// real settings file, an audit log, a controllable clock, and the fake queue gate.
type rootRig struct {
	sys      systemService
	sw       *fakeSwitcher
	st       *settings.Store
	log      *audit.Log
	cfgPath  string
	restarts *int
	clock    *time.Time
	rootMode *bool
}

func newRootRig(t *testing.T) *rootRig {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	cfgPath := filepath.Join(dir, "config.yml")
	st, err := settings.Open(cfgPath,
		settings.Values{Model: "gpt-5.6-terra", Autonomy: policy.ConfirmRisky, MaxTasks: 3,
			MaxRetries: 3, TrashRetentionDays: 30, TrashMaxGB: 5},
		&config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	auth, _ := newAuth(t) // seeds the one user with testPassword
	sw := &fakeSwitcher{}
	log := &audit.Log{DB: db}
	restarts := 0
	rootMode := false
	clock := testClock
	s := &Server{
		Auth: auth, Audit: log, Settings: st, realmSwitch: sw,
		Info:    func() *aosv1.InfoResponse { return &aosv1.InfoResponse{RootMode: rootMode} },
		Restart: func() error { restarts++; return nil },
		Now:     func() time.Time { return clock },
	}
	return &rootRig{sys: systemService{s}, sw: sw, st: st, log: log, cfgPath: cfgPath,
		restarts: &restarts, clock: &clock, rootMode: &rootMode}
}

func (r *rootRig) set(ctx context.Context, enabled bool, password string) error {
	_, err := r.sys.SetRootMode(ctx, connect.NewRequest(&aosv1.SetRootModeRequest{Enabled: enabled, Password: password}))
	return err
}

// savedRootMode reads root_mode back from the settings the store persisted, which
// is config.yml on disk — untouched means still the fallback "false".
func (r *rootRig) savedRootMode(t *testing.T) string {
	t.Helper()
	for _, s := range r.st.List() {
		if s.Key == settings.RootModeKey {
			return s.Value
		}
	}
	t.Fatal("root_mode is not in the settings list")
	return ""
}

var desktopCtx = context.WithValue(context.Background(), actorKey{}, "user:desktop")
var cliCtx = context.WithValue(context.Background(), actorKey{}, "user:cli")

// TestRootModeRightPasswordSwitchesAndRestarts is the M7.7 happy path: the right
// password writes root_mode, audits the switch in the current Realm, and restarts.
func TestRootModeRightPasswordSwitchesAndRestarts(t *testing.T) {
	r := newRootRig(t)
	if err := r.set(desktopCtx, true, testPassword); err != nil {
		t.Fatalf("SetRootMode: %v", err)
	}
	if got := r.savedRootMode(t); got != "true" {
		t.Errorf("root_mode = %q, want true", got)
	}
	if *r.restarts != 1 {
		t.Errorf("restarts = %d, want 1", *r.restarts)
	}
	entries, _ := r.log.List(context.Background(), "", 10, 0)
	if len(entries) != 1 || entries[0].Tool != "root_mode" || entries[0].ResultSummary != "entered Root Mode" {
		t.Errorf("audit %+v, want one 'entered Root Mode' entry", entries)
	}
}

// TestRootModeWrongPasswordChangesNothing: a wrong password leaves config.yml
// untouched, does not restart, and reports how many tries are left (M7.7).
func TestRootModeWrongPasswordChangesNothing(t *testing.T) {
	r := newRootRig(t)
	err := r.set(desktopCtx, true, "not-the-password")
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("wrong password: %v, want Unauthenticated", err)
	}
	if n := attemptsLeft(t, err); n != 4 {
		t.Errorf("attempts left = %d, want 4", n)
	}
	if got := r.savedRootMode(t); got != "false" {
		t.Errorf("root_mode = %q after a wrong password, want it untouched (false)", got)
	}
	if *r.restarts != 0 {
		t.Errorf("restarts = %d after a wrong password, want 0", *r.restarts)
	}
}

// TestRootModeLocksOutAfterFiveWrongThenExpires: the sixth try is locked out with
// the unlock time, and once the window passes the switch works again (M7.7).
func TestRootModeLocksOutAfterFiveWrongThenExpires(t *testing.T) {
	r := newRootRig(t)
	for i := 0; i < rootModeMaxAttempts; i++ {
		if err := r.set(desktopCtx, true, "wrong"); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("attempt %d: %v, want Unauthenticated", i+1, err)
		}
	}
	// The sixth try is locked out before the password is even considered.
	err := r.set(desktopCtx, true, testPassword)
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("sixth try: %v, want ResourceExhausted", err)
	}
	if *r.restarts != 0 {
		t.Fatalf("a locked-out switch restarted %d times, want 0", *r.restarts)
	}
	// After the lock expires, the right password works.
	*r.clock = r.clock.Add(rootModeLockFor + time.Minute)
	if err := r.set(desktopCtx, true, testPassword); err != nil {
		t.Fatalf("after the lock expired: %v", err)
	}
	if *r.restarts != 1 {
		t.Errorf("restarts = %d after recovery, want 1", *r.restarts)
	}
}

// TestRootModeOffNeedsNoPassword: leaving Root Mode lowers privilege, so it asks
// for no password (M7.7).
func TestRootModeOffNeedsNoPassword(t *testing.T) {
	r := newRootRig(t)
	*r.rootMode = true // currently in Root Mode
	if err := r.set(desktopCtx, false, ""); err != nil {
		t.Fatalf("SetRootMode off: %v", err)
	}
	if got := r.savedRootMode(t); got != "false" {
		t.Errorf("root_mode = %q, want false", got)
	}
	if *r.restarts != 1 {
		t.Errorf("restarts = %d, want 1", *r.restarts)
	}
	entries, _ := r.log.List(context.Background(), "", 10, 0)
	if len(entries) != 1 || entries[0].ResultSummary != "left Root Mode" {
		t.Errorf("audit %+v, want one 'left Root Mode' entry", entries)
	}
}

// TestRootModeSocketCallerNeedsNoPassword: over the control socket the caller has
// already proven root (§7.5), so no password is asked (M7.11 uses this).
func TestRootModeSocketCallerNeedsNoPassword(t *testing.T) {
	r := newRootRig(t)
	if err := r.set(cliCtx, true, ""); err != nil {
		t.Fatalf("SetRootMode over the socket: %v", err)
	}
	if got := r.savedRootMode(t); got != "true" {
		t.Errorf("root_mode = %q, want true", got)
	}
}

// TestRootModeRefusedWhileActiveWithoutSpendingAPassword: a Task Queued, Running
// or AwaitingUser refuses the switch (FailedPrecondition, naming the Task) before
// the password is checked, so no attempt is spent (M7.7).
func TestRootModeRefusedWhileActiveWithoutSpendingAPassword(t *testing.T) {
	for _, st := range []aosv1.TaskState{
		aosv1.TaskState_TASK_STATE_QUEUED,
		aosv1.TaskState_TASK_STATE_RUNNING,
		aosv1.TaskState_TASK_STATE_AWAITING_USER,
	} {
		t.Run(st.String(), func(t *testing.T) {
			r := newRootRig(t)
			r.sw.active = []*aosv1.Task{{Id: "t_1", Title: "long job", State: st}}

			// A wrong password does not even matter: the active Task is caught first.
			err := r.set(desktopCtx, true, "wrong")
			if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("with an active Task: %v, want FailedPrecondition", err)
			}
			if blocked := blockedTasks(t, err); len(blocked) != 1 || blocked[0].Id != "t_1" || blocked[0].State != st {
				t.Fatalf("blocked detail = %+v, want the active Task", blocked)
			}
			if r.sw.commits != 0 || *r.restarts != 0 {
				t.Fatalf("commit ran (%d) or restarted (%d) with an active Task", r.sw.commits, *r.restarts)
			}

			// No password attempt was spent: once the Task clears, the first wrong
			// password still reports the full budget minus one.
			r.sw.active = nil
			err = r.set(desktopCtx, true, "wrong")
			if n := attemptsLeft(t, err); n != rootModeMaxAttempts-1 {
				t.Errorf("attempts left = %d after the refusal, want %d (no attempt spent while blocked)", n, rootModeMaxAttempts-1)
			}
		})
	}
}

// TestRootModeAlreadyInRealmDoesNothing: asking for the Realm already in force
// neither restarts nor asks for a password (M7.7).
func TestRootModeAlreadyInRealmDoesNothing(t *testing.T) {
	r := newRootRig(t)
	if err := r.set(desktopCtx, false, ""); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("switching to the current Realm: %v, want FailedPrecondition", err)
	}
	if r.sw.commits != 0 || *r.restarts != 0 {
		t.Errorf("a no-op switch committed (%d) or restarted (%d)", r.sw.commits, *r.restarts)
	}
}

// TestRootModeUnavailableWithoutARestarter: without a restarter the switch is
// refused rather than writing the key and failing to restart into the new Realm.
func TestRootModeUnavailableWithoutARestarter(t *testing.T) {
	r := newRootRig(t)
	r.sys.s.Restart = nil
	if err := r.set(desktopCtx, true, testPassword); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("no restarter: %v, want Unavailable", err)
	}
	if got := r.savedRootMode(t); got != "false" {
		t.Errorf("root_mode = %q, want it untouched", got)
	}
}

// TestGenericUpdateRefusesRootMode: the ordinary settings path never changes
// root_mode; it points at the guarded switch instead (M7.7).
func TestGenericUpdateRefusesRootMode(t *testing.T) {
	r := newRootRig(t)
	_, err := settingsService{r.sys.s}.Update(desktopCtx, connect.NewRequest(&aosv1.UpdateSettingRequest{Key: "root_mode", Value: "true"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("Update root_mode: %v, want InvalidArgument", err)
	}
	if got := r.savedRootMode(t); got != "false" {
		t.Errorf("root_mode = %q after a refused Update, want it untouched", got)
	}
}

func attemptsLeft(t *testing.T, err error) int {
	t.Helper()
	for _, d := range details(t, err) {
		if a, ok := d.(*aosv1.RootModeAttempt); ok {
			return int(a.AttemptsLeft)
		}
	}
	t.Fatalf("no RootModeAttempt detail on %v", err)
	return 0
}

func blockedTasks(t *testing.T, err error) []*aosv1.BlockingTask {
	t.Helper()
	for _, d := range details(t, err) {
		if b, ok := d.(*aosv1.RootModeBlocked); ok {
			return b.Tasks
		}
	}
	t.Fatalf("no RootModeBlocked detail on %v", err)
	return nil
}

// details unwraps the proto detail messages carried on a connect error.
func details(t *testing.T, err error) []proto.Message {
	t.Helper()
	var ce *connect.Error
	if !errors.As(err, &ce) {
		t.Fatalf("not a connect error: %v", err)
	}
	var out []proto.Message
	for _, d := range ce.Details() {
		if msg, verr := d.Value(); verr == nil {
			out = append(out, msg)
		}
	}
	return out
}
