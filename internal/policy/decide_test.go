package policy

import (
	"slices"
	"testing"
)

const home = "/home/aos"

func protection() *Protection { return NewProtection(home, nil) }

func TestChangingAProtectedPathNeedsApprovalAtEveryAutonomy(t *testing.T) {
	call := Call{Tool: "delete", Risky: true, Effects: []Effect{{Path: "/home/aos/.ssh/id_ed25519", Op: Delete}}, Folder: "/home/aos/.ssh"}
	for _, a := range []Autonomy{Auto, ConfirmRisky, ConfirmAll} {
		d := Decide(call, Context{Autonomy: a, Interactive: true, Landlock: true, Protected: protection()})
		if d.Verdict != Ask || !slices.Equal(d.ProtectedPaths, []string{"/home/aos/.ssh/id_ed25519"}) || d.Grantable {
			t.Errorf("autonomy %v: got %+v, want Ask for the Protected Path, not grantable", a, d)
		}
	}
	d := Decide(call, Context{Autonomy: Auto, Interactive: false, Landlock: true, Protected: protection()})
	if d.Verdict != Deny {
		t.Errorf("non-interactive: got %+v, want Deny", d)
	}
}

func TestRiskyActionsFollowAutonomy(t *testing.T) {
	risky := Call{Tool: "delete", Risky: true, Reasons: []string{"deletes ~/notes.txt"}, Effects: []Effect{{Path: "/home/aos/notes.txt", Op: Delete}}, Folder: "/home/aos"}
	safe := Call{Tool: "list_dir", Folder: "/home/aos"}
	for _, tc := range []struct {
		name        string
		call        Call
		autonomy    Autonomy
		interactive bool
		want        Verdict
	}{
		{"risky, auto", risky, Auto, true, Allow},
		{"risky, confirm-risky", risky, ConfirmRisky, true, Ask},
		{"risky, confirm-all", risky, ConfirmAll, true, Ask},
		{"safe, confirm-risky", safe, ConfirmRisky, true, Allow},
		{"safe, confirm-all", safe, ConfirmAll, true, Ask},
		{"risky, confirm-risky, nobody to ask", risky, ConfirmRisky, false, Deny},
		{"risky, auto, nobody to ask (aos run --autonomy auto)", risky, Auto, false, Allow},
		{"safe, confirm-all, nobody to ask", safe, ConfirmAll, false, Deny},
	} {
		d := Decide(tc.call, Context{Autonomy: tc.autonomy, Interactive: tc.interactive, Landlock: true, Protected: protection()})
		if d.Verdict != tc.want {
			t.Errorf("%s: got %+v, want verdict %v", tc.name, d, tc.want)
		}
		if d.Verdict == Ask && !d.Grantable {
			t.Errorf("%s: Approval for an unprotected call must offer Allow for this Task", tc.name)
		}
	}
}

func TestTaskGrantsCoverTheSameToolInTheSameFolder(t *testing.T) {
	grants := []Grant{{Tool: "delete", Folder: "/home/aos/Downloads"}}
	ctx := Context{Autonomy: ConfirmRisky, Interactive: true, Landlock: true, Grants: grants, Protected: protection()}
	deleteIn := func(folder string) Call {
		return Call{Tool: "delete", Risky: true, Effects: []Effect{{Path: folder + "/x", Op: Delete}}, Folder: folder}
	}
	for _, tc := range []struct {
		name string
		call Call
		want Verdict
	}{
		{"same Tool, same folder", deleteIn("/home/aos/Downloads"), Allow},
		{"same Tool, subfolder", deleteIn("/home/aos/Downloads/zips"), Allow},
		{"same Tool, sibling folder with a shared prefix", deleteIn("/home/aos/Downloads2"), Ask},
		{"other Tool, same folder", Call{Tool: "move", Risky: true, Folder: "/home/aos/Downloads"}, Ask},
	} {
		if d := Decide(tc.call, ctx); d.Verdict != tc.want || (d.Verdict == Allow && d.By != "grant") {
			t.Errorf("%s: got %+v, want %v", tc.name, d, tc.want)
		}
	}

	ctx.Protected = NewProtection(home, []string{"/home/aos/Downloads/taxes"})
	if d := Decide(deleteIn("/home/aos/Downloads/taxes"), ctx); d.Verdict != Ask {
		t.Errorf("a grant must never cover a Protected Path: got %+v", d)
	}
}

func TestCoordinationToolsNeverNeedApproval(t *testing.T) {
	ctx := Context{Autonomy: ConfirmAll, Interactive: false, Landlock: true, Protected: protection()}
	if d := Decide(Call{Tool: "ask_user", Coordination: true}, ctx); d.Verdict != Allow {
		t.Errorf("got %+v, want Allow", d)
	}
}

func TestWithoutLandlockAutoActsAsConfirmRisky(t *testing.T) {
	ctx := Context{Autonomy: Auto, Interactive: true, Landlock: false, Protected: protection()}
	if d := Decide(Call{Tool: "delete", Risky: true, Folder: "/home/aos"}, ctx); d.Verdict != Ask {
		t.Errorf("risky call: got %+v, want Ask", d)
	}
	if d := Decide(Call{Tool: "list_dir", Folder: "/home/aos"}, ctx); d.Verdict != Allow {
		t.Errorf("safe call: got %+v, want Allow", d)
	}
}

// TestRootModeSkipsTheProtectedStepButKeepsRisky is the M7.4 policy change: in
// Root Mode step 1 (Protected Paths, locks, .env, dirty-git) does not run, so a
// change to a Protected Path is allowed without an Approval — while Risky Actions
// still follow the Autonomy level, so confirm-risky still asks before an rm -rf.
func TestRootModeSkipsTheProtectedStepButKeepsRisky(t *testing.T) {
	// A write to a Protected dotfile: Standard asks, Root allows outright.
	protectedWrite := Call{Tool: "write_file", Effects: []Effect{{Path: "/home/aos/.ssh/config", Op: Write}}, Folder: "/home/aos/.ssh"}
	if d := Decide(protectedWrite, Context{Autonomy: Auto, Interactive: true, Landlock: true, Protected: protection()}); d.Verdict != Ask {
		t.Errorf("Standard: got %+v, want Ask for the Protected Path", d)
	}
	if d := Decide(protectedWrite, Context{Autonomy: Auto, Interactive: true, Landlock: true, Root: true, Protected: protection()}); d.Verdict != Allow || d.By != "autonomy" {
		t.Errorf("Root: got %+v, want Allow with the Protected step skipped", d)
	}

	// A .env write, likewise: enforced by pattern in Standard, ignored in Root.
	envWrite := Call{Tool: "write_file", Effects: []Effect{{Path: "/home/aos/project/.env", Op: Write}}, Folder: "/home/aos/project"}
	if d := Decide(envWrite, Context{Autonomy: Auto, Interactive: true, Landlock: true, Protected: protection()}); d.Verdict != Ask {
		t.Errorf("Standard .env: got %+v, want Ask", d)
	}
	if d := Decide(envWrite, Context{Autonomy: Auto, Interactive: true, Landlock: true, Root: true, Protected: protection()}); d.Verdict != Allow {
		t.Errorf("Root .env: got %+v, want Allow", d)
	}

	// Risky Actions are unchanged: confirm-risky still asks, even in Root Mode.
	risky := Call{Tool: "run_command", Risky: true, Reasons: []string{"rm -rf build"}, Effects: []Effect{{Path: "/srv/app/build", Op: Delete}}, Folder: "/srv/app"}
	if d := Decide(risky, Context{Autonomy: ConfirmRisky, Interactive: true, Landlock: true, Root: true, Protected: protection()}); d.Verdict != Ask {
		t.Errorf("Root Risky under confirm-risky: got %+v, want Ask", d)
	}
	if d := Decide(risky, Context{Autonomy: Auto, Interactive: true, Landlock: true, Root: true, Protected: protection()}); d.Verdict != Allow {
		t.Errorf("Root Risky under auto: got %+v, want Allow", d)
	}
}
