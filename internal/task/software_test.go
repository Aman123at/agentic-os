package task

import (
	"context"
	"strings"
	"testing"
	"time"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/llm"
	"github.com/Aman123at/agentic-os/internal/llm/fake"
	"github.com/Aman123at/agentic-os/internal/policy"
	"github.com/Aman123at/agentic-os/internal/tool"
)

// fakeSoftware stands in for the software Manager.
type fakeSoftware struct{ installs []string }

func (f *fakeSoftware) Install(_ context.Context, taskID, title, manager string, packages []string) (tool.SoftwareResult, error) {
	f.installs = append(f.installs, manager+":"+strings.Join(packages, ","))
	return tool.SoftwareResult{Changes: []string{"installed nginx:arm64 1.24.0 (apt)"}, Checkpoint: "c_1234", CheckpointName: "Before: " + title}, nil
}
func (f *fakeSoftware) Remove(context.Context, string, string, string, []string) (tool.SoftwareResult, error) {
	return tool.SoftwareResult{}, nil
}
func (f *fakeSoftware) RunAsRoot(context.Context, string, string, string, string, time.Duration) (tool.CommandResult, tool.SoftwareResult, error) {
	return tool.CommandResult{}, tool.SoftwareResult{}, nil
}
func (f *fakeSoftware) Checkpoint(context.Context, string, string) (string, error) {
	return "c_manual", nil
}

func TestATaskRecordsItsCheckpointAndACancelOffersTheRestore(t *testing.T) {
	block := blockingTool{started: make(chan struct{})}
	var told string
	h := newHarness(t, policy.Auto,
		fake.Calls("", fake.Call{Name: "install_package", Args: map[string]any{"manager": nil, "packages": []string{"nginx"}}}),
		func(req llm.Request) (llm.Response, error) {
			for _, out := range fake.LastOutputs(req) {
				told = out
			}
			return fake.Calls("", fake.Call{Name: "block", Args: map[string]any{}})(req)
		},
	)
	sw := &fakeSoftware{}
	h.m.Close()
	h.cfg.Tools = tool.NewRegistry(append(tool.SoftwareTools(), block)...)
	newEnv := h.cfg.NewEnv
	h.cfg.NewEnv = func(env *tool.Env) (func(), error) {
		env.Software = sw
		return newEnv(env)
	}
	h.m, _ = New(h.cfg)
	t.Cleanup(h.m.Close)

	created, _ := h.m.Create(context.Background(), "install nginx", 0, true)
	<-block.started
	if err := h.m.Cancel(context.Background(), created.Id); err != nil {
		t.Fatal(err)
	}
	task := h.waitSummary(t, created.Id, aosv1.TaskState_TASK_STATE_CANCELLED, "aos checkpoint restore c_1234")
	if task.CheckpointId != "c_1234" {
		t.Errorf("task %+v", task)
	}
	if len(sw.installs) != 1 || sw.installs[0] != "apt:nginx" {
		t.Errorf("installs %v", sw.installs)
	}
	for _, want := range []string{"installed nginx:arm64 1.24.0 (apt)", `Checkpoint c_1234 ("Before: install nginx")`} {
		if !strings.Contains(told, want) {
			t.Errorf("the Agent was not told %q:\n%s", want, told)
		}
	}
}
