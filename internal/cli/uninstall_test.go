package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// mkFile creates root+p (with parents) so a temp root can stand in for the
// native install's layout.
func mkFile(t *testing.T, root, p string) string {
	t.Helper()
	full := filepath.Join(root, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

// seedInstall lays down the files install.sh creates, under a temp root, and
// returns an uninstall wired to that root with the system operations stubbed —
// so the test exercises file removal without touching the host's systemd or
// user database.
func seedInstall(t *testing.T, purge bool) (*uninstall, string) {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{
		uninstallUnit, uninstallAos, uninstallAosd,
		uninstallLib + "/agent-bin/rm",
		uninstallSudoers,
		uninstallConf + "/config.yml",
		uninstallState + "/aos.db",
		uninstallHome + "/notes.txt",
	} {
		mkFile(t, root, p)
	}
	u := &uninstall{
		out: &bytes.Buffer{}, purge: purge, root: root,
		stopService: func() error { return nil }, daemonReload: func() error { return nil }, delUser: func() {},
	}
	return u, root
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	return err == nil
}

// TestUninstallRemovesUnitAndBinaryKeepsData is the M6.19 acceptance: a plain
// uninstall takes the systemd unit and the binary and leaves /home/aos,
// /var/lib/aos and the config, so a reinstall finds its data.
func TestUninstallRemovesUnitAndBinaryKeepsData(t *testing.T) {
	u, root := seedInstall(t, false)
	if err := u.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, p := range []string{uninstallUnit, uninstallAos, uninstallAosd, uninstallLib} {
		if exists(t, filepath.Join(root, p)) {
			t.Errorf("%s should be gone after uninstall", p)
		}
	}
	for _, p := range []string{uninstallConf, uninstallState, uninstallHome} {
		if !exists(t, filepath.Join(root, p)) {
			t.Errorf("%s should be kept without --purge", p)
		}
	}
}

// TestUninstallPurgeRemovesEverything is the other half: --purge also deletes
// the account's sudoers grant, the config, the state and the home folder.
func TestUninstallPurgeRemovesEverything(t *testing.T) {
	u, root := seedInstall(t, true)
	called := false
	u.delUser = func() { called = true }
	if err := u.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, p := range []string{
		uninstallUnit, uninstallAos, uninstallAosd, uninstallLib,
		uninstallSudoers, uninstallConf, uninstallState, uninstallHome,
	} {
		if exists(t, filepath.Join(root, p)) {
			t.Errorf("%s should be gone after --purge", p)
		}
	}
	if !called {
		t.Error("--purge did not remove the aos user")
	}
}

// TestUninstallIsIdempotent: removing an already-absent install is not an error,
// so a re-run, or an uninstall after a partial install, still succeeds.
func TestUninstallIsIdempotent(t *testing.T) {
	u, _ := seedInstall(t, true)
	if err := u.run(); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := u.run(); err != nil {
		t.Fatalf("second run over an empty tree: %v", err)
	}
}
