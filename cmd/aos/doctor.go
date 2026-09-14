package main

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
	"github.com/amantiwari/agentic-os/internal/sandbox"
	"github.com/amantiwari/agentic-os/tools/hostcheck"
)

func doctorCmd() *cobra.Command {
	var hostCheck bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Show Mode, Landlock status and API key presence; --host-check runs the Host acceptance report",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			info, err := newClient().system.Info(ctx, connect.NewRequest(&aosv1.InfoRequest{}))
			cancel()
			if err != nil {
				fmt.Fprintf(w, "aosd:      %v\n", explain(err))
				fmt.Fprintf(w, "Mode:      %s (image built for %s)\n", envOr("AOS_MODE", "unset"), envOr("AOS_IMAGE_MODE", "unknown"))
			} else {
				i := info.Msg
				fmt.Fprintf(w, "aosd:      running, version %s\n", i.Version)
				fmt.Fprintf(w, "Mode:      %s (image built for %s)\n", i.Mode, envOr("AOS_IMAGE_MODE", "unknown"))
				fmt.Fprintf(w, "Model:     %s\n", i.Model)
				fmt.Fprintf(w, "Autonomy:  %s, up to %d Tasks at once\n", autonomyName(i.Autonomy), i.MaxTasks)
				fmt.Fprintf(w, "API key:   %s\n", map[string]string{"present": "present", "empty": "empty (OPENAI_API_KEY is empty on the Host)", "missing": "not provided"}[i.ApiKey])
			}
			fmt.Fprintf(w, "Platform:  %s/%s\n", runtime.GOOS, runtime.GOARCH)
			if abi := sandbox.ABI(); abi > 0 {
				fmt.Fprintf(w, "Landlock:  ABI %d\n", abi)
			} else {
				fmt.Fprintln(w, "Landlock:  unavailable (Agents are confined by policy checks only)")
			}
			if !hostCheck {
				return nil
			}
			fmt.Fprintln(w, "\nHost check")
			opts := hostcheck.DefaultOptions()
			opts.RequireAosd = true
			report, err := hostcheck.Run(cmd.Context(), opts)
			if err != nil {
				return err
			}
			report.Write(w)
			if report.Failed() {
				return exitError{1}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&hostCheck, "host-check", false, "run the M0 prototype checks and print a pass/fail report")
	return cmd
}

func autonomyName(a aosv1.Autonomy) string {
	switch a {
	case aosv1.Autonomy_AUTONOMY_AUTO:
		return "auto"
	case aosv1.Autonomy_AUTONOMY_CONFIRM_ALL:
		return "confirm-all"
	}
	return "confirm-risky"
}
