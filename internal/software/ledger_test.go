package software

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aman123at/agentic-os/internal/store"
)

func newLedger(t *testing.T) *Ledger {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &Ledger{DB: db}
}

func pkg(version string) string { return `{"version":"` + version + `"}` }

func TestRestoreUndoesWhatCameAfterACheckpointAndReplayRedoesTheEnd(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	install := &Op{Action: "install", Summary: "Install nginx (apt)", Changes: []Change{
		{Kind: KindPackage, Manager: "apt", Name: "nginx:arm64", After: pkg("1.24.0")},
		{Kind: KindPackage, Manager: "apt", Name: "libc6:arm64", Before: pkg("2.39-0"), After: pkg("2.39-1")},
		{Kind: KindFile, Name: "/etc/nginx", After: `{"type":"dir","mode":493,"uid":0,"gid":0}`},
	}}
	if err := l.Append(ctx, install); err != nil {
		t.Fatal(err)
	}
	cp, err := l.CreateCheckpoint(ctx, "", "t_1", true)
	if err != nil || cp.LedgerId != install.ID || !strings.HasPrefix(cp.Name, "Checkpoint ") {
		t.Fatalf("checkpoint %+v, %v", cp, err)
	}
	if err := l.Append(ctx, &Op{Action: "install", Changes: []Change{
		{Kind: KindPackage, Manager: "apt", Name: "libc6:arm64", Before: pkg("2.39-1"), After: pkg("2.39-2")},
		{Kind: KindPackage, Manager: "pipx", Name: "httpie", After: pkg("3.2.4")},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(ctx, &Op{Action: "remove", Changes: []Change{
		{Kind: KindPackage, Manager: "apt", Name: "nginx:arm64", Before: pkg("1.24.0")},
	}}); err != nil {
		t.Fatal(err)
	}

	after, err := l.After(ctx, cp.LedgerId)
	if err != nil || len(after) != 2 {
		t.Fatalf("after the checkpoint: %v, %v", after, err)
	}
	undo := Undo(after)
	want := map[Key]string{
		{KindPackage, "apt", "libc6:arm64"}: pkg("2.39-1"), // the version at the Checkpoint
		{KindPackage, "pipx", "httpie"}:     "",            // installed later: removed
		{KindPackage, "apt", "nginx:arm64"}: pkg("1.24.0"), // removed later: back
	}
	if len(undo) != len(want) {
		t.Errorf("undo %v", undo)
	}
	for k, v := range want {
		if undo[k] != v {
			t.Errorf("undo %v = %q, want %q", k, undo[k], v)
		}
	}
	all, _ := l.After(ctx, 0)
	final := Final(all)
	if final[Key{KindPackage, "apt", "nginx:arm64"}] != "" || final[Key{KindPackage, "apt", "libc6:arm64"}] != pkg("2.39-2") || final[Key{KindFile, "", "/etc/nginx"}] == "" {
		t.Errorf("final %v", final)
	}

	pkgs, err := l.Packages(ctx)
	if err != nil || len(pkgs) != 2 || pkgs[0].Name != "libc6:arm64" || pkgs[0].Version != "2.39-2" || pkgs[1].Manager != "pipx" {
		t.Errorf("packages %v, %v", pkgs, err)
	}
	newest, _ := l.List(ctx, 2, 0)
	if len(newest) != 2 || newest[0].Action != "remove" || len(newest[1].Changes) != 2 {
		t.Errorf("newest %+v", newest)
	}
	if p := newest[0].Proto(); p.Changes[0].Before != "1.24.0" || p.Changes[0].After != "" {
		t.Errorf("proto %+v", p)
	}
}

func TestCheckpointsAreFoundByAnUniquePrefix(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	a, _ := l.CreateCheckpoint(ctx, "Before nginx", "", false)
	if got, err := l.Checkpoint(ctx, a.Id[:5]); err != nil || got.Id != a.Id || got.Name != "Before nginx" {
		t.Errorf("by prefix: %+v, %v", got, err)
	}
	if _, err := l.Checkpoint(ctx, "c_nope"); !errors.Is(err, ErrNoCheckpoint) {
		t.Errorf("unknown: %v", err)
	}
	if _, err := l.Checkpoint(ctx, ""); !errors.Is(err, ErrNoCheckpoint) {
		t.Errorf("empty: %v", err)
	}
	if _, err := l.CreateCheckpoint(ctx, "b", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Checkpoint(ctx, "c_"); err == nil || !strings.Contains(err.Error(), "matches 2") {
		t.Errorf("ambiguous: %v", err)
	}
	list, _ := l.Checkpoints(ctx)
	if len(list) != 2 {
		t.Errorf("list %v", list)
	}
}
