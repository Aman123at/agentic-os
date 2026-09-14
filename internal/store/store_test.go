package store

import (
	"context"
	"database/sql"
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
	if err := db.Read().QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 3 {
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
