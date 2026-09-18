package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
)

// configCmd reads and writes the settings in /etc/aos/config.yml (ADR-0010),
// through aosd — the file is root-only, and aosd writes it back preserving
// comments and key order.
func configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Show or change settings (stored in /etc/aos/config.yml)"}

	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List every setting, its value and where it comes from", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := newClient().settings.Get(cmd.Context(), connect.NewRequest(&aosv1.GetSettingsRequest{}))
			if err != nil {
				return explain(err)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "KEY\tVALUE\tSOURCE")
			for _, s := range resp.Msg.Settings {
				source := s.Source
				if s.PendingRestart {
					source += " (pending restart)"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", s.Key, quoteEmpty(s.Value), source)
			}
			return w.Flush()
		}})

	cmd.AddCommand(&cobra.Command{Use: "get <key>", Short: "Print one setting's value", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := newClient().settings.Get(cmd.Context(), connect.NewRequest(&aosv1.GetSettingsRequest{}))
			if err != nil {
				return explain(err)
			}
			for _, s := range resp.Msg.Settings {
				if s.Key == args[0] {
					fmt.Println(s.Value)
					return nil
				}
			}
			return fmt.Errorf("no setting %q (aos config list shows them all)", args[0])
		}})

	cmd.AddCommand(&cobra.Command{Use: "set <key> <value>", Short: `Change a setting (an empty value "" resets it to the default)`, Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := newClient().settings.Update(cmd.Context(), connect.NewRequest(&aosv1.UpdateSettingRequest{Key: args[0], Value: args[1]}))
			if err != nil {
				return explain(err)
			}
			s := resp.Msg.Setting
			fmt.Printf("%s = %s\n", s.Key, quoteEmpty(s.Value))
			if s.PendingRestart {
				fmt.Println("This setting applies on the next start: aos daemon restart")
			}
			return nil
		}})

	return cmd
}

func quoteEmpty(v string) string {
	if v == "" {
		return `""`
	}
	return v
}
