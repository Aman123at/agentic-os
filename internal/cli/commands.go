package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"google.golang.org/protobuf/encoding/protojson"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
)

func isTTY(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

// ---------------------------------------------------------------- aos run

func runCmd() *cobra.Command {
	var autonomy string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   `run "<task>"`,
		Short: "Run one Task and wait for it (for scripts: without a terminal, calls that need an Approval are denied)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := parseAutonomy(autonomy)
			if err != nil {
				return err
			}
			interactive := isTTY(os.Stdin) && isTTY(os.Stdout) && !asJSON
			c := newClient()
			created, err := c.tasks.CreateTask(cmd.Context(), connect.NewRequest(&aosv1.CreateTaskRequest{Prompt: strings.Join(args, " "), Autonomy: a, Interactive: interactive}))
			if err != nil {
				return explain(err)
			}
			return followTask(cmd.Context(), c, created.Msg.Task.Id, interactive, asJSON)
		},
	}
	cmd.Flags().StringVar(&autonomy, "autonomy", "", "auto, confirm-risky or confirm-all for this Task (default: the configured Autonomy)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print events and the final Task as JSON lines")
	return cmd
}

// followTask follows a Task until it finishes, answering Approvals and
// questions in the terminal when interactive, and reports how it ended.
// Ctrl-C cancels the Task.
func followTask(parent context.Context, c *client, id string, interactive, asJSON bool) error {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	st := newStyles(isTTY(os.Stdout) && !asJSON)
	if !asJSON {
		fmt.Fprintf(os.Stderr, "%sTask %s%s\n", st.dim, id, st.reset)
	}
	f := newFollower(c, id, os.Stdout, st, isTTY(os.Stdout))
	f.json = asJSON
	if interactive {
		f.decide = promptDecision
		f.answer = promptAnswer
	}
	task, err := f.run(ctx)
	if ctx.Err() != nil {
		// Ctrl-C: cancel the Task, then report how it ended.
		_, _ = c.tasks.CancelTask(context.Background(), connect.NewRequest(&aosv1.CancelTaskRequest{Id: id}))
		fmt.Fprintf(os.Stderr, "\nCancelling %s…\n", id)
		task, err = waitFinished(c, id)
	}
	if err != nil {
		return err
	}
	return report(task, st, asJSON)
}

func parseAutonomy(s string) (aosv1.Autonomy, error) {
	switch s {
	case "":
		return aosv1.Autonomy_AUTONOMY_UNSPECIFIED, nil
	case "auto":
		return aosv1.Autonomy_AUTONOMY_AUTO, nil
	case "confirm-risky":
		return aosv1.Autonomy_AUTONOMY_CONFIRM_RISKY, nil
	case "confirm-all":
		return aosv1.Autonomy_AUTONOMY_CONFIRM_ALL, nil
	}
	return 0, fmt.Errorf("--autonomy %q: use auto, confirm-risky or confirm-all", s)
}

func waitFinished(c *client, id string) (*aosv1.Task, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		got, err := c.tasks.GetTask(ctx, connect.NewRequest(&aosv1.GetTaskRequest{Id: id}))
		if err != nil {
			return nil, explain(err)
		}
		if finished(got.Msg.Task.State) {
			return got.Msg.Task, nil
		}
		select {
		case <-ctx.Done():
			return got.Msg.Task, nil
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// report prints how a Task ended and returns its exit code.
func report(t *aosv1.Task, st styles, asJSON bool) error {
	if asJSON {
		b, _ := protojson.Marshal(t)
		fmt.Println(string(b))
	} else {
		mark := st.green + "✓"
		if t.State != aosv1.TaskState_TASK_STATE_SUCCEEDED {
			mark = st.red + "✗"
		}
		fmt.Fprintf(os.Stderr, "%s %s%s", mark, stateName(t.State), st.reset)
		if u := usageLine(t); u != "" {
			fmt.Fprintf(os.Stderr, " %s· %s%s", st.dim, u, st.reset)
		}
		fmt.Fprintln(os.Stderr)
		if t.State != aosv1.TaskState_TASK_STATE_SUCCEEDED && t.Summary != "" {
			fmt.Fprintln(os.Stderr, t.Summary)
		}
	}
	switch t.State {
	case aosv1.TaskState_TASK_STATE_SUCCEEDED:
		return nil
	case aosv1.TaskState_TASK_STATE_CANCELLED:
		return exitError{130}
	}
	return exitError{1}
}

// promptDecision reads one key from the terminal for an Approval.
func promptDecision(a *aosv1.Approval) aosv1.ApprovalDecision {
	for {
		fmt.Print("  choice: ")
		key := readKey()
		fmt.Println(string(key))
		switch key {
		case 'a', 'y':
			return aosv1.ApprovalDecision_APPROVAL_DECISION_ALLOW_ONCE
		case 't':
			if a.Grantable {
				return aosv1.ApprovalDecision_APPROVAL_DECISION_ALLOW_FOR_TASK
			}
		case 'd', 'n', 3: // 3 is Ctrl-C
			return aosv1.ApprovalDecision_APPROVAL_DECISION_DENY
		}
	}
}

func readKey() byte {
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err == nil {
		defer func() { _ = term.Restore(fd, old) }()
	}
	b := make([]byte, 1)
	if _, err := os.Stdin.Read(b); err != nil {
		return 'd'
	}
	return b[0]
}

func promptAnswer(string) (string, bool) {
	fmt.Print("  answer: ")
	var line strings.Builder
	b := make([]byte, 1)
	for {
		if _, err := os.Stdin.Read(b); err != nil || b[0] == '\n' {
			break
		}
		line.WriteByte(b[0])
	}
	return strings.TrimSpace(line.String()), true
}

// ---------------------------------------------------------------- tasks, show, cancel, stop

func tasksCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{Use: "tasks", Short: "List recent Tasks", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := newClient().tasks.ListTasks(cmd.Context(), connect.NewRequest(&aosv1.ListTasksRequest{Limit: int32(limit)}))
			if err != nil {
				return explain(err)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tSTATE\tCREATED\tTASK")
			for _, t := range resp.Msg.Tasks {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.Id, stateName(t.State), t.CreatedAt.AsTime().Local().Format("Jan 2 15:04"), t.Title)
			}
			return w.Flush()
		}}
	cmd.Flags().IntVar(&limit, "limit", 20, "how many Tasks")
	return cmd
}

func showCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{Use: "show <id>", Short: "Show a Task's steps (--follow keeps watching)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			st := newStyles(isTTY(os.Stdout))
			got, err := c.tasks.GetTask(cmd.Context(), connect.NewRequest(&aosv1.GetTaskRequest{Id: args[0]}))
			if err != nil {
				return explain(err)
			}
			t := got.Msg.Task
			fmt.Printf("%s%s%s  %s · %s · %s\n", st.bold, t.Title, st.reset, t.Id, stateName(t.State), strings.ToLower(strings.TrimPrefix(t.Autonomy.String(), "AUTONOMY_")))
			f := newFollower(c, t.Id, os.Stdout, st, false)
			if follow && !finished(t.State) {
				f.tty = isTTY(os.Stdout)
				task, err := f.run(cmd.Context())
				if err != nil {
					return err
				}
				return report(task, st, false)
			}
			for _, s := range got.Msg.Steps {
				f.step(s, false)
			}
			if t.Summary != "" && t.State != aosv1.TaskState_TASK_STATE_SUCCEEDED {
				fmt.Printf("%s%s%s\n", st.dim, t.Summary, st.reset)
			}
			if a := t.GetAwaiting(); a != nil {
				fmt.Printf("%sAwaiting you: %s%s\n", st.yellow, orText(a.Question, "an Approval (aos approve / aos deny)"), st.reset)
			}
			if u := usageLine(t); u != "" {
				fmt.Printf("%s%s%s\n", st.dim, u, st.reset)
			}
			if t.CheckpointId != "" {
				fmt.Printf("%sCheckpoint before its software changes: %s (aos checkpoint restore %s undoes them)%s\n", st.dim, t.CheckpointId, t.CheckpointId, st.reset)
			}
			return nil
		}}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep watching until the Task finishes")
	return cmd
}

func orText(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func cancelCmd() *cobra.Command {
	return &cobra.Command{Use: "cancel <id>", Short: "Cancel a Task", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := newClient().tasks.CancelTask(cmd.Context(), connect.NewRequest(&aosv1.CancelTaskRequest{Id: args[0]})); err != nil {
				return explain(err)
			}
			fmt.Println("Cancelling", args[0])
			return nil
		}}
}

func stopCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{Use: "stop --all", Short: "Stop every Agent", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !all {
				return fmt.Errorf("use aos stop --all (or aos cancel <id> for one Task)")
			}
			resp, err := newClient().tasks.StopAll(cmd.Context(), connect.NewRequest(&aosv1.StopAllRequest{}))
			if err != nil {
				return explain(err)
			}
			fmt.Printf("Cancelled %d Task(s)\n", resp.Msg.Cancelled)
			return nil
		}}
	cmd.Flags().BoolVar(&all, "all", false, "stop every queued, running and waiting Task")
	return cmd
}

// ---------------------------------------------------------------- approve, deny

func approveCmd(allow bool) *cobra.Command {
	name, short := "deny", "Deny a pending Approval"
	if allow {
		name, short = "approve", "Allow a pending Approval (--for-task: the same Tool in the same folder for the rest of the Task)"
	}
	var forTask bool
	cmd := &cobra.Command{Use: name + " [approval-id | task-id]", Short: short, Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			pending, err := c.approvals.ListPending(cmd.Context(), connect.NewRequest(&aosv1.ListPendingRequest{}))
			if err != nil {
				return explain(err)
			}
			var chosen *aosv1.Approval
			for _, a := range pending.Msg.Approvals {
				if len(args) == 0 || a.Id == args[0] || a.TaskId == args[0] {
					if chosen != nil && len(args) == 0 {
						return fmt.Errorf("several Approvals are pending; name one: %s", ids(pending.Msg.Approvals))
					}
					chosen = a
				}
			}
			if chosen == nil {
				return fmt.Errorf("no pending Approval matches")
			}
			decision := aosv1.ApprovalDecision_APPROVAL_DECISION_DENY
			if allow {
				decision = aosv1.ApprovalDecision_APPROVAL_DECISION_ALLOW_ONCE
				if forTask {
					decision = aosv1.ApprovalDecision_APPROVAL_DECISION_ALLOW_FOR_TASK
				}
			}
			resp, err := c.approvals.Decide(cmd.Context(), connect.NewRequest(&aosv1.DecideRequest{ApprovalId: chosen.Id, Decision: decision}))
			if err != nil {
				return explain(err)
			}
			fmt.Printf("%s: %s\n", strings.ToLower(strings.TrimPrefix(resp.Msg.Approval.Decision.String(), "APPROVAL_DECISION_")), chosen.Summary)
			return nil
		}}
	if allow {
		cmd.Flags().BoolVar(&forTask, "for-task", false, "allow the same Tool in the same folder for the rest of the Task")
	}
	return cmd
}

func ids(as []*aosv1.Approval) string {
	var out []string
	for _, a := range as {
		out = append(out, a.Id+" ("+a.Summary+")")
	}
	return strings.Join(out, ", ")
}

// ---------------------------------------------------------------- trash, protect

func trashCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "trash", Short: "List, restore or empty the Trash"}
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List the Trash", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := newClient().trash.ListTrash(cmd.Context(), connect.NewRequest(&aosv1.ListTrashRequest{}))
			if err != nil {
				return explain(err)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tDELETED\tSIZE\tFROM")
			for _, i := range resp.Msg.Items {
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", i.Id, i.DeletedAt.AsTime().Local().Format("Jan 2 15:04"), i.Size, i.OriginalPath)
			}
			return w.Flush()
		}})
	cmd.AddCommand(&cobra.Command{Use: "restore <id>", Short: "Put an item back", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := newClient().trash.Restore(cmd.Context(), connect.NewRequest(&aosv1.RestoreRequest{Id: args[0]}))
			if err != nil {
				return explain(err)
			}
			fmt.Println("Restored", resp.Msg.Path)
			return nil
		}})
	var yes bool
	empty := &cobra.Command{Use: "empty", Short: "Delete everything in the Trash permanently", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !yes {
				return fmt.Errorf("this deletes the Trash permanently; run aos trash empty --yes")
			}
			resp, err := newClient().trash.Empty(cmd.Context(), connect.NewRequest(&aosv1.EmptyRequest{}))
			if err != nil {
				return explain(err)
			}
			fmt.Printf("Removed %d item(s)\n", resp.Msg.Removed)
			return nil
		}}
	empty.Flags().BoolVar(&yes, "yes", false, "confirm")
	cmd.AddCommand(empty)
	return cmd
}

func protectCmd(lock bool) *cobra.Command {
	name, short := "unprotect", "Unlock a path you locked"
	if lock {
		name, short = "protect", "Lock a path: Agents need your Approval to change it (no path: list Protected Paths)"
	}
	return &cobra.Command{Use: name + " <path>", Short: short, Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			if len(args) == 0 {
				if !lock {
					return fmt.Errorf("name the path to unlock")
				}
				resp, err := c.files.ListProtected(cmd.Context(), connect.NewRequest(&aosv1.ListProtectedRequest{}))
				if err != nil {
					return explain(err)
				}
				w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
				fmt.Fprintln(w, "PATH\tSOURCE\tENFORCED BY")
				for _, e := range resp.Msg.Entries {
					by := "policy"
					if e.Kernel {
						by = "kernel and policy"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\n", e.Path, e.Source, by)
				}
				return w.Flush()
			}
			path, err := absPath(args[0])
			if err != nil {
				return err
			}
			if lock {
				_, err = c.files.Protect(cmd.Context(), connect.NewRequest(&aosv1.ProtectRequest{Path: path}))
			} else {
				_, err = c.files.Unprotect(cmd.Context(), connect.NewRequest(&aosv1.UnprotectRequest{Path: path}))
			}
			if err != nil {
				return explain(err)
			}
			fmt.Printf("%sed %s\n", name, path)
			return nil
		}}
}

func absPath(p string) (string, error) {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") {
		return p, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return cwd + "/" + p, nil
}

func execRealRm(args []string) int {
	cmd := exec.Command("/usr/bin/rm", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		return 1
	}
	return 0
}

// ---------------------------------------------------------------- audit

func auditCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{Use: "audit [task-id]", Short: "Show the Audit Log, newest first", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &aosv1.AuditRequest{Limit: int32(limit)}
			if len(args) == 1 {
				req.TaskId = args[0]
			}
			resp, err := newClient().system.Audit(cmd.Context(), connect.NewRequest(req))
			if err != nil {
				return explain(err)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "TIME\tTASK\tACTOR\tTOOL\tDECISION\tBY\tRESULT")
			for _, e := range resp.Msg.Entries {
				result, _, _ := strings.Cut(e.ResultSummary, "\n")
				if len(result) > 60 {
					result = result[:60] + "…"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", e.Time.AsTime().Local().Format("15:04:05"), e.TaskId, e.Actor, e.Tool, e.Decision, e.DecidedBy, result)
			}
			return w.Flush()
		}}
	cmd.Flags().IntVar(&limit, "limit", 50, "how many entries")
	return cmd
}
