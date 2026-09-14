// Package tool defines the Tools an Agent can call (PLAN.md §9): their schemas,
// how each call is classified for policy, and how it runs.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/amantiwari/agentic-os/internal/files"
	"github.com/amantiwari/agentic-os/internal/llm"
	"github.com/amantiwari/agentic-os/internal/policy"
)

// Spec describes a Tool.
type Spec struct {
	Name        string
	Description string
	Parameters  map[string]any
	// Hosted names a provider-side tool, which the provider runs itself.
	Hosted string
}

// Tool is one named action.
type Tool interface {
	Spec() Spec
	// Prepare validates the arguments and describes the call for policy and the
	// Approval, before anything happens.
	Prepare(ctx context.Context, env *Env, args json.RawMessage) (*Call, error)
}

// Call is a prepared Tool call.
type Call struct {
	// Summary says what the call does, for the Approval and the step feed.
	Summary string
	// ReadOnly calls run in parallel with each other.
	ReadOnly bool
	Policy   policy.Call
	Run      func(ctx context.Context, r Run) Result
}

// Run carries what the call may use while running.
type Run struct {
	StepID string
	// Widen lists Protected Paths approved for this one call (PLAN.md §7.4).
	Widen []string
	// Progress reports download progress.
	Progress func(url, path string, n, total int64)
}

// Result is what the model sees.
type Result struct {
	Output    string
	OutputRef string
	Error     bool
}

// Errorf returns a failed Result.
func Errorf(format string, args ...any) Result {
	return Result{Output: "error: " + fmt.Sprintf(format, args...), Error: true}
}

// Env is a Task's context for its Tool calls.
type Env struct {
	TaskID      string
	Home        string
	Interactive bool
	// Files returns a runner for file operations, confined to the Agent ruleset
	// widened by the given Protected Paths.
	Files func(widen []string) files.Runner
	// FileOps configures file operations (home, Shared Folder, uid).
	FileOps files.Ops
	// Stat reports whether a path exists and is a folder, for classifying calls.
	Stat func(path string) (exists, dir bool)
	// Cwd is the Session's current folder, for relative paths; nil means Home.
	Cwd      func() string
	Sessions Sessions
	Outputs  *Outputs
	HTTP     *http.Client
	AskUser  func(ctx context.Context, question string) (string, error)
	// Remember saves a Memory entry; direct saves it without asking the user.
	Remember func(ctx context.Context, text string, direct bool) error
	// UserMessages returns what the user said in this Task so far.
	UserMessages func() []string
	// TaskTitle names the Task, e.g. in its Checkpoint.
	TaskTitle string
	// Software runs the Privileged Tools; nil where they are unavailable.
	Software Software
	// Services manages the Machine's Services; nil where they are unavailable.
	Services Services
	// SetCheckpoint records the Checkpoint taken before the Task's first software change.
	SetCheckpoint func(id string)
}

func (e *Env) stat(path string) (exists, dir bool) {
	if e.Stat == nil {
		return false, false
	}
	return e.Stat(path)
}

func (e *Env) cwd() string {
	if e.Cwd != nil {
		if c := e.Cwd(); c != "" {
			return c
		}
	}
	return e.Home
}

// Abs resolves a path argument: ~ and $HOME expand, relative paths start at the Session's folder.
func (e *Env) Abs(path string) string {
	switch {
	case path == "~" || path == "$HOME":
		path = e.Home
	case strings.HasPrefix(path, "~/"):
		path = filepath.Join(e.Home, path[2:])
	case strings.HasPrefix(path, "$HOME/"):
		path = filepath.Join(e.Home, path[6:])
	case !filepath.IsAbs(path):
		path = filepath.Join(e.cwd(), path)
	}
	return filepath.Clean(path)
}

// Display shortens a path under home to ~/….
func (e *Env) Display(path string) string {
	if path == e.Home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, e.Home+"/"); ok {
		return "~/" + rest
	}
	return path
}

// Registry holds the Tools offered to Agents.
type Registry struct {
	tools map[string]Tool
	order []string
}

// NewRegistry returns a Registry of tools, in the order given.
func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{tools: map[string]Tool{}}
	for _, t := range tools {
		name := t.Spec().Name
		r.tools[name] = t
		r.order = append(r.order, name)
	}
	return r
}

// Get returns the Tool called name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Specs describes every Tool to the model.
func (r *Registry) Specs() []llm.ToolSpec {
	specs := make([]llm.ToolSpec, 0, len(r.order))
	for _, name := range r.order {
		s := r.tools[name].Spec()
		specs = append(specs, llm.ToolSpec{Name: s.Name, Description: s.Description, Parameters: s.Parameters, Hosted: s.Hosted})
	}
	return specs
}

// decode unmarshals JSON arguments strictly.
func decode(args json.RawMessage, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(args)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

// object builds a JSON Schema object for strict function calling: every
// property is required, and optional ones accept null.
func object(props map[string]any) map[string]any {
	req := make([]string, 0, len(props))
	for name := range props {
		req = append(req, name)
	}
	sort.Strings(req)
	return map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func optStr(desc string) map[string]any {
	return map[string]any{"type": []string{"string", "null"}, "description": desc}
}
func optInt(desc string) map[string]any {
	return map[string]any{"type": []string{"integer", "null"}, "description": desc}
}
func optBool(desc string) map[string]any {
	return map[string]any{"type": []string{"boolean", "null"}, "description": desc}
}
