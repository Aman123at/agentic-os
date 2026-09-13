// Command aos is the CLI inside the Machine (PLAN.md §4.2). M0 provides only
// `aos doctor`; Tasks arrive in M1.
package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/amantiwari/agentic-os/internal/sandbox"
	"github.com/amantiwari/agentic-os/tools/hostcheck"
)

func main() {
	sandbox.RunHelperIfRequested()
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{Use: "aos", Short: "Agentic OS command line", SilenceUsage: true}
	root.AddCommand(doctorCmd())
	return root
}

func doctorCmd() *cobra.Command {
	var hostCheck bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Show Mode, Landlock status and API key presence; --host-check runs the Host acceptance report",
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Mode:      %s (image built for %s)\n", envOr("AOS_MODE", "unset"), envOr("AOS_IMAGE_MODE", "unknown"))
			fmt.Fprintf(w, "Platform:  %s/%s\n", runtime.GOOS, runtime.GOARCH)
			if abi := sandbox.ABI(); abi > 0 {
				fmt.Fprintf(w, "Landlock:  ABI %d\n", abi)
			} else {
				fmt.Fprintln(w, "Landlock:  unavailable (Agents are confined by policy checks only)")
			}
			fmt.Fprintf(w, "API key:   %s\n", keyStatus())
			if !hostCheck {
				return nil
			}
			fmt.Fprintln(w, "\nHost check")
			report, err := hostcheck.Run(cmd.Context(), hostcheck.DefaultOptions())
			if err != nil {
				return err
			}
			report.Write(w)
			if report.Failed() {
				return fmt.Errorf("host check failed")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&hostCheck, "host-check", false, "run the M0 prototype checks and print a pass/fail report")
	return cmd
}

func keyStatus() string {
	fi, err := os.Stat("/run/secrets/openai_api_key")
	switch {
	case err != nil:
		return "not provided"
	case fi.Size() == 0:
		return "empty (OPENAI_API_KEY not set on the Host)"
	default:
		return "present"
	}
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
