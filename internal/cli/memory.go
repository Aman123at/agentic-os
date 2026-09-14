package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
)

// ---------------------------------------------------------------- memory

func memoryCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "memory", Short: "Memory: what every Agent is told about your preferences"}
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List Memory and the Agents' proposals", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := newClient().settings.ListMemory(cmd.Context(), connect.NewRequest(&aosv1.ListMemoryRequest{}))
			if err != nil {
				return explain(err)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tSTATUS\tTEXT")
			proposals := false
			for _, m := range resp.Msg.Memories {
				fmt.Fprintf(w, "%s\t%s\t%s\n", m.Id, m.Status, m.Text)
				proposals = proposals || m.Status == "proposed"
			}
			if err := w.Flush(); err != nil {
				return err
			}
			if proposals {
				fmt.Println("Proposals are given to Agents once you accept them: aos memory accept <id>")
			}
			return nil
		}})
	cmd.AddCommand(&cobra.Command{Use: `add "<text>"`, Short: "Remember something for every future Task", Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := newClient().settings.AddMemory(cmd.Context(), connect.NewRequest(&aosv1.AddMemoryRequest{Text: strings.Join(args, " ")}))
			if err != nil {
				return explain(err)
			}
			fmt.Println("Remembered as", resp.Msg.Memory.Id)
			return nil
		}})
	cmd.AddCommand(&cobra.Command{Use: "accept <id>", Short: "Accept an Agent's proposal", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := newClient().settings.AcceptMemory(cmd.Context(), connect.NewRequest(&aosv1.AcceptMemoryRequest{Id: args[0]}))
			if err != nil {
				return explain(err)
			}
			fmt.Println("Remembered:", resp.Msg.Memory.Text)
			return nil
		}})
	cmd.AddCommand(&cobra.Command{Use: "forget <id>", Short: "Remove an entry or reject a proposal", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := newClient().settings.ForgetMemory(cmd.Context(), connect.NewRequest(&aosv1.ForgetMemoryRequest{Id: args[0]})); err != nil {
				return explain(err)
			}
			fmt.Println("Forgot", args[0])
			return nil
		}})
	return cmd
}
