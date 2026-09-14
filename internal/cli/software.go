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

// ---------------------------------------------------------------- software

func softwareCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "software", Short: "Software installed through AOS: the Install Ledger"}
	var all bool
	list := &cobra.Command{Use: "list", Short: "Software the Machine gets back after every restart", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := newClient().software.ListPackages(cmd.Context(), connect.NewRequest(&aosv1.ListPackagesRequest{}))
			if err != nil {
				return explain(err)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "MANAGER\tPACKAGE\tVERSION")
			deps := 0
			for _, p := range resp.Msg.Packages {
				if p.Auto && !all {
					deps++
					continue
				}
				name := p.Name
				if p.Auto {
					name += " (dependency)"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", p.Manager, name, p.Version)
			}
			if err := w.Flush(); err != nil {
				return err
			}
			if deps > 0 {
				fmt.Printf("… and %d dependencies (--all lists them)\n", deps)
			}
			return nil
		}}
	list.Flags().BoolVar(&all, "all", false, "include dependencies")
	var limit int
	ledger := &cobra.Command{Use: "ledger", Short: "Every recorded change with root authority, newest first", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := newClient().software.ListLedger(cmd.Context(), connect.NewRequest(&aosv1.ListLedgerRequest{Limit: int32(limit)}))
			if err != nil {
				return explain(err)
			}
			for _, op := range resp.Msg.Ops {
				fmt.Printf("#%d  %s  %s  %s", op.Id, op.Time.AsTime().Local().Format("Jan 2 15:04"), op.Actor, op.Summary)
				if op.TaskId != "" {
					fmt.Printf("  (%s)", op.TaskId)
				}
				fmt.Println()
				for _, c := range op.Changes {
					fmt.Printf("    %s %s: %s → %s\n", c.Kind, c.Name, orText(c.Before, "none"), orText(c.After, "none"))
				}
			}
			return nil
		}}
	ledger.Flags().IntVar(&limit, "limit", 20, "how many operations")
	cmd.AddCommand(list, ledger)
	return cmd
}

// ---------------------------------------------------------------- checkpoint

func checkpointCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "checkpoint", Short: "List, create or restore Checkpoints of the Machine's software"}
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List Checkpoints, newest first", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := newClient().software.ListCheckpoints(cmd.Context(), connect.NewRequest(&aosv1.ListCheckpointsRequest{}))
			if err != nil {
				return explain(err)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tCREATED\tLEDGER\tNAME")
			for _, c := range resp.Msg.Checkpoints {
				name := c.Name
				if c.TaskId != "" {
					name += "  (" + c.TaskId + ")"
				}
				fmt.Fprintf(w, "%s\t%s\t#%d\t%s\n", c.Id, c.CreatedAt.AsTime().Local().Format("Jan 2 15:04"), c.LedgerId, name)
			}
			return w.Flush()
		}})
	cmd.AddCommand(&cobra.Command{Use: `create ["<name>"]`, Short: "Name the current state", Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := newClient().software.CreateCheckpoint(cmd.Context(), connect.NewRequest(&aosv1.CreateCheckpointRequest{Name: strings.Join(args, " ")}))
			if err != nil {
				return explain(err)
			}
			fmt.Printf("Created Checkpoint %s (%s)\n", resp.Msg.Checkpoint.Id, resp.Msg.Checkpoint.Name)
			return nil
		}})
	cmd.AddCommand(&cobra.Command{Use: "restore <id>", Short: "Undo every software, /etc and Service change since a Checkpoint", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(os.Stderr, "Restoring; package changes can take a while…")
			resp, err := newClient().software.RestoreCheckpoint(cmd.Context(), connect.NewRequest(&aosv1.RestoreCheckpointRequest{Id: args[0]}))
			if err != nil {
				return explain(err)
			}
			r := resp.Msg
			if r.Op == nil || len(r.Op.Changes) == 0 {
				fmt.Println("Nothing needed changing.")
			} else {
				fmt.Printf("Restored (Ledger #%d):\n", r.Op.Id)
				for _, c := range r.Op.Changes {
					fmt.Printf("  %s %s: %s → %s\n", c.Kind, c.Name, orText(c.Before, "none"), orText(c.After, "none"))
				}
			}
			for _, n := range r.Notes {
				fmt.Println("Note:", n)
			}
			if r.Before != nil {
				fmt.Printf("To undo this Restore: aos checkpoint restore %s\n", r.Before.Id)
			}
			return nil
		}})
	return cmd
}
