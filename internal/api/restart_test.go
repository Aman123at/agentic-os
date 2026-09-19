package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Aman123at/agentic-os/internal/audit"
	"github.com/Aman123at/agentic-os/internal/store"
)

// TestRestartRepliesAndAuditsThenTriggersTheRestart: the Restart RPC (M7.3)
// invokes the restarter, records the request in the Audit Log, and returns
// success; the actual teardown is out of band, so the reply reaches the caller.
func TestRestartRepliesAndAuditsThenTriggersTheRestart(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	log := &audit.Log{DB: db}
	auth, _ := newAuth(t)
	restarted := false
	h := auth.TCP((&Server{Auth: auth, Audit: log, Restart: func() error { restarted = true; return nil }}).Handler())

	if rec := call(t, h, "/aos.v1.SystemService/Restart", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("Restart: %d %s", rec.Code, rec.Body)
	}
	if !restarted {
		t.Error("Restart did not trigger the restarter")
	}
	entries, err := log.List(ctx, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Tool != "restart" || entries[0].ResultSummary != "restarting" {
		t.Fatalf("audit %+v, want one restart entry recorded as restarting", entries)
	}
}

// TestRestartReportsAFailedRestarterAndStaysUp: when the restarter fails, the
// RPC returns the error (so aosd stays up) and the failure is audited.
func TestRestartReportsAFailedRestarterAndStaysUp(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	log := &audit.Log{DB: db}
	auth, _ := newAuth(t)
	h := auth.TCP((&Server{Auth: auth, Audit: log, Restart: func() error { return errors.New("no such unit") }}).Handler())

	if rec := call(t, h, "/aos.v1.SystemService/Restart", `{}`); rec.Code == http.StatusOK {
		t.Fatalf("Restart with a failing restarter returned %d, want an error", rec.Code)
	}
	entries, err := log.List(ctx, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ResultSummary != "no such unit" {
		t.Fatalf("audit %+v, want the failure recorded", entries)
	}
}

// TestRestartIsUnavailableWithoutARestarter: a foreground run wires no
// restarter, so the RPC answers Unavailable rather than pretending to restart.
func TestRestartIsUnavailableWithoutARestarter(t *testing.T) {
	auth, _ := newAuth(t)
	h := auth.TCP((&Server{Auth: auth}).Handler())
	if rec := call(t, h, "/aos.v1.SystemService/Restart", `{}`); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("Restart with no restarter: %d %s, want 503", rec.Code, rec.Body)
	}
}
