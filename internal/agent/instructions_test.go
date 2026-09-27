package agent

import (
	"strings"
	"testing"
	"unicode/utf8"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
)

// TestInstructionsDropTheDockerEraFalsehoods guards the M6.12 rewrite: the
// prompt must no longer claim a container, a missing systemd, a fresh system on
// restart, or the retired Shared Folder, and must name the native reality.
func TestInstructionsDropTheDockerEraFalsehoods(t *testing.T) {
	got := Instructions(Machine{OS: "Ubuntu 24.04.3 LTS", Arch: "amd64", Mode: "ui", Landlock: true, Browser: true})

	for _, gone := range []string{
		"running in Docker",
		"There is no systemd",
		"fresh system",
		"~/Shared",
		"Shared Folder",
	} {
		if strings.Contains(got, gone) {
			t.Errorf("the prompt still carries the retired phrase %q:\n%s", gone, got)
		}
	}

	for _, want := range []string{
		"Ubuntu 24.04.3 LTS (amd64), in ui Mode.",
		"persistent server",
		"filesystem survives a restart",
		"systemd units",
		"manage_service",
		"~/Downloads",
		// M6.13: bind Services to loopback unless the user asked for public.
		"Bind Services to 127.0.0.1",
		// M6.15: the native Machine may be bare, so the prompt tells the Agent to
		// install what a Task needs rather than assuming a toolchain is present.
		"minimal toolchain",
		"don't assume a runtime is already there",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, got)
		}
	}
}

// TestInstructionsBrowserSection stays gated on Browser.
func TestInstructionsBrowserSection(t *testing.T) {
	with := Instructions(Machine{Browser: true})
	if !strings.Contains(with, "## Browser") || !strings.Contains(with, "browser_open") {
		t.Errorf("the Browser section is missing when Browser is true:\n%s", with)
	}
	without := Instructions(Machine{Browser: false})
	if strings.Contains(without, "## Browser") {
		t.Errorf("the Browser section appears when Browser is false:\n%s", without)
	}
}

// TestInstructionsDefaults falls back to Ubuntu / cli when unset.
func TestInstructionsDefaults(t *testing.T) {
	got := Instructions(Machine{})
	if !strings.Contains(got, "Ubuntu (unknown architecture), in cli Mode.") {
		t.Errorf("defaults not applied:\n%s", got)
	}
	if !strings.Contains(got, "policy checks are the only guard") {
		t.Errorf("the no-Landlock sandbox line is missing:\n%s", got)
	}
}

// TestInstructionsRootMode is the M7.6 golden for both Realms: Standard says the
// Agent acts as aos without sudo and that Protected Paths need an Approval; Root
// Mode says it is root, that sudo is unnecessary and nothing is Protected, while
// both keep the shared guidance (install_package, the sandbox line).
func TestInstructionsRootMode(t *testing.T) {
	std := Instructions(Machine{OS: "Ubuntu 24.04.3 LTS", Arch: "amd64", Mode: "ui", Landlock: true})
	for _, want := range []string{
		"You act as the user aos (home folder ~ = /home/aos), without sudo.",
		"Protected Paths can't be changed without the user's Approval",
	} {
		if !strings.Contains(std, want) {
			t.Errorf("Standard prompt lacks %q:\n%s", want, std)
		}
	}
	for _, gone := range []string{"You are **root**", "Nothing is Protected in Root Mode"} {
		if strings.Contains(std, gone) {
			t.Errorf("Standard prompt carries Root-only text %q:\n%s", gone, std)
		}
	}

	root := Instructions(Machine{OS: "Ubuntu 24.04.3 LTS", Arch: "amd64", Mode: "ui", Landlock: true, Root: true})
	for _, want := range []string{
		"You are **root** on this Machine (home folder ~ = /root)",
		"`sudo` is unnecessary",
		"nothing on the filesystem is Protected",
		"say what you are about to do before you touch system files",
		"Nothing is Protected in Root Mode",
		"Risky Actions still do",
		// The shared guidance survives in both Realms.
		"install_package",
		"Landlock enforces these protections",
	} {
		if !strings.Contains(root, want) {
			t.Errorf("Root prompt lacks %q:\n%s", want, root)
		}
	}
	if strings.Contains(root, "You act as the user aos") {
		t.Errorf("Root prompt still calls the Agent aos:\n%s", root)
	}
}

// TestEditorNote is the whole of the note for a Task from Code (M8.11): the
// Workspace, how to work there, the files open by their Workspace paths, and
// the text the user added, fenced past any backticks in it.
func TestEditorNote(t *testing.T) {
	if EditorNote(nil) != "" || EditorNote(&aosv1.EditorContext{}) != "" {
		t.Error("a Task not from Code has a note")
	}
	got := EditorNote(&aosv1.EditorContext{
		Workspace:          "/home/aos/project/",
		ActiveFile:         "/home/aos/project/src/util.ts",
		OpenFiles:          []string{"/home/aos/project/src/util.ts", "/home/aos/project/main.go", "/etc/hosts"},
		Selection:          "const s = ```x```;\n",
		SelectionStartLine: 3,
		SelectionEndLine:   4,
	})
	want := "# Working in Code\n" +
		"The user is working in Code, AOS's editor, on the folder /home/aos/project (the Workspace), and wrote this from its Agent sidebar. Every file you change with the Files Tools is shown to them as a diff they keep or discard hunk by hunk.\n" +
		"- Unless the user says otherwise, work in the Workspace. Your Session doesn't start there: give full paths, or cd into it first.\n" +
		"- Read a file before you change it. Prefer edit_file for changes to existing code, with enough surrounding lines to match once; write_file is for new files.\n" +
		"- Keep changes small and to the point: don't reformat, reorder or rename what the Task doesn't need.\n" +
		"- Build and test your change with run_command where the project allows.\n" +
		"- Finish with the files you changed, a line each saying what and why.\n" +
		"\nThe file in front: src/util.ts\n" +
		"Open files: src/util.ts, main.go, /etc/hosts\n" +
		"\nThe user added this from lines 3–4 of src/util.ts to the chat:\n" +
		"````\nconst s = ```x```;\n````\n"
	if got != want {
		t.Errorf("note:\n%s\nwant:\n%s", got, want)
	}

	long := EditorNote(&aosv1.EditorContext{Workspace: "/w", Selection: strings.Repeat("é", MaxSelection)})
	if !strings.Contains(long, "the editor to the chat (cut short)") || !utf8.ValidString(long) || len(long) > MaxSelection+2048 {
		t.Errorf("a long selection is not cut to %d bytes: %d bytes", MaxSelection, len(long))
	}
}
