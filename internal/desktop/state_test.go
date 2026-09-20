package desktop

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aman123at/agentic-os/internal/store"
)

func TestAppearanceKeepsTheLookAndDropsTheWindows(t *testing.T) {
	// Seeding a fresh Root Realm copies the appearance from Standard but never the
	// window layout or the open chats it names (M7.10).
	in := `{"theme":"dark","wallpaper":"aurora","glass":true,"shortcuts":{"spotlight":"cmd+k"},"windows":[{"id":"win-1","appId":"agent"}]}`
	got := Appearance(in)
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("Appearance produced invalid JSON %q: %v", got, err)
	}
	if _, ok := m["windows"]; ok {
		t.Errorf("Appearance kept the windows: %q", got)
	}
	for _, k := range []string{"theme", "wallpaper", "glass", "shortcuts"} {
		if _, ok := m[k]; !ok {
			t.Errorf("Appearance dropped %q: %q", k, got)
		}
	}

	// Nothing to copy: empty in, empty out; likewise a blob that is only windows,
	// and a blob that does not parse.
	for _, in := range []string{"", `{"windows":[{"id":"w"}]}`, "not json"} {
		if got := Appearance(in); got != "" {
			t.Errorf("Appearance(%q) = %q, want empty", in, got)
		}
	}
}

func TestStateRoundTrip(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &State{DB: db}
	ctx := context.Background()

	// Nothing saved yet.
	if got, err := s.Get(ctx); err != nil || got != "" {
		t.Fatalf("empty Get = %q, %v", got, err)
	}

	// Save, then read it back.
	if err := s.Save(ctx, `{"windows":[]}`); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(ctx); err != nil || got != `{"windows":[]}` {
		t.Fatalf("Get = %q, %v", got, err)
	}

	// A second save replaces the first (single row).
	if err := s.Save(ctx, `{"theme":"dark"}`); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx); got != `{"theme":"dark"}` {
		t.Fatalf("after replace Get = %q", got)
	}

	// Oversized layouts are refused.
	if err := s.Save(ctx, strings.Repeat("x", MaxState+1)); err == nil {
		t.Fatal("expected an error saving an oversized layout")
	}
}
