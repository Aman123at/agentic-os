// Package cli is the aos CLI inside the Machine (PLAN.md §4.2). It talks to aosd
// over its Unix socket. Run as rm, it is the Agent Session's rm shim (§7.8).
package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Aman123at/agentic-os/internal/daemon"
	"github.com/Aman123at/agentic-os/internal/files"
)

// exitError carries a process exit code out of a command.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

// Main runs aos with os.Args and returns the exit code. The caller must have
// called sandbox.RunHelperIfRequested first.
func Main() int {
	if filepath.Base(os.Args[0]) == "rm" {
		return rmShim(os.Args[1:])
	}
	err := Root().Execute()
	var exit exitError
	switch {
	case errors.As(err, &exit):
		return exit.code
	case err != nil:
		fmt.Fprintln(os.Stderr, "aos:", err)
		return 1
	}
	return 0
}

// Root is the aos command tree. It is exported so tools/docsgen can walk it to
// generate the documentation site's command reference.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "aos",
		Short:         "Agentic OS: give Agents Tasks on this Machine",
		Long:          "Without a command, aos starts an interactive chat: each message is a Task, and Approvals are asked in the terminal.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		// `aos --version` prints the build's stamped Version — on a tagged
		// release the tag itself (M6.18), so the binary and the tag agree.
		Version: daemon.Version,
		RunE:    func(cmd *cobra.Command, _ []string) error { return chat(cmd.Context()) },
	}
	root.SetVersionTemplate("aos {{.Version}}\n")
	root.AddCommand(runCmd(), tasksCmd(), showCmd(), followUpCmd(), resumeCmd(), replyCmd(), cancelCmd(), stopCmd(), approveCmd(true), approveCmd(false),
		attachCmd(), trashCmd(), protectCmd(true), protectCmd(false), softwareCmd(), checkpointCmd(), serviceCmd(), memoryCmd(),
		auditCmd(), doctorCmd(), configCmd(), daemonCmd(), statusCmd(), modeCmd(), rootCmd(), browserCmd(), uninstallCmd())
	return root
}

// rmShim moves rm's operands to the Trash (PLAN.md §7.8).
func rmShim(args []string) int {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/home/aos"
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = home
	}
	ops := files.Ops{Home: home, UID: os.Getuid()}
	for _, a := range args {
		if a == "--help" || a == "--version" {
			// Unusual requests go to the real rm.
			return execRealRm(args)
		}
	}
	return files.Rm(ops, cwd, args, os.Stdout, os.Stderr)
}
