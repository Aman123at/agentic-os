package daemon

import (
	"testing"

	"github.com/Aman123at/agentic-os/internal/realm"
)

// TestDirsForKeepsTheRealmsApart checks the M7.2 wiring: everything but the
// account uses the Realm's own database, the account and the locks always use
// the Standard database, and each Realm's Other points at the opposite one so
// the Cost Limit can sum both.
func TestDirsForKeepsTheRealmsApart(t *testing.T) {
	base := "/var/lib/aos"

	std := dirsFor(base, realm.Standard)
	if std.DB != base+"/aos.db" {
		t.Errorf("Standard DB = %q", std.DB)
	}
	if std.Account != base+"/aos.db" {
		t.Errorf("Standard account = %q", std.Account)
	}
	if std.DB != std.Account {
		t.Error("Standard should use one file for both its own state and the account")
	}
	if std.Outputs != base+"/outputs" {
		t.Errorf("Standard outputs = %q", std.Outputs)
	}
	if std.Other != base+"/root/aos.db" {
		t.Errorf("Standard other = %q, want the Root database", std.Other)
	}

	root := dirsFor(base, realm.Root)
	if root.DB != base+"/root/aos.db" {
		t.Errorf("Root DB = %q", root.DB)
	}
	if root.Account != base+"/aos.db" {
		t.Errorf("Root account = %q, want the Standard database", root.Account)
	}
	if root.DB == root.Account {
		t.Error("Root must not write its history into the Standard database")
	}
	if root.Outputs != base+"/root/outputs" {
		t.Errorf("Root outputs = %q", root.Outputs)
	}
	if root.Other != base+"/aos.db" {
		t.Errorf("Root other = %q, want the Standard database", root.Other)
	}
}
