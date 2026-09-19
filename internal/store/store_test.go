package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationsApplyOnceAndTheLedgerIsAppendOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aos.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	// Opening again finds every migration applied.
	if db, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.Read().QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 6 {
		t.Fatalf("user_version %d, %v", version, err)
	}

	ctx := context.Background()
	err = db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO ledger_ops (time, action, summary) VALUES (1, 'install', 'Install nginx (apt)')`)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		_, err = tx.Exec(`INSERT INTO ledger_entries (op_id, kind, manager, name, after_state) VALUES (?, 'package', 'apt', 'nginx:arm64', '{}')`, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`UPDATE ledger_ops SET summary = 'x'`,
		`DELETE FROM ledger_ops`,
		`UPDATE ledger_entries SET after_state = ''`,
		`DELETE FROM ledger_entries`,
	} {
		err := db.Write(ctx, func(tx *sql.Tx) error { _, err := tx.Exec(stmt); return err })
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: %v", stmt, err)
		}
	}
}

// TestM6AuthTablesCascade checks 0005_m6.sql: the auth tables exist and a
// refresh token is tied to its user, so removing the user removes its tokens.
func TestM6AuthTablesCascade(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	err = db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO users (username, pw_hash, created_at, updated_at) VALUES ('aman', 'pbkdf2-sha256$1$AA$AA', 1, 1)`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO refresh_tokens (id, family, username, created_at, expires_at) VALUES ('id1', 'fam1', 'aman', 1, 2)`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// A token for a user who does not exist is refused by the foreign key.
	err = db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO refresh_tokens (id, family, username, created_at, expires_at) VALUES ('id2', 'fam2', 'ghost', 1, 2)`)
		return err
	})
	if err == nil {
		t.Error("a refresh token referencing an unknown user was allowed")
	}
	// Deleting the user cascades to its tokens.
	if err := db.Write(ctx, func(tx *sql.Tx) error { _, err := tx.Exec(`DELETE FROM users WHERE username = 'aman'`); return err }); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Read().QueryRow(`SELECT count(*) FROM refresh_tokens`).Scan(&n); err != nil || n != 0 {
		t.Errorf("refresh_tokens after deleting the user: %d rows (%v), want 0", n, err)
	}
}

// TestPerRealmDatabasesAreIndependent stands in for the M7.2 isolation: the Root
// Realm gets its own database file, which migrates from empty just like the
// Standard one, and no query can reach across the two — a row written to one is
// absent from the other, because there is no shared realm column to forget.
func TestPerRealmDatabasesAreIndependent(t *testing.T) {
	dir := t.TempDir()
	standard, err := Open(filepath.Join(dir, "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer standard.Close()
	// The Root database is a fresh file under its own directory (the Daemon
	// creates it 0700 on a first Root start); opening it applies every migration
	// from empty, exactly as on that first start.
	if err := os.MkdirAll(filepath.Join(dir, "root"), 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := Open(filepath.Join(dir, "root", "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var version int
	if err := root.Read().QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 6 {
		t.Fatalf("Root database user_version %d, %v; want every migration applied", version, err)
	}

	ctx := context.Background()
	if err := standard.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO usage (day, model, input_tokens, cached_input_tokens, output_tokens, reasoning_tokens, cost_usd, cost_known) VALUES ('2026-09-20', 'gpt-big', 1, 0, 1, 0, 1.0, 1)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := root.Read().QueryRow(`SELECT count(*) FROM usage`).Scan(&n); err != nil || n != 0 {
		t.Errorf("Root usage after a Standard write: %d rows (%v), want 0 — the Realms must not share history", n, err)
	}
}
