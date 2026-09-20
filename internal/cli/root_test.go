package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/gen/go/aos/v1/aosv1connect"
	"github.com/Aman123at/agentic-os/internal/settings"
)

// fakeSystem is a SystemServiceClient that records SetRootMode calls and can be
// told to refuse the switch. The embedded interface satisfies the methods the
// tests never touch (they panic if called, which would fail the test loudly).
type fakeSystem struct {
	aosv1connect.SystemServiceClient
	rootMode   bool
	setErr     error
	calls      []bool // one entry per SetRootMode call, its Enabled value
	clearErr   error
	clearCalls int // ClearRootHistory calls
}

func (f *fakeSystem) Info(context.Context, *connect.Request[aosv1.InfoRequest]) (*connect.Response[aosv1.InfoResponse], error) {
	return connect.NewResponse(&aosv1.InfoResponse{RootMode: f.rootMode}), nil
}

func (f *fakeSystem) SetRootMode(_ context.Context, req *connect.Request[aosv1.SetRootModeRequest]) (*connect.Response[aosv1.SetRootModeResponse], error) {
	f.calls = append(f.calls, req.Msg.Enabled)
	if f.setErr != nil {
		return nil, f.setErr
	}
	return connect.NewResponse(&aosv1.SetRootModeResponse{}), nil
}

func (f *fakeSystem) ClearRootHistory(context.Context, *connect.Request[aosv1.ClearRootHistoryRequest]) (*connect.Response[aosv1.ClearRootHistoryResponse], error) {
	f.clearCalls++
	if f.clearErr != nil {
		return nil, f.clearErr
	}
	return connect.NewResponse(&aosv1.ClearRootHistoryResponse{}), nil
}

// TestSwitchRootNeedsAYes checks the confirmation gate: without --yes, anything
// but `yes` cancels the switch and never reaches aosd, and `yes` lets it through.
func TestSwitchRootNeedsAYes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typed   string
		wantSet bool
	}{
		{"no cancels", "no\n", false},
		{"empty cancels", "\n", false},
		{"eof cancels", "", false},
		{"yes proceeds", "yes\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSystem{}
			var out strings.Builder
			if err := switchRoot(context.Background(), strings.NewReader(tc.typed), &out, &client{system: f}, true, false); err != nil {
				t.Fatalf("switchRoot: %v", err)
			}
			if got := len(f.calls) == 1; got != tc.wantSet {
				t.Fatalf("SetRootMode called %d time(s), want called=%v", len(f.calls), tc.wantSet)
			}
			if tc.wantSet {
				if !f.calls[0] {
					t.Errorf("SetRootMode called with Enabled=false, want true")
				}
				if !strings.Contains(out.String(), "Restarting AOS into Root Mode") {
					t.Errorf("no restart message: %q", out.String())
				}
			} else if !strings.Contains(out.String(), "Cancelled") {
				t.Errorf("no cancel message: %q", out.String())
			}
			// The warning is always shown, so nobody switches without reading it.
			if !strings.Contains(out.String(), "cannot be undone") {
				t.Errorf("warning not printed: %q", out.String())
			}
		})
	}
}

// TestSwitchRootYesSkipsTheConfirm checks --yes: the switch goes straight through
// without reading stdin, for scripts.
func TestSwitchRootYesSkipsTheConfirm(t *testing.T) {
	f := &fakeSystem{}
	var out strings.Builder
	// A reader that fails if read, proving --yes never consults stdin.
	if err := switchRoot(context.Background(), failReader{t}, &out, &client{system: f}, false, true); err != nil {
		t.Fatalf("switchRoot: %v", err)
	}
	if len(f.calls) != 1 || f.calls[0] {
		t.Fatalf("SetRootMode calls = %v, want one call with Enabled=false", f.calls)
	}
	if !strings.Contains(out.String(), "Restarting AOS into Standard Mode") {
		t.Errorf("no restart message: %q", out.String())
	}
}

// TestSwitchRootRefusedWhileActive checks the running-Agent gate: a switch aosd
// refuses (FailedPrecondition with a RootModeBlocked detail) prints the Task and
// exits non-zero, and --yes does not override it.
func TestSwitchRootRefusedWhileActive(t *testing.T) {
	blocked := connect.NewError(connect.CodeFailedPrecondition, errors.New("an Agent is still working"))
	detail, err := connect.NewErrorDetail(&aosv1.RootModeBlocked{
		Tasks: []*aosv1.BlockingTask{{Id: "task_42", Title: "Reindex the archive", State: aosv1.TaskState_TASK_STATE_RUNNING}},
	})
	if err != nil {
		t.Fatal(err)
	}
	blocked.AddDetail(detail)

	f := &fakeSystem{setErr: blocked}
	var out strings.Builder
	got := switchRoot(context.Background(), strings.NewReader(""), &out, &client{system: f}, true, true)

	var exit exitError
	if !errors.As(got, &exit) || exit.code == 0 {
		t.Fatalf("switchRoot error = %v, want a non-zero exitError", got)
	}
	s := out.String()
	for _, want := range []string{"task_42", "Reindex the archive", "aos cancel"} {
		if !strings.Contains(s, want) {
			t.Errorf("blocked output missing %q:\n%s", want, s)
		}
	}
}

// TestClearRootNeedsAYes checks `aos root clear`'s confirmation gate: without
// --yes, anything but `yes` cancels and never reaches aosd; `yes` clears (M7.12).
func TestClearRootNeedsAYes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		typed     string
		wantClear bool
	}{
		{"no cancels", "no\n", false},
		{"empty cancels", "\n", false},
		{"yes proceeds", "yes\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSystem{}
			var out strings.Builder
			if err := clearRoot(context.Background(), strings.NewReader(tc.typed), &out, &client{system: f}, false); err != nil {
				t.Fatalf("clearRoot: %v", err)
			}
			if got := f.clearCalls == 1; got != tc.wantClear {
				t.Fatalf("ClearRootHistory called %d time(s), want called=%v", f.clearCalls, tc.wantClear)
			}
			if tc.wantClear {
				if !strings.Contains(out.String(), "Clearing Root Mode history and restarting") {
					t.Errorf("no restart message: %q", out.String())
				}
			} else if !strings.Contains(out.String(), "Cancelled") {
				t.Errorf("no cancel message: %q", out.String())
			}
			// The warning names what stays, always.
			if !strings.Contains(out.String(), "is real and stays") {
				t.Errorf("warning not printed: %q", out.String())
			}
		})
	}
}

// TestClearRootYesSkipsTheConfirm checks --yes: the clear goes straight through
// without reading stdin, for scripts (M7.12).
func TestClearRootYesSkipsTheConfirm(t *testing.T) {
	f := &fakeSystem{}
	var out strings.Builder
	if err := clearRoot(context.Background(), failReader{t}, &out, &client{system: f}, true); err != nil {
		t.Fatalf("clearRoot: %v", err)
	}
	if f.clearCalls != 1 {
		t.Fatalf("ClearRootHistory calls = %d, want one", f.clearCalls)
	}
}

// TestClearRootRefusedWhileActive checks the running-Agent gate on the clear: a
// refusal carrying RootModeBlocked prints the Task and exits non-zero (M7.12).
func TestClearRootRefusedWhileActive(t *testing.T) {
	blocked := connect.NewError(connect.CodeFailedPrecondition, errors.New("an Agent is still working"))
	detail, err := connect.NewErrorDetail(&aosv1.RootModeBlocked{
		Tasks: []*aosv1.BlockingTask{{Id: "task_42", Title: "Reindex the archive", State: aosv1.TaskState_TASK_STATE_RUNNING}},
	})
	if err != nil {
		t.Fatal(err)
	}
	blocked.AddDetail(detail)

	f := &fakeSystem{clearErr: blocked}
	var out strings.Builder
	got := clearRoot(context.Background(), strings.NewReader(""), &out, &client{system: f}, true)

	var exit exitError
	if !errors.As(got, &exit) || exit.code == 0 {
		t.Fatalf("clearRoot error = %v, want a non-zero exitError", got)
	}
	s := out.String()
	for _, want := range []string{"task_42", "Reindex the archive", "aos cancel"} {
		if !strings.Contains(s, want) {
			t.Errorf("blocked output missing %q:\n%s", want, s)
		}
	}
}

// TestConfigRefusesRootModeTowardsAosRoot checks the config path stays closed:
// changing root_mode through the generic settings surface points at `aos root`.
func TestConfigRefusesRootModeTowardsAosRoot(t *testing.T) {
	msg := settings.ErrRootModeNotHere.Error()
	if !strings.Contains(msg, "aos root on") || !strings.Contains(msg, "aos root off") {
		t.Fatalf("root_mode refusal does not point at aos root: %q", msg)
	}
}

// TestRootBannerRendering checks the banner: it names ROOT MODE, and carries the
// red ANSI codes only when colour is on.
func TestRootBannerRendering(t *testing.T) {
	plain := rootBannerLine(newStyles(false))
	if !strings.Contains(plain, "ROOT MODE") {
		t.Errorf("plain banner missing ROOT MODE: %q", plain)
	}
	if strings.Contains(plain, "\x1b[") {
		t.Errorf("plain banner has ANSI codes: %q", plain)
	}
	coloured := rootBannerLine(newStyles(true))
	if !strings.Contains(coloured, "\x1b[31m") {
		t.Errorf("coloured banner missing the red code: %q", coloured)
	}
}

// TestOnOff maps the Realm to the one word bare `aos root` prints.
func TestOnOff(t *testing.T) {
	if onOff(true) != "on" || onOff(false) != "off" {
		t.Fatalf("onOff: got %q/%q, want on/off", onOff(true), onOff(false))
	}
}

// failReader fails the test if anything reads from it.
type failReader struct{ t *testing.T }

func (r failReader) Read([]byte) (int, error) {
	r.t.Fatal("stdin was read despite --yes")
	return 0, nil
}
