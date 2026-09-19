// Package profile builds what every Agent is told about the Machine and the
// user (PLAN.md §8.5): the Memory the user accepted, and the Machine Profile.
package profile

import (
	"fmt"
	"strings"
)

// Machine is what the Machine Profile describes.
type Machine struct {
	OS, Arch, Mode string
	Landlock       bool
	// Software is what the Install Ledger installed, without dependencies.
	Software  []Software
	Services  []Service
	Listeners []Listener
	// Replaying is true while Replay reinstalls software after a restart.
	Replaying bool
}

type Software struct {
	Manager, Name, Version string
}

type Service struct {
	Name, State string
	Ports       []int
}

// Listener is a program listening on a TCP port.
type Listener struct {
	Port             int
	Process, Service string
}

// maxProfile is the Machine Profile's size budget (PLAN.md §8.5).
const maxProfile = 1024

// Profile renders the Machine Profile. Lists are shortened to stay under 1 KB.
func Profile(m Machine) string {
	var text string
	for _, limit := range []int{8, 4, 2, 0} {
		text = render(m, limit)
		if len(text) < maxProfile {
			break
		}
	}
	return text
}

func render(m Machine, limit int) string {
	var b strings.Builder
	b.WriteString("# Machine Profile\n")
	sandbox := "Landlock confines Agents"
	if !m.Landlock {
		sandbox = "no Landlock on this Host: policy checks only"
	}
	fmt.Fprintf(&b, "- %s on %s, %s Mode; %s.\n", or(m.OS, "Ubuntu"), or(m.Arch, "unknown architecture"), or(m.Mode, "cli"), sandbox)
	if m.Replaying {
		b.WriteString("- Replay is reinstalling software after a restart; installs wait until it finishes.\n")
	}
	software := make([]string, len(m.Software))
	for i, s := range m.Software {
		software[i] = fmt.Sprintf("%s %s (%s)", s.Name, s.Version, s.Manager)
	}
	b.WriteString("- Installed through AOS: " + list(software, limit, "nothing yet") + ".\n")
	b.WriteString("- The Machine starts with a minimal toolchain; install what a Task needs (Node, a Python runner, …) with install_package rather than assuming it is present.\n")
	services := make([]string, len(m.Services))
	for i, s := range m.Services {
		services[i] = s.Name + " (" + s.State
		if len(s.Ports) > 0 {
			services[i] += ", port " + joinInts(s.Ports)
		}
		services[i] += ")"
	}
	b.WriteString("- Services: " + list(services, limit, "none") + ".\n")
	var other []string
	for _, l := range m.Listeners {
		if l.Service == "" {
			other = append(other, fmt.Sprintf("%d (%s)", l.Port, or(l.Process, "unknown")))
		}
	}
	if len(other) > 0 {
		b.WriteString("- Also listening: " + list(other, limit, "") + ".\n")
	}
	b.WriteString("- A Service or port is reachable in the user's browser at the AOS address under /port/<n>/ (n is the port).\n")
	b.WriteString("- ~/Downloads holds downloads.\n")
	b.WriteString("- The whole filesystem persists across restarts; make software and system changes through install_package and run_privileged_command so AOS can undo and replay them.\n")
	return b.String()
}

// Context is the message from AOS that starts every Agent's conversation:
// the user's Memory, then the Machine Profile (PLAN.md §8.2).
func Context(memory []string, m Machine) string {
	var b strings.Builder
	if len(memory) > 0 {
		b.WriteString("# Memory\nThe user asked AOS to remember:\n")
		for _, e := range memory {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(e))
		}
		b.WriteString("\n")
	}
	b.WriteString(Profile(m))
	return b.String()
}

func list(items []string, limit int, empty string) string {
	switch {
	case len(items) == 0:
		return empty
	case limit == 0:
		return fmt.Sprintf("%d", len(items))
	case len(items) > limit:
		return strings.Join(items[:limit], ", ") + fmt.Sprintf(" and %d more", len(items)-limit)
	}
	return strings.Join(items, ", ")
}

func joinInts(ns []int) string {
	s := make([]string, len(ns))
	for i, n := range ns {
		s[i] = fmt.Sprint(n)
	}
	return strings.Join(s, ", ")
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
