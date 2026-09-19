package agent

import (
	"strings"
	"testing"
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
