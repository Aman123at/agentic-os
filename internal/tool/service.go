package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Aman123at/agentic-os/internal/policy"
)

// Services manages the Machine's Services for Agents (PLAN.md §12).
type Services interface {
	Create(ctx context.Context, taskID, taskTitle string, def ServiceDefinition) (ServiceChange, error)
	Remove(ctx context.Context, taskID, taskTitle, name string) (ServiceChange, error)
	Start(ctx context.Context, name string) (ServiceStatus, error)
	Stop(ctx context.Context, name string) (ServiceStatus, error)
	Restart(ctx context.Context, name string) (ServiceStatus, error)
	// Status describes one Service, or all when name is empty.
	Status(name string) ([]ServiceStatus, error)
	// Logs returns up to limit bytes of a Service's recent output.
	Logs(name string, limit int) (string, error)
}

// ServiceDefinition is what an Agent asks for.
type ServiceDefinition struct {
	Name, Command, Dir string
	Env                map[string]string
	Root, Autostart    bool
	Restart            string
}

// ServiceStatus is how a Service runs.
type ServiceStatus struct {
	Name, Command, State, LastExit string
	PID, Restarts                  int
	Ports                          []int
	Root                           bool
}

// ServiceChange is the outcome of creating or removing a Service.
type ServiceChange struct {
	Status ServiceStatus
	// Checkpoint is set when this call took the Checkpoint before its Task's first change.
	Checkpoint, CheckpointName string
}

func (s ServiceStatus) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", s.Name, s.State)
	if s.PID > 0 {
		fmt.Fprintf(&b, " (pid %d)", s.PID)
	}
	if len(s.Ports) > 0 {
		ports := make([]string, len(s.Ports))
		for i, p := range s.Ports {
			ports[i] = fmt.Sprint(p)
		}
		fmt.Fprintf(&b, ", listening on port %s", strings.Join(ports, ", "))
	}
	if s.LastExit != "" {
		fmt.Fprintf(&b, ", last exit: %s", s.LastExit)
	}
	if s.Restarts > 0 {
		fmt.Fprintf(&b, ", restarted %d times", s.Restarts)
	}
	if s.Root {
		b.WriteString(", as root")
	}
	return b.String()
}

// ServiceTools returns manage_service.
func ServiceTools() []Tool { return []Tool{manageService{}} }

type manageService struct{}

func (manageService) Spec() Spec {
	return Spec{Name: "manage_service", Description: strings.Join([]string{
		"Manage Services: long-running programs AOS keeps running and starts again when the Machine restarts (there is no systemd).",
		"create runs command with bash in working_dir (default ~) as aos, confined like you; the command must stay in the foreground (e.g. nginx -g 'daemon off;').",
		"Programs such as nginx need a configuration in the home folder: their default files under /var and /run are not writable for aos.",
		"create and remove are Risky Actions, and root=true runs the Service as root outside the sandbox.",
		"start, stop and restart act on an existing Service; status (name null for all) shows state and ports; logs shows recent output.",
		"A Service listening on a port is reachable from the user's browser at http://<port>.localhost:<AOS port>.",
	}, " "), Parameters: object(map[string]any{
		"action":      str("create, remove, start, stop, restart, status or logs"),
		"name":        optStr("Service name: lower-case letters, digits, '-', '_' and '.'"),
		"command":     optStr("For create: the bash command to run"),
		"working_dir": optStr("For create: the folder to run it in (default ~)"),
		"env":         map[string]any{"type": []string{"object", "null"}, "additionalProperties": map[string]any{"type": "string"}, "description": "For create: environment variables"},
		"restart":     optStr("For create: always, on-failure (default) or never"),
		"autostart":   optBool("For create: start it when the Machine starts (default true)"),
		"root":        optBool("For create: run as root, outside the sandbox (a Privileged Tool call)"),
		"lines":       optInt("For logs: how many recent lines (default 50)"),
	})}
}

func (manageService) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct {
		Action, Name, Command string
		WorkingDir            string `json:"working_dir"`
		Env                   map[string]string
		Restart               string
		Autostart             *bool
		Root                  bool
		Lines                 int
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	c := &Call{Policy: policy.Call{Tool: "manage_service", Folder: a.Name}}
	switch a.Action {
	case "create":
		if a.Name == "" || strings.TrimSpace(a.Command) == "" {
			return nil, errors.New("create needs name and command")
		}
		dir := env.Home
		if a.WorkingDir != "" {
			dir = env.Abs(a.WorkingDir)
		}
		def := ServiceDefinition{Name: a.Name, Command: a.Command, Dir: dir, Env: a.Env, Root: a.Root, Autostart: a.Autostart == nil || *a.Autostart, Restart: a.Restart}
		c.Summary = fmt.Sprintf("Create Service %s: %s", a.Name, oneLine(a.Command, 160))
		c.Policy.Risky, c.Policy.Reasons = true, []string{"keeps a program running, also after restarts: " + oneLine(a.Command, 120)}
		if a.Root {
			c.Summary = fmt.Sprintf("Create Service %s as root: %s", a.Name, oneLine(a.Command, 160))
			analysis := policy.AnalyzeCommand(a.Command, policy.ShellEnv{Cwd: dir, Home: env.Home, Stat: env.Stat})
			c.Policy.Reasons = append(c.Policy.Reasons, "runs as root, outside the sandbox")
			c.Policy.Reasons = append(c.Policy.Reasons, analysis.Reasons...)
			c.Policy.Effects = analysis.Effects
		}
		c.Run = func(ctx context.Context, r Run) Result {
			if env.Services == nil {
				return Errorf("Services are not available here")
			}
			change, err := env.Services.Create(ctx, env.TaskID, env.TaskTitle, def)
			if err != nil {
				return Errorf("%v", err)
			}
			return settle(ctx, env, change, fmt.Sprintf("Created Service %s; it starts with the Machine: %v.", a.Name, def.Autostart))
		}
	case "remove":
		if a.Name == "" {
			return nil, errors.New("remove needs name")
		}
		c.Summary = "Remove Service " + a.Name
		c.Policy.Risky, c.Policy.Reasons = true, []string{"removes Service " + a.Name}
		c.Run = func(ctx context.Context, r Run) Result {
			if env.Services == nil {
				return Errorf("Services are not available here")
			}
			change, err := env.Services.Remove(ctx, env.TaskID, env.TaskTitle, a.Name)
			if err != nil {
				return Errorf("%v", err)
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Removed Service %s.\n", a.Name)
			noteCheckpoint(env, &b, change.Checkpoint, change.CheckpointName)
			return Result{Output: b.String()}
		}
	case "start", "stop", "restart":
		if a.Name == "" {
			return nil, fmt.Errorf("%s needs name", a.Action)
		}
		c.Summary = strings.ToUpper(a.Action[:1]) + a.Action[1:] + " Service " + a.Name
		c.Run = func(ctx context.Context, r Run) Result {
			if env.Services == nil {
				return Errorf("Services are not available here")
			}
			act := map[string]func(context.Context, string) (ServiceStatus, error){
				"start": env.Services.Start, "stop": env.Services.Stop, "restart": env.Services.Restart}[a.Action]
			st, err := act(ctx, a.Name)
			if err != nil {
				return Errorf("%v", err)
			}
			if a.Action == "stop" {
				return Result{Output: st.String() + "\nIt stays stopped until started again or the Machine restarts; remove deletes it."}
			}
			return settle(ctx, env, ServiceChange{Status: st}, "")
		}
	case "status":
		c.Summary, c.ReadOnly = "Show Services", true
		if a.Name != "" {
			c.Summary = "Show Service " + a.Name
		}
		c.Run = func(ctx context.Context, r Run) Result {
			if env.Services == nil {
				return Errorf("Services are not available here")
			}
			all, err := env.Services.Status(a.Name)
			if err != nil {
				return Errorf("%v", err)
			}
			if len(all) == 0 {
				return Result{Output: "There are no Services."}
			}
			lines := make([]string, len(all))
			for i, s := range all {
				lines[i] = s.String() + "; command: " + oneLine(s.Command, 160)
			}
			return Result{Output: strings.Join(lines, "\n")}
		}
	case "logs":
		if a.Name == "" {
			return nil, errors.New("logs needs name")
		}
		lines := a.Lines
		if lines <= 0 || lines > 1000 {
			lines = 50
		}
		c.Summary, c.ReadOnly = "Show the log of Service "+a.Name, true
		c.Run = func(ctx context.Context, r Run) Result {
			if env.Services == nil {
				return Errorf("Services are not available here")
			}
			out, err := env.Services.Logs(a.Name, 64<<10)
			if err != nil {
				return Errorf("%v", err)
			}
			return Result{Output: lastLines(out, lines)}
		}
	default:
		return nil, fmt.Errorf("unknown action %q: use create, remove, start, stop, restart, status or logs", a.Action)
	}
	return c, nil
}

// settle waits briefly for a started Service to listen or fail, so the Agent
// learns at once whether it works.
func settle(ctx context.Context, env *Env, change ServiceChange, note string) Result {
	st := change.Status
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if all, err := env.Services.Status(st.Name); err == nil && len(all) == 1 {
			st = all[0]
		}
		if st.State != "running" || len(st.Ports) > 0 {
			break
		}
		select {
		case <-ctx.Done():
			return Errorf("%v", ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
	var b strings.Builder
	if note != "" {
		b.WriteString(note + "\n")
	}
	b.WriteString(st.String() + "\n")
	noteCheckpoint(env, &b, change.Checkpoint, change.CheckpointName)
	failed := st.State == "failed" || st.State == "restarting" || st.State == "stopped"
	if failed {
		if logs, err := env.Services.Logs(st.Name, 4<<10); err == nil && logs != "" {
			b.WriteString("Recent output:\n" + lastLines(logs, 20))
		}
	}
	return Result{Output: b.String(), Error: failed}
}

func noteCheckpoint(env *Env, b *strings.Builder, id, name string) {
	if id == "" {
		return
	}
	fmt.Fprintf(b, "Before this Task's first change, AOS took Checkpoint %s (%q); restoring it undoes the Task's changes.\n", id, name)
	if env.SetCheckpoint != nil {
		env.SetCheckpoint(id)
	}
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
