package cli

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/sandbox"
	"github.com/Aman123at/agentic-os/tools/hostcheck"
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
				fmt.Fprintf(w, "API key:   %s\n", keyText(i))
				fmt.Fprintf(w, "Retries:   %d (AOS_MAX_RETRIES)\n", i.MaxRetries)
				if t := i.Today; t != nil {
					fmt.Fprintf(w, "Today:     %d input tokens (%d cached), %d output tokens, %s\n", t.InputTokens, t.CachedInputTokens, t.OutputTokens, costText(t))
				}
				fmt.Fprintf(w, "Limits:    per Task %s, per day %s\n", limitText(i.TaskCostLimitUsd), limitText(i.DailyCostLimitUsd))
				if !i.PricesKnown {
					fmt.Fprintf(w, "Prices:    none for %s in /var/lib/aos/prices.yaml: costs are unknown and Cost Limits can't apply\n", i.Model)
				}
				switch {
				case i.Browser:
					fmt.Fprintln(w, "Browser:   included (INCLUDE_BROWSER=true)")
				case i.BrowserUnavailable != "":
					fmt.Fprintf(w, "Browser:   unavailable: %s\n", i.BrowserUnavailable)
				case i.Mode == "ui":
					fmt.Fprintln(w, "Browser:   not included (set INCLUDE_BROWSER=true in .env, then docker compose up --build)")
				}
				if r := i.Replay; r != nil && r.State != aosv1.ReplayState_REPLAY_STATE_UNSPECIFIED {
					fmt.Fprintf(w, "Replay:    %s\n", replayText(r))
				}
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

func limitText(usd float64) string {
	if usd <= 0 {
		return "none"
	}
	return dollars(usd)
}

// replayText describes Replay's progress in one line, notes after it.
func replayText(r *aosv1.ReplayStatus) string {
	switch r.State {
	case aosv1.ReplayState_REPLAY_STATE_RUNNING:
		return fmt.Sprintf("in progress (%d of %d): %s", r.Done, r.Total, r.Message)
	case aosv1.ReplayState_REPLAY_STATE_FAILED:
		return r.Message
	}
	return strings.ReplaceAll(r.Message, "\n", "\n           ")
}

// keyText describes the API key: its hint and where it came from, never the key.
func keyText(i *aosv1.InfoResponse) string {
	switch i.ApiKey {
	case "present":
		from := "from .env"
		if i.ApiKeySource == "settings" {
			from = "set in System Settings"
		}
		return fmt.Sprintf("%s, %s", i.ApiKeyHint, from)
	case "empty":
		return "empty (OPENAI_API_KEY is empty on the Host)"
	}
	return "not provided"
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
