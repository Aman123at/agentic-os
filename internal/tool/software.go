package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/amantiwari/agentic-os/internal/policy"
)

// Software runs the Privileged Tools with root authority and records them in
// the Install Ledger (PLAN.md §11).
type Software interface {
	Install(ctx context.Context, taskID, taskTitle, manager string, packages []string) (SoftwareResult, error)
	Remove(ctx context.Context, taskID, taskTitle, manager string, packages []string) (SoftwareResult, error)
	RunAsRoot(ctx context.Context, taskID, taskTitle, command, dir string, timeout time.Duration) (CommandResult, SoftwareResult, error)
	Checkpoint(ctx context.Context, taskID, name string) (id string, err error)
}

// SoftwareResult is what a Privileged Tool call did.
type SoftwareResult struct {
	// Output is the package manager's output.
	Output string
	// Changes describe what changed, such as "installed nginx 1.24.0 (apt)".
	Changes []string
	// Checkpoint is set when this call took the Checkpoint before its Task's
	// first software change.
	Checkpoint, CheckpointName string
	// Waited is true when the call waited for Replay or another Privileged Tool.
	Waited bool
}

// SoftwareTools returns the Software group except manage_service.
func SoftwareTools() []Tool {
	return []Tool{installPackage{}, installPackage{remove: true}, runPrivileged{}}
}

// describe adds what a Privileged Tool call changed to its result.
func (r SoftwareResult) describe(env *Env, b *strings.Builder) {
	if r.Waited {
		b.WriteString("(It waited for Replay or another software change to finish.)\n")
	}
	if len(r.Changes) == 0 {
		b.WriteString("Nothing changed in the Install Ledger.\n")
	} else {
		b.WriteString("Recorded in the Install Ledger:\n")
		for _, c := range r.Changes {
			b.WriteString("  " + c + "\n")
		}
	}
	if r.Checkpoint != "" {
		fmt.Fprintf(b, "Before this Task's first software change, AOS took Checkpoint %s (%q); restoring it undoes the Task's software changes.\n", r.Checkpoint, r.CheckpointName)
		if env.SetCheckpoint != nil {
			env.SetCheckpoint(r.Checkpoint)
		}
	}
}

// ---------------------------------------------------------------- install_package, remove_package

type installPackage struct{ remove bool }

func (t installPackage) Spec() Spec {
	if t.remove {
		return Spec{Name: "remove_package", Description: "Remove software installed with apt, pipx or npm; with apt, dependencies nothing else needs go too. Recorded in the Install Ledger. A Risky Action.",
			Parameters: object(map[string]any{
				"manager":  optStr("apt (default), pipx or npm"),
				"packages": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Package names"},
			})}
	}
	return Spec{Name: "install_package", Description: strings.Join([]string{
		"Install software: apt (system packages, as root; the default), pipx (Python applications) or npm (Node.js programs, into ~/.local).",
		"AOS records it in the Install Ledger, so it survives restarts and can be undone; before a Task's first software change AOS takes a Checkpoint.",
		"Recommended packages are not installed; name them if needed. Packages may pin a version: apt nginx=1.24.0-2ubuntu7, pipx httpie==3.2.4, npm cowsay@1.6.0.",
		"Installing does not start servers: run them as a Service with manage_service. A Risky Action.",
	}, " "), Parameters: object(map[string]any{
		"manager":  optStr("apt (default), pipx or npm"),
		"packages": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Package names, optionally with versions"},
	})}
}

func (t installPackage) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct {
		Manager  string
		Packages []string
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if a.Manager == "" {
		a.Manager = "apt"
	}
	if a.Manager != "apt" && a.Manager != "pipx" && a.Manager != "npm" {
		return nil, fmt.Errorf("manager %q: use apt, pipx or npm", a.Manager)
	}
	if len(a.Packages) == 0 {
		return nil, errors.New("no packages given")
	}
	name, verb, reason := "install_package", "Install", "installs software"
	if t.remove {
		name, verb, reason = "remove_package", "Remove", "removes software"
	}
	if a.Manager == "apt" {
		reason += " as root"
	}
	list := strings.Join(a.Packages, ", ")
	return &Call{Summary: fmt.Sprintf("%s %s (%s)", verb, list, a.Manager),
		Policy: policy.Call{Tool: name, Risky: true, Reasons: []string{reason + ": " + list}, Folder: a.Manager},
		Run: func(ctx context.Context, r Run) Result {
			if env.Software == nil {
				return Errorf("software changes are not available here")
			}
			run := env.Software.Install
			if t.remove {
				run = env.Software.Remove
			}
			res, err := run(ctx, env.TaskID, env.TaskTitle, a.Manager, a.Packages)
			var b strings.Builder
			if err != nil {
				fmt.Fprintf(&b, "error: %v\n", err)
			}
			res.describe(env, &b)
			if err != nil && res.Output != "" {
				b.WriteString("Output:\n" + Shape([]byte(res.Output), ""))
			}
			return Result{Output: b.String(), Error: err != nil}
		}}, nil
}

// ---------------------------------------------------------------- run_privileged_command

type runPrivileged struct{}

func (runPrivileged) Spec() Spec {
	return Spec{Name: "run_privileged_command", Description: strings.Join([]string{
		"Run a bash command as root, outside the sandbox and your Session, in your Session's current folder.",
		"Use it only for what needs root beyond installing packages, such as editing a file in /etc; install software with install_package.",
		"Changes to packages and /etc are recorded in the Install Ledger and survive restarts; other changes outside the home folder are lost when the Machine restarts.",
		"Always a Risky Action.",
	}, " "), Parameters: object(map[string]any{
		"command":         str("The bash command"),
		"timeout_seconds": optInt("Stop the command after this many seconds (default 600)"),
	})}
}

func (runPrivileged) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct {
		Command        string
		TimeoutSeconds int `json:"timeout_seconds"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Command) == "" {
		return nil, errors.New("command is empty")
	}
	cwd := env.cwd()
	analysis := policy.AnalyzeCommand(a.Command, policy.ShellEnv{Cwd: cwd, Home: env.Home, Stat: env.Stat})
	timeout := defaultTimeout
	if a.TimeoutSeconds > 0 {
		timeout = time.Duration(a.TimeoutSeconds) * time.Second
	}
	return &Call{Summary: "Run as root: " + oneLine(a.Command, 200),
		Policy: policy.Call{Tool: "run_privileged_command", Folder: cwd, Risky: true,
			Reasons: append([]string{"runs as root, outside the sandbox"}, analysis.Reasons...), Effects: analysis.Effects},
		Run: func(ctx context.Context, r Run) Result {
			if env.Software == nil {
				return Errorf("commands as root are not available here")
			}
			cr, sr, err := env.Software.RunAsRoot(ctx, env.TaskID, env.TaskTitle, a.Command, cwd, timeout)
			res := commandResult(env, r, cr, err, "Ran as root outside your Session; `cd` and `export` did not carry over.")
			var b strings.Builder
			sr.describe(env, &b)
			res.Output = b.String() + res.Output
			return res
		}}, nil
}

// ---------------------------------------------------------------- create_checkpoint

type createCheckpoint struct{}

func (createCheckpoint) Spec() Spec {
	return Spec{Name: "create_checkpoint", Description: "Name the Machine's current software state, so the user can Restore it later. AOS already takes one before a Task's first software change.",
		Parameters: object(map[string]any{"name": str("A short name, such as \"before upgrading Python\"")})}
}

func (createCheckpoint) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct{ Name string }
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	return &Call{Summary: "Create Checkpoint " + oneLine(a.Name, 80), Policy: policy.Call{Tool: "create_checkpoint", Coordination: true},
		Run: func(ctx context.Context, r Run) Result {
			if env.Software == nil {
				return Errorf("Checkpoints are not available here")
			}
			id, err := env.Software.Checkpoint(ctx, env.TaskID, a.Name)
			if err != nil {
				return Errorf("%v", err)
			}
			return Result{Output: fmt.Sprintf("Created Checkpoint %s (%q).", id, a.Name)}
		}}, nil
}
