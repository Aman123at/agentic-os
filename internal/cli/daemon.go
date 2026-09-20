package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/realm"
)

// daemonCmd controls the systemd unit that runs aosd on a native install
// (ADR-0009, M6.2). The verbs are thin wrappers over systemctl and journalctl;
// they need root (the unit is a system service), so run them as root or with
// sudo. `aos status` is the everyday front door.
func daemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Control the aosd systemd service (start, stop, restart, logs)",
		Args:  cobra.NoArgs,
	}
	verb := func(use, short, action string) *cobra.Command {
		return &cobra.Command{
			Use: use, Short: short, Args: cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error { return systemctl(action, "aos") },
		}
	}
	cmd.AddCommand(
		verb("start", "Start the Daemon (systemctl start aos)", "start"),
		verb("stop", "Stop the Daemon, draining Tasks first (systemctl stop aos)", "stop"),
		verb("restart", "Restart the Daemon (systemctl restart aos)", "restart"),
		logsCmd(),
	)
	return cmd
}

func logsCmd() *cobra.Command {
	var follow bool
	var lines int
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show the Daemon's journal (journalctl -u aos)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			args := []string{"-u", "aos", "-n", fmt.Sprint(lines)}
			if follow {
				args = append(args, "-f")
			}
			return passthrough("journalctl", args...)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new log lines")
	cmd.Flags().IntVarP(&lines, "lines", "n", 200, "how many recent lines to show")
	return cmd
}

// systemctl runs `systemctl <action> aos`, mapping a missing systemctl to a
// clear message (a non-systemd box, where Compose is the alternative).
func systemctl(action, unit string) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemctl is not available: the Daemon runs under systemd only (ADR-0009). Under Docker Compose use `docker compose %s`", composeVerb(action))
	}
	return passthrough("systemctl", action, unit)
}

func composeVerb(action string) string {
	switch action {
	case "start":
		return "up -d"
	case "stop":
		return "down"
	default:
		return "restart"
	}
}

// passthrough runs a command with the caller's stdio and turns a non-zero exit
// into the same exit code for aos.
func passthrough(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return exitError{ee.ExitCode()}
		}
		return err
	}
	return nil
}

// statusCmd is the front door: it prints the Machine's Mode, port and health,
// and the systemd unit's state where systemd runs it (M6.2).
func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the Machine is running, its Mode, port and health",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			port := envOr("AOS_PORT", "7700")
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			info, err := newClient().system.Info(ctx, connect.NewRequest(&aosv1.InfoRequest{}))
			cancel()
			if err != nil {
				fmt.Fprintf(w, "Health:  not responding — %v\n", explain(err))
				fmt.Fprintf(w, "Port:    %s\n", port)
				printUnitState(w)
				return exitError{1}
			}
			i := info.Msg
			if i.RootMode {
				fmt.Fprintln(w, rootBannerLine(newStyles(isTTY(os.Stdout))))
			}
			fmt.Fprintf(w, "Health:  ok (aosd %s responding on the control socket)\n", i.Version)
			fmt.Fprintf(w, "Mode:    %s\n", i.Mode)
			fmt.Fprintf(w, "Realm:   %s\n", realm.Of(i.RootMode))
			fmt.Fprintf(w, "Port:    %s\n", port)
			fmt.Fprintf(w, "Model:   %s\n", i.Model)
			printUnitState(w)
			printExposed(cmd.Context(), w)
			return nil
		},
	}
}

// printExposed warns about ports reachable from outside the Machine: a Service
// or loose listener bound to a wildcard address (0.0.0.0 or ::) is on the
// network past the Account. Best-effort — silent if the supervisor doesn't
// answer, so status still prints when it is busy.
func printExposed(ctx context.Context, w io.Writer) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	resp, err := newClient().supervisor.ListServices(ctx, connect.NewRequest(&aosv1.ListServicesRequest{}))
	if err != nil {
		return
	}
	seen := map[int32]bool{}
	var exposed []int32
	for _, s := range resp.Msg.Services {
		if s.Reachable {
			for _, p := range s.Ports {
				if !seen[p] {
					seen[p], exposed = true, append(exposed, p)
				}
			}
		}
	}
	for _, l := range resp.Msg.Listeners {
		if l.Reachable && !seen[l.Port] {
			seen[l.Port], exposed = true, append(exposed, l.Port)
		}
	}
	if len(exposed) == 0 {
		return
	}
	sort.Slice(exposed, func(i, j int) bool { return exposed[i] < exposed[j] })
	nums := make([]string, len(exposed))
	for i, p := range exposed {
		nums[i] = fmt.Sprint(p)
	}
	noun := "ports"
	if len(exposed) == 1 {
		noun = "port"
	}
	fmt.Fprintf(w, "Exposed: %s %s reachable from outside (bound to 0.0.0.0/::)\n", noun, strings.Join(nums, ", "))
}

// printUnitState adds the systemd unit's active state when systemd is present;
// it stays silent under Compose, where aosd is not a systemd unit.
func printUnitState(w io.Writer) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return
	}
	out, _ := exec.Command("systemctl", "is-active", "aos").Output()
	if state := strings.TrimSpace(string(out)); state != "" {
		fmt.Fprintf(w, "Unit:    %s (systemd)\n", state)
	}
}
