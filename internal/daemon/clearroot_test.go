package daemon

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/Aman123at/agentic-os/internal/realm"
	"github.com/Aman123at/agentic-os/internal/store"
)

// TestClearRootHistoryRemovesTheRootSubtreeAndItRecreatesEmpty is the M7.12 test:
// clearing Root Mode history deletes the Root Realm's database and its outputs,
// and the next Root start — the same MkdirAll and store.Open init() runs — brings
// them back empty. It exercises the exact removal the daemon wires
// (os.RemoveAll(rootStateDir(base))), independent of the Linux-only build.
func TestClearRootHistoryRemovesTheRootSubtreeAndItRecreatesEmpty(t *testing.T) {
	base := t.TempDir()
	dirs := dirsFor(base, realm.Root)

	// A Root start with history: the database holds a Task and outputs holds a blob.
	if err := os.MkdirAll(dirs.Outputs, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(dirs.DB)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Write(context.Background(), func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT INTO tasks (id, title, prompt, state, autonomy, interactive, created_at, updated_at)
			VALUES ('t_1', 'old', 'p', 1, 1, 0, 0, 0)`)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	blob := filepath.Join(dirs.Outputs, "cmd-1.txt")
	if err := os.WriteFile(blob, []byte("secret output"), 0o600); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Clear: the exact removal the daemon performs (M7.12).
	if err := os.RemoveAll(rootStateDir(base)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rootStateDir(base)); !os.IsNotExist(err) {
		t.Fatalf("the Root subtree survives the clear: %v", err)
	}

	// The next Root start recreates the layout, as init() does.
	if err := os.MkdirAll(dirs.Outputs, 0o700); err != nil {
		t.Fatal(err)
	}
	db2, err := store.Open(dirs.DB)
	if err != nil {
		t.Fatalf("reopening the Root database after a clear: %v", err)
	}
	defer db2.Close()

	var tasks int
	if err := db2.Read().QueryRow(`SELECT count(*) FROM tasks`).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if tasks != 0 {
		t.Errorf("the recreated Root database has %d Task(s), want an empty history", tasks)
	}
	entries, err := os.ReadDir(dirs.Outputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the recreated outputs directory has %d ent(s), want it empty", len(entries))
	}
}
