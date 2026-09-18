package desktop

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aman123at/agentic-os/internal/store"
)

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
