package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
)

// ---------------------------------------------------------------- service

func serviceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "service", Short: "Services: programs AOS keeps running, also after restarts"}
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List Services and every program listening on a port", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := newClient().supervisor.ListServices(cmd.Context(), connect.NewRequest(&aosv1.ListServicesRequest{}))
			if err != nil {
				return explain(err)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSTATE\tPID\tPORTS\tRESTARTS\tCOMMAND")
			for _, s := range resp.Msg.Services {
				state := serviceState(s.State)
				if s.LastExit != "" && s.State != aosv1.ServiceState_SERVICE_STATE_RUNNING {
					state += " (" + s.LastExit + ")"
				}
				if s.Root {
					state += ", root"
				}
				cmdText := s.Command
				if len(cmdText) > 60 {
					cmdText = cmdText[:59] + "…"
				}
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%d\t%s\n", s.Name, state, s.Pid, ports(s.Ports), s.Restarts, cmdText)
			}
			if err := w.Flush(); err != nil {
				return err
			}
			var other []string
			for _, l := range resp.Msg.Listeners {
				if l.Service == "" {
					other = append(other, fmt.Sprintf("%d (%s, pid %d)", l.Port, orText(l.Process, "?"), l.Pid))
				}
			}
			if len(other) > 0 {
				fmt.Println("Also listening:", strings.Join(other, ", "))
			}
			if len(resp.Msg.Listeners) > 0 {
				fmt.Printf("Open a port from this computer's browser: http://<port>.localhost:%s\n", envOr("AOS_PORT", "7700"))
			}
			return nil
		}})
	var follow bool
	var tail int
	logs := &cobra.Command{Use: "logs <name>", Short: "Show a Service's recent output (-f keeps following)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			stream, err := newClient().supervisor.StreamLogs(cmd.Context(), connect.NewRequest(&aosv1.StreamLogsRequest{Name: args[0], Follow: follow, TailBytes: int32(tail)}))
			if err != nil {
				return explain(err)
			}
			defer stream.Close()
			for stream.Receive() {
				_, _ = os.Stdout.Write(stream.Msg().Data)
			}
			return explain(stream.Err())
		}}
	logs.Flags().BoolVarP(&follow, "follow", "f", false, "keep showing new output")
	logs.Flags().IntVar(&tail, "bytes", 0, "how much recent output (default 64 KiB)")
	cmd.AddCommand(logs)
	for _, action := range []string{"start", "stop", "restart", "remove"} {
		short := map[string]string{
			"start":   "Start a stopped Service",
			"stop":    "Stop a Service until it is started again or the Machine restarts",
			"restart": "Stop and start a Service",
			"remove":  "Stop a Service and delete it (recorded in the Install Ledger)",
		}[action]
		cmd.AddCommand(&cobra.Command{Use: action + " <name>", Short: short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, ctx, name := newClient(), cmd.Context(), args[0]
				var info *aosv1.ServiceInfo
				var err error
				switch action {
				case "start":
					var r *connect.Response[aosv1.StartServiceResponse]
					if r, err = c.supervisor.StartService(ctx, connect.NewRequest(&aosv1.StartServiceRequest{Name: name})); err == nil {
						info = r.Msg.Service
					}
				case "stop":
					var r *connect.Response[aosv1.StopServiceResponse]
					if r, err = c.supervisor.StopService(ctx, connect.NewRequest(&aosv1.StopServiceRequest{Name: name})); err == nil {
						info = r.Msg.Service
					}
				case "restart":
					var r *connect.Response[aosv1.RestartServiceResponse]
					if r, err = c.supervisor.RestartService(ctx, connect.NewRequest(&aosv1.RestartServiceRequest{Name: name})); err == nil {
						info = r.Msg.Service
					}
				default:
					_, err = c.supervisor.RemoveService(ctx, connect.NewRequest(&aosv1.RemoveServiceRequest{Name: name}))
				}
				if err != nil {
					return explain(err)
				}
				if info == nil {
					fmt.Println("Removed", name)
					return nil
				}
				fmt.Printf("%s: %s\n", info.Name, serviceState(info.State))
				return nil
			}})
	}
	return cmd
}

func serviceState(s aosv1.ServiceState) string {
	return strings.ToLower(strings.TrimPrefix(s.String(), "SERVICE_STATE_"))
}

func ports(ps []int32) string {
	if len(ps) == 0 {
		return "-"
	}
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = fmt.Sprint(p)
	}
	return strings.Join(out, ",")
}
