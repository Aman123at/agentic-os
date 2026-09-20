package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
)

// rootCmd shows or switches the Realm (PLAN.md §18 M7.11). One image and one
// aosd carry both Realms; root_mode: in config.yml selects which one starts, and
// switching is a guarded key change plus a restart, not a rebuild. Bare
// `aos root` prints the Realm in force in one word — `on` or `off`, matching
// `aos status` — and `aos root on` / `aos root off` change it behind a warning
// and a `yes`. Over the control socket the caller has already proven root
// (§7.5), so no password is asked; the Daemon restarts itself out of band (M7.3),
// so the CLI never touches systemctl. In `cli` Mode, with no Desktop, this is the
// only way to switch.
func rootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "root [on|off]",
		Short: "Show or switch Root Mode (on or off)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := newClient().system.Info(cmd.Context(), connect.NewRequest(&aosv1.InfoRequest{}))
			if err != nil {
				return explain(err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), onOff(resp.Msg.RootMode))
			return nil
		},
	}
	cmd.AddCommand(rootSwitchCmd(true), rootSwitchCmd(false))
	return cmd
}

// rootSwitchCmd builds `aos root on` or `aos root off`.
func rootSwitchCmd(on bool) *cobra.Command {
	name, short := "off", "Turn off Root Mode: leave the Root Realm and restart into Standard Mode"
	if on {
		name, short = "on", "Turn on Root Mode: Agents and the Terminal run as root; AOS restarts"
	}
	var yes bool
	cmd := &cobra.Command{
		Use:   name,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return switchRoot(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), newClient(), on, yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation (for scripts); it does not override the running-Agent check")
	return cmd
}

// switchRoot prints the warning, asks for `yes` unless --yes, then calls
// SetRootMode (M7.7). A switch refused because an Agent is still working prints
// the blocking Tasks and exits non-zero, whether or not --yes was given.
func switchRoot(ctx context.Context, in io.Reader, out io.Writer, c *client, on, yes bool) error {
	if on {
		fmt.Fprint(out, rootOnWarning)
	} else {
		fmt.Fprint(out, rootOffWarning)
	}
	if !yes {
		ok, err := confirmRoot(in, out, on)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(out, "Cancelled — nothing changed.")
			return nil
		}
	}
	_, err := c.system.SetRootMode(ctx, connect.NewRequest(&aosv1.SetRootModeRequest{Enabled: on}))
	if err != nil {
		if b := blockedDetail(err); b != nil {
			printBlocked(out, b)
			return exitError{1}
		}
		return explain(err)
	}
	where := "Standard Mode"
	if on {
		where = "Root Mode"
	}
	fmt.Fprintf(out, "Restarting AOS into %s…\n", where)
	return nil
}

// confirmRoot reads one line and reports whether the person typed `yes`.
func confirmRoot(in io.Reader, out io.Writer, on bool) (bool, error) {
	verb := "off"
	if on {
		verb = "on"
	}
	fmt.Fprintf(out, "Type yes to turn Root Mode %s: ", verb)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return strings.TrimSpace(line) == "yes", nil
}

// blockedDetail returns the RootModeBlocked detail carried on a switch error
// (FailedPrecondition, M7.7), or nil when the error is something else.
func blockedDetail(err error) *aosv1.RootModeBlocked {
	var ce *connect.Error
	if !errors.As(err, &ce) {
		return nil
	}
	for _, d := range ce.Details() {
		if msg, verr := d.Value(); verr == nil {
			if b, ok := msg.(*aosv1.RootModeBlocked); ok {
				return b
			}
		}
	}
	return nil
}

// printBlocked lists the Tasks that must finish or be cancelled before the Realm
// can switch, and points at `aos cancel`.
func printBlocked(out io.Writer, b *aosv1.RootModeBlocked) {
	fmt.Fprintln(out, "An Agent is still working — Root Mode can't be switched until it finishes or you cancel it:")
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, t := range b.Tasks {
		fmt.Fprintf(w, "  %s\t%s\t%s\n", t.Id, stateName(t.State), t.Title)
	}
	_ = w.Flush()
	fmt.Fprintln(out, "Cancel it with aos cancel <id>, then try again.")
}

func onOff(rootMode bool) string {
	if rootMode {
		return "on"
	}
	return "off"
}

// rootBannerLine is the red ROOT MODE line the Realm-aware commands print at the
// top while Root Mode is on (M7.11), so the Realm in force is never a surprise.
func rootBannerLine(st styles) string {
	return fmt.Sprintf("%s%s⛔ ROOT MODE — Agents and the Terminal run as root%s", st.bold, st.red, st.reset)
}

// printRealmBanner prints the ROOT MODE banner to w when the Machine is in Root
// Mode. Best-effort: it is a heads-up, so an Info that cannot be reached is
// silent and the command carries on.
func printRealmBanner(ctx context.Context, c *client, w *os.File) {
	info, err := c.system.Info(ctx, connect.NewRequest(&aosv1.InfoRequest{}))
	if err != nil || info.Msg == nil || !info.Msg.RootMode {
		return
	}
	fmt.Fprintln(w, rootBannerLine(newStyles(isTTY(w))))
}

// The warnings mirror the Desktop's red gate (M7.9), so the CLI and the switch in
// System Settings say the same thing.
const rootOnWarning = `Turn on Root Mode?

  • Agents and the Terminal will run as root with full sudo.
  • Every file and folder is unlocked; Protected Paths are not enforced.
  • Changes to the system cannot be undone by AOS — Trash and Checkpoints
    don't cover what root does outside them.
  • Root Mode keeps its own chats and Audit Log, hidden from Standard Mode.
  • Open Terminal sessions and Services will stop, and AOS will restart.
  • A root Agent can, in the end, get around AOS itself.

`

const rootOffWarning = `Turn off Root Mode?

  Root Mode Services will stop; AOS will restart into Standard Mode. What root
  already did to the server's files and packages is real and stays.

`
