package profile

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/store"
)

func TestTheContextGivesMemoryThenTheMachineProfile(t *testing.T) {
	m := Machine{OS: "Ubuntu 24.04.3 LTS", Arch: "arm64", Mode: "cli", Landlock: true,
		Software:  []Software{{Manager: "apt", Name: "nginx", Version: "1.24.0-2ubuntu7"}},
		Services:  []Service{{Name: "site", State: "running", Ports: []int{8081}}},
		Listeners: []Listener{{Port: 8081, Process: "nginx", Service: "site"}, {Port: 3000, Process: "node"}},
		Replaying: true}
	got := Context([]string{"I prefer tabs.", " Answer in French. "}, m)
	for _, want := range []string{
		"# Memory\nThe user asked AOS to remember:\n- I prefer tabs.\n- Answer in French.\n\n# Machine Profile\n",
		"Ubuntu 24.04.3 LTS on arm64, cli Mode; Landlock confines Agents.",
		"Replay is reinstalling software",
		"Installed through AOS: nginx 1.24.0-2ubuntu7 (apt).",
		"Services: site (running, port 8081).",
		"Also listening: 3000 (node).",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the context lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "8081 (nginx)") {
		t.Errorf("a Service's port is listed twice:\n%s", got)
	}
	// M6.12: the Profile names /port/<n>/ and the persistent filesystem, and no
	// longer the retired Shared Folder, the .localhost form or a fresh system.
	// M6.15: the Profile says the base Machine is a minimal toolchain, so the
	// Agent installs what a Task needs rather than assuming it is present.
	for _, want := range []string{"/port/<n>/", "filesystem persists across restarts", "starts with a minimal toolchain"} {
		if !strings.Contains(got, want) {
			t.Errorf("the Profile lacks %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{".localhost", "~/Shared", "fresh system", "survive a restart"} {
		if strings.Contains(got, gone) {
			t.Errorf("the Profile still carries the retired phrase %q:\n%s", gone, got)
		}
	}
	if empty := Context(nil, Machine{}); strings.Contains(empty, "Memory") || !strings.Contains(empty, "Installed through AOS: nothing yet.") {
		t.Errorf("without Memory or software:\n%s", empty)
	}
}

func TestTheMachineProfileStaysUnder1KB(t *testing.T) {
	m := Machine{OS: "Ubuntu 24.04.3 LTS", Arch: "arm64", Mode: "ui", Landlock: true}
	for i := 0; i < 200; i++ {
		m.Software = append(m.Software, Software{Manager: "apt", Name: fmt.Sprintf("package-with-a-long-name-%d", i), Version: "1.2.3-4ubuntu5"})
		m.Services = append(m.Services, Service{Name: fmt.Sprintf("service-%d", i), State: "running", Ports: []int{8000 + i}})
		m.Listeners = append(m.Listeners, Listener{Port: 9000 + i, Process: "python3"})
	}
	p := Profile(m)
	if len(p) >= maxProfile || !strings.Contains(p, "(apt) and 198 more.") || !strings.Contains(p, "port 8001) and 198 more.") {
		t.Errorf("profile of %d bytes:\n%s", len(p), p)
	}
}

func TestMemoryProposalsWaitForTheUser(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var notified []*aosv1.Notification
	s := &Memories{DB: db, Notify: func(_ context.Context, n *aosv1.Notification) { notified = append(notified, n) }}
	ctx := context.Background()

	proposal, err := s.Propose(ctx, "t_1", "The user deploys with rsync.")
	if err != nil {
		t.Fatal(err)
	}
	if len(notified) != 1 || notified[0].MemoryId != proposal.Id || notified[0].Body != "The user deploys with rsync." || notified[0].TaskId != "t_1" {
		t.Errorf("notifications %+v", notified)
	}
	written, _ := s.Add(ctx, "", "I prefer tabs.")
	if accepted, _ := s.Accepted(ctx); len(accepted) != 1 || accepted[0] != "I prefer tabs." {
		t.Fatalf("accepted before the proposal was: %v", accepted)
	}
	if m, err := s.Accept(ctx, proposal.Id); err != nil || m.Status != Accepted {
		t.Fatalf("accept: %+v %v", m, err)
	}
	if accepted, _ := s.Accepted(ctx); len(accepted) != 2 {
		t.Errorf("accepted %v", accepted)
	}
	if err := s.Forget(ctx, written.Id); err != nil {
		t.Fatal(err)
	}
	if err := s.Forget(ctx, written.Id); !errors.Is(err, ErrNoMemory) {
		t.Errorf("forgetting twice: %v", err)
	}
	if _, err := s.Add(ctx, "", "  "); err == nil {
		t.Error("an empty entry was saved")
	}
	list, _ := s.List(ctx)
	if len(list) != 1 || list[0].Id != proposal.Id {
		t.Errorf("list %v", list)
	}
}
