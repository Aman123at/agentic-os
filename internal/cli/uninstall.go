package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

// The native install's paths, mirroring install.sh (ADR-0009, M6.17): uninstall
// is that script run backwards. The binary and the unit go on every uninstall;
// the account, its sudoers grant and the data go only with --purge, so a plain
// uninstall a reinstall can find its config, keys and Trash again.
const (
	uninstallUnit    = "/etc/systemd/system/aos.service"
	uninstallAos     = "/usr/local/bin/aos"
	uninstallAosd    = "/usr/local/bin/aosd"
	uninstallLib     = "/usr/local/lib/aos"
	uninstallSudoers = "/etc/sudoers.d/aos"
	uninstallConf    = "/etc/aos"
	uninstallState   = "/var/lib/aos"
	uninstallHome    = "/home/aos"
)

// uninstallCmd removes a native install: it stops the service, deletes its
// systemd unit and the binary, and — only with --purge — the aos account and
// all its data (M6.19). Like install.sh it is a root, systemd-only operation, so
// run it with sudo. Without --yes it prints exactly what it would remove and
// changes nothing, the way `DRY_RUN=1 sh install.sh` rehearses the install.
func uninstallCmd() *cobra.Command {
	var yes, purge bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the systemd unit and the binary (--purge also deletes the account and all data)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u := newUninstall(cmd.OutOrStdout(), purge)
			if !yes {
				u.plan()
				return nil
			}
			if os.Geteuid() != 0 {
				return fmt.Errorf("run as root: sudo aos uninstall")
			}
			return u.run()
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "actually remove things; without it, only print what would be removed")
	cmd.Flags().BoolVar(&purge, "purge", false, "also delete the aos account, /home/aos, /var/lib/aos and the config")
	return cmd
}

// uninstall removes a native install. The system operations (systemd, the user
// database) are fields so a test drives the file removal against a temp root
// without touching the host that runs it.
type uninstall struct {
	out   io.Writer
	purge bool
	root  string // "" is the real filesystem; a temp dir under test

	stopService  func() error // systemctl stop + disable aos
	daemonReload func() error // systemctl daemon-reload, after the unit is gone
	delUser      func()       // userdel aos (best-effort; --purge only)
}

// newUninstall wires the real system operations. A test builds an uninstall
// literal instead, with these stubbed and root set to a temp directory.
func newUninstall(out io.Writer, purge bool) *uninstall {
	return &uninstall{
		out: out, purge: purge,
		stopService: func() error {
			if _, err := exec.LookPath("systemctl"); err != nil {
				return fmt.Errorf("systemctl is not available: uninstall removes a systemd install (ADR-0009). Under Docker Compose, `docker compose down` and remove the image")
			}
			// Best-effort: the unit may already be stopped, or was never enabled.
			// stop drains running Tasks first (Type=notify), disable drops the
			// enable symlink so a reboot does not bring it back.
			_ = passthrough("systemctl", "stop", "aos")
			_ = passthrough("systemctl", "disable", "aos")
			return nil
		},
		daemonReload: func() error { return passthrough("systemctl", "daemon-reload") },
		delUser: func() {
			// The account may already be gone, or never have existed here; a
			// failure is a note, not a stop — the data is already removed.
			if err := passthrough("userdel", "aos"); err != nil {
				fmt.Fprintf(out, "  note     could not remove the aos user (userdel): %v\n", err)
			}
		},
	}
}

// plan prints what --yes would remove and changes nothing.
func (u *uninstall) plan() {
	fmt.Fprintln(u.out, "aos uninstall would remove:")
	for _, p := range []string{uninstallUnit, uninstallAos, uninstallAosd, uninstallLib} {
		fmt.Fprintf(u.out, "  %s\n", p)
	}
	if u.purge {
		fmt.Fprintln(u.out, "and, with --purge, the account and all data:")
		fmt.Fprintln(u.out, "  the aos user")
		for _, p := range []string{uninstallSudoers, uninstallConf, uninstallState, uninstallHome} {
			fmt.Fprintf(u.out, "  %s\n", p)
		}
	} else {
		fmt.Fprintf(u.out, "\nKeeping the account and data: %s, %s and %s (add --purge to remove them).\n",
			uninstallHome, uninstallState, uninstallConf)
	}
	fmt.Fprintln(u.out, "\nRe-run with --yes to do it.")
}

// run performs the uninstall, in the reverse order of install.sh: the service
// first, then the unit and binary, then — with --purge — the account and data.
func (u *uninstall) run() error {
	if err := u.stopService(); err != nil {
		return err
	}
	if err := u.remove(uninstallUnit); err != nil {
		return err
	}
	if err := u.daemonReload(); err != nil {
		return err
	}
	// The binary and its two links (aos, and the Agent's rm shim under lib).
	for _, p := range []string{uninstallAos, uninstallAosd, uninstallLib} {
		if err := u.remove(p); err != nil {
			return err
		}
	}
	if !u.purge {
		fmt.Fprintf(u.out, "\nKept the account and data: %s, %s and %s.\nRun `aos uninstall --purge --yes` to remove them too.\n",
			uninstallHome, uninstallState, uninstallConf)
		return nil
	}
	// --purge: the account, its passwordless-sudo grant, and every folder install
	// created for it.
	u.delUser()
	for _, p := range []string{uninstallSudoers, uninstallConf, uninstallState, uninstallHome} {
		if err := u.remove(p); err != nil {
			return err
		}
	}
	fmt.Fprintln(u.out, "\nPurged: the aos account and all its data are gone.")
	return nil
}

// remove deletes one path (under root, for tests), reporting whether it was
// there. A path already absent is not an error — an uninstall is idempotent and
// may follow a partial install.
func (u *uninstall) remove(p string) error {
	full := filepath.Join(u.root, p)
	if _, err := os.Lstat(full); errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(u.out, "  absent   %s\n", p)
		return nil
	}
	if err := os.RemoveAll(full); err != nil {
		return fmt.Errorf("removing %s: %w", p, err)
	}
	fmt.Fprintf(u.out, "  removed  %s\n", p)
	return nil
}
