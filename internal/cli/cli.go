// Package cli is the aos CLI inside the Machine (PLAN.md §4.2). It talks to aosd
// over its Unix socket. Run as rm, it is the Agent Session's rm shim (§7.8).
package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/amantiwari/agentic-os/internal/files"
	"github.com/amantiwari/agentic-os/internal/sandbox"
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
	err := rootCmd().Execute()
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

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "aos",
		Short:         "Agentic OS: give Agents Tasks on this Machine",
		Long:          "Without a command, aos starts an interactive chat: each message is a Task, and Approvals are asked in the terminal.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE:          func(cmd *cobra.Command, _ []string) error { return chat(cmd.Context()) },
	}
	root.AddCommand(runCmd(), tasksCmd(), showCmd(), cancelCmd(), stopCmd(), approveCmd(true), approveCmd(false),
		attachCmd(), trashCmd(), protectCmd(true), protectCmd(false), auditCmd(), desktopURLCmd(), doctorCmd(), laterCmd("follow-up"), laterCmd("resume"))
	return root
}

// laterCmd is a command of a later milestone.
func laterCmd(name string) *cobra.Command {
	return &cobra.Command{Use: name + " <id>", Short: "Arrives in milestone M2", Hidden: true,
		RunE: func(*cobra.Command, []string) error { return fmt.Errorf("aos %s arrives in milestone M2", name) }}
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
	ops := files.Ops{Home: home, Shared: sandbox.DefaultLayout().Shared, UID: os.Getuid()}
	for _, a := range args {
		if a == "--help" || a == "--version" {
			// Unusual requests go to the real rm.
			return execRealRm(args)
		}
	}
	return files.Rm(ops, cwd, args, os.Stdout, os.Stderr)
}
