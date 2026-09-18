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

// Sessions runs commands for one Task (PLAN.md §10).
type Sessions interface {
	// Run runs a command in the Task's persistent Agent Session.
	Run(ctx context.Context, command string, timeout time.Duration) (CommandResult, error)
	// Input types into the command that is waiting for input, then waits like Run.
	Input(ctx context.Context, text string, timeout time.Duration) (CommandResult, error)
	// RunIsolated runs a command outside the Session, in its current folder, with
	// the sandbox widened to approved Protected Paths.
	RunIsolated(ctx context.Context, command string, widen []string, timeout time.Duration) (CommandResult, error)
	// Start runs a command in the background.
	Start(ctx context.Context, command string) (Process, error)
	// Stop stops a background process, or the foreground command when id is empty.
	Stop(ctx context.Context, id string) error
	Processes() []Process
	Cwd() string
}

// CommandResult is the outcome of a foreground command.
type CommandResult struct {
	Output   []byte
	ExitCode int
	// Waiting means the command is still running and looks like it waits for input.
	Waiting  bool
	TimedOut bool
	Duration time.Duration
	Cwd      string
	// Notes are told to the Agent, e.g. what the user typed into the Session.
	Notes []string
}

// Process is a background command.
type Process struct {
	ID        string
	Command   string
	PID       int
	Running   bool
	ExitCode  int
	OutputRef string
}

// SessionTools returns the Session group.
func SessionTools() []Tool {
	return []Tool{runCommand{}, sendInput{}, readOutput{}, stopProcess{}}
}

const defaultTimeout = 10 * time.Minute

// ---------------------------------------------------------------- run_command

type runCommand struct{}

func (runCommand) Spec() Spec {
	return Spec{Name: "run_command", Description: strings.Join([]string{
		"Run a bash command in your persistent Session (current folder and exported variables carry over) as the unprivileged user aos, without sudo.",
		"Output shows the first 2 KB and last 6 KB; read_output pages through the rest.",
		"Long-running programs such as servers: background=true.",
		"If the command waits for input, you are told so; answer with send_input.",
		"If the sandbox refuses a change to a Protected Path, call again with protected_paths listing exactly the paths to change; the user is asked.",
	}, " "), Parameters: object(map[string]any{
		"command":         str("The bash command"),
		"timeout_seconds": optInt("Stop the command after this many seconds (default 600)"),
		"background":      optBool("Run in the background and return immediately"),
		"protected_paths": map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "string"}, "description": "Protected Paths this command must change; requires the user's Approval"},
	})}
}

func (runCommand) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct {
		Command        string
		TimeoutSeconds int      `json:"timeout_seconds"`
		Background     bool     `json:"background"`
		ProtectedPaths []string `json:"protected_paths"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Command) == "" {
		return nil, errors.New("command is empty")
	}
	cwd := env.cwd()
	analysis := policy.AnalyzeCommand(a.Command, policy.ShellEnv{Cwd: cwd, Home: env.Home, Stat: env.Stat})
	c := &Call{Summary: "Run: " + oneLine(a.Command, 200),
		Policy: policy.Call{Tool: "run_command", Folder: cwd, Risky: analysis.Risky, Reasons: analysis.Reasons, Effects: analysis.Effects}}
	for _, p := range a.ProtectedPaths {
		c.Policy.Effects = append(c.Policy.Effects, policy.Effect{Path: env.Abs(p), Op: policy.Write})
	}
	timeout := defaultTimeout
	if a.TimeoutSeconds > 0 {
		timeout = time.Duration(a.TimeoutSeconds) * time.Second
	}
	c.Run = func(ctx context.Context, r Run) Result {
		switch {
		case a.Background:
			if len(r.Widen) > 0 {
				return Errorf("background commands cannot change Protected Paths")
			}
			p, err := env.Sessions.Start(ctx, a.Command)
			if err != nil {
				return Errorf("%v", err)
			}
			return Result{Output: fmt.Sprintf("Started in the background as process %s (pid %d). read_output ref=%q shows its output; stop_process stops it.", p.ID, p.PID, p.OutputRef), OutputRef: p.OutputRef}
		case len(r.Widen) > 0:
			res, err := env.Sessions.RunIsolated(ctx, a.Command, r.Widen, timeout)
			return commandResult(env, r, res, err, "Ran outside your Session with the approved Protected Paths writable; `cd` and `export` did not carry over.")
		default:
			res, err := env.Sessions.Run(ctx, a.Command, timeout)
			return commandResult(env, r, res, err, "")
		}
	}
	return c, nil
}

// commandResult saves the full output and describes the result for the model.
func commandResult(env *Env, r Run, res CommandResult, err error, note string) Result {
	if err != nil {
		return Errorf("%v", err)
	}
	var ref string
	if env.Outputs != nil && len(res.Output) > headBytes+tailBytes {
		if name, f, err := env.Outputs.Create(env.TaskID, r.StepID+".out"); err == nil {
			_, _ = f.Write(res.Output)
			_ = f.Close()
			ref = name
		}
	}
	var b strings.Builder
	switch {
	case res.Waiting:
		fmt.Fprintf(&b, "Still running after %s and waiting for input. Answer with send_input, or stop it with stop_process.\n", res.Duration.Round(time.Millisecond))
	case res.TimedOut:
		fmt.Fprintf(&b, "Stopped after the timeout (%s); exit code %d.\n", res.Duration.Round(time.Second), res.ExitCode)
	default:
		fmt.Fprintf(&b, "Exit code %d after %s.\n", res.ExitCode, res.Duration.Round(time.Millisecond))
	}
	if res.Cwd != "" {
		fmt.Fprintf(&b, "Current folder: %s\n", env.Display(res.Cwd))
	}
	if note != "" {
		b.WriteString(note + "\n")
	}
	for _, n := range res.Notes {
		b.WriteString(n + "\n")
	}
	if len(res.Output) == 0 {
		b.WriteString("(no output)")
	} else {
		b.WriteString("Output:\n")
		b.WriteString(Shape(res.Output, ref))
	}
	return Result{Output: b.String(), OutputRef: ref, Error: !res.Waiting && res.ExitCode != 0}
}

// ---------------------------------------------------------------- send_input

type sendInput struct{}

func (sendInput) Spec() Spec {
	return Spec{Name: "send_input", Description: "Type into the command that is waiting for input in your Session, then wait for it like run_command. A newline is sent after the text unless enter=false.",
		Parameters: object(map[string]any{
			"text":            str("Text to type"),
			"enter":           optBool("Press Enter after the text (default true)"),
			"timeout_seconds": optInt("Wait at most this many seconds (default 600)"),
		})}
}

func (sendInput) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct {
		Text           string
		Enter          *bool
		TimeoutSeconds int `json:"timeout_seconds"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	text := a.Text
	if a.Enter == nil || *a.Enter {
		text += "\r"
	}
	timeout := defaultTimeout
	if a.TimeoutSeconds > 0 {
		timeout = time.Duration(a.TimeoutSeconds) * time.Second
	}
	return &Call{Summary: "Type into the Session", Policy: policy.Call{Tool: "send_input"},
		Run: func(ctx context.Context, r Run) Result {
			res, err := env.Sessions.Input(ctx, text, timeout)
			return commandResult(env, r, res, err, "")
		}}, nil
}

// ---------------------------------------------------------------- read_output

type readOutput struct{}

func (readOutput) Spec() Spec {
	return Spec{Name: "read_output", Description: "Read part of a command's full output, or a background process's output so far, by its ref.",
		Parameters: object(map[string]any{
			"ref":    str("Output ref from run_command"),
			"offset": optInt("Byte offset to start at"),
			"limit":  optInt("Bytes to read (default and maximum 16000)"),
		})}
}

func (readOutput) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct {
		Ref           string
		Offset, Limit int64
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if a.Limit <= 0 || a.Limit > 16000 {
		a.Limit = 16000
	}
	return &Call{Summary: "Read output " + a.Ref, ReadOnly: true, Policy: policy.Call{Tool: "read_output"},
		Run: func(ctx context.Context, r Run) Result {
			data, size, err := env.Outputs.Page(env.TaskID, a.Ref, a.Offset, a.Limit)
			if err != nil {
				return Errorf("%v", err)
			}
			end := a.Offset + int64(len(data))
			out := fmt.Sprintf("Bytes %d-%d of %d:\n%s", a.Offset, end, size, data)
			if end < size {
				out += fmt.Sprintf("\n… continue with offset=%d", end)
			}
			return Result{Output: out}
		}}, nil
}

// ---------------------------------------------------------------- stop_process

type stopProcess struct{}

func (stopProcess) Spec() Spec {
	return Spec{Name: "stop_process", Description: "Stop one of this Task's background processes, or with an empty process_id the command running in your Session (Ctrl-C, then kill after 5 s).",
		Parameters: object(map[string]any{"process_id": optStr("Background process id; null for the Session's command")})}
}

func (stopProcess) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct {
		ProcessID string `json:"process_id"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	return &Call{Summary: "Stop process " + a.ProcessID, Policy: policy.Call{Tool: "stop_process"},
		Run: func(ctx context.Context, r Run) Result {
			if err := env.Sessions.Stop(ctx, a.ProcessID); err != nil {
				return Errorf("%v", err)
			}
			return Result{Output: "Stopped."}
		}}, nil
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
