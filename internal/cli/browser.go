package cli

import (
	"fmt"
	"os"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/browser"
)

// browserCmd installs or removes the Browser app's headless shell (PLAN.md
// M6.11, ADR-0008). The image no longer bakes Chromium in: `sudo aos browser
// install` fetches Chrome-for-Testing on demand, and `aos browser remove`
// reverses it. Both write the include_browser key themselves and restart the
// Daemon so the Browser app appears or disappears. They need root — the shell
// lands in /opt and the restart is a system unit — and stay out of the Install
// Ledger, so a Restore never removes the browser's libraries out from under it.
func browserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "browser",
		Short: "Install or remove the Browser app (Chromium's headless shell)",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(browserInstallCmd(), browserRemoveCmd())
	return cmd
}

func browserInstallCmd() *cobra.Command {
	var noDeps bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Download and install the Browser (run with sudo)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireRoot("aos browser install"); err != nil {
				return err
			}
			in := &browser.Installer{Out: cmd.OutOrStdout(), InstallDeps: !noDeps}
			if err := in.Install(cmd.Context()); err != nil {
				return err
			}
			return applyBrowser(cmd, "true", "The Browser app is available in the Desktop.")
		},
	}
	cmd.Flags().BoolVar(&noDeps, "no-deps", false, "don't apt-get the system libraries the browser needs; just report any that are missing")
	return cmd
}

func browserRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove",
		Short: "Remove the installed Browser (run with sudo)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireRoot("aos browser remove"); err != nil {
				return err
			}
			in := &browser.Installer{Out: cmd.OutOrStdout()}
			if err := in.Remove(); err != nil {
				return err
			}
			return applyBrowser(cmd, "false", "The Browser app is off.")
		},
	}
}

// applyBrowser writes include_browser through aosd (so config.yml keeps its
// comments and order) and restarts the unit, since include_browser is a
// startup-only key. It mirrors applyMode.
func applyBrowser(cmd *cobra.Command, value, after string) error {
	if _, err := newClient().settings.Update(cmd.Context(),
		connect.NewRequest(&aosv1.UpdateSettingRequest{Key: "include_browser", Value: value})); err != nil {
		return explain(err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Restarting the Daemon to apply the change ...")
	if err := systemctl("restart", "aos"); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), after)
	return nil
}

// requireRoot refuses a command that must run as root before it does any work,
// so the failure names sudo rather than surfacing a bare permission error.
func requireRoot(what string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("%s needs root; run it with sudo", what)
	}
	return nil
}
