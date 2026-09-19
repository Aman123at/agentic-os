package daemon

import "github.com/Aman123at/agentic-os/internal/realm"

// realmDirs is where a Realm keeps its files under a base directory (StateDir in
// a running aosd). One database file per Realm keeps the two histories apart
// with no query that can leak across them (M7.2): a forgotten `WHERE realm = ?`
// cannot exist when there is no realm column.
type realmDirs struct {
	// DB is the Realm's database. The Daemon opens it for everything except the
	// account: tasks, task_steps, approvals, grants, audit_log, memories,
	// notifications, usage, ledger_*, checkpoints, services, desktop_state.
	DB string
	// Account is the Standard database, which always holds the sign-in (users,
	// refresh_tokens) and the shared protected_paths, so one sign-in and one set
	// of locks survive a switch between Realms.
	Account string
	// Outputs is where read_output blobs for this Realm are kept.
	Outputs string
	// Other is the opposite Realm's database. The Cost Limit adds its usage total
	// in (read-only) so Root Mode cannot be used to dodge a limit — only ever a
	// total, never a Task. In Standard it points at the Root database, which may
	// not exist yet (no Root start has happened); in Root it is the Account
	// database, which always exists.
	Other string
}

// dirsFor returns the file layout for Realm r under base. Standard keeps
// base/aos.db and base/outputs; Root gets base/root/aos.db and base/root/outputs
// (root:root 0700), created on first Root start with the same embedded
// migrations (M7.2).
func dirsFor(base string, r realm.Realm) realmDirs {
	account := base + "/aos.db"
	if r.IsRoot() {
		root := base + "/root"
		return realmDirs{DB: root + "/aos.db", Account: account, Outputs: root + "/outputs", Other: account}
	}
	return realmDirs{DB: account, Account: account, Outputs: base + "/outputs", Other: base + "/root/aos.db"}
}
