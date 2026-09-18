package software

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Aman123at/agentic-os/internal/tool"
)

// Tools gives Agents the Manager (tool.Software).
type Tools struct{ M *Manager }

var _ tool.Software = Tools{}

func agentCall(taskID, title string) Call {
	return Call{TaskID: taskID, TaskTitle: title, Actor: "agent"}
}

func (t Tools) Install(ctx context.Context, taskID, title, manager string, packages []string) (tool.SoftwareResult, error) {
	out, err := t.M.Install(ctx, agentCall(taskID, title), manager, packages)
	return result(out), err
}

func (t Tools) Remove(ctx context.Context, taskID, title, manager string, packages []string) (tool.SoftwareResult, error) {
	out, err := t.M.Remove(ctx, agentCall(taskID, title), manager, packages)
	return result(out), err
}

func (t Tools) RunAsRoot(ctx context.Context, taskID, title, command, dir string, timeout time.Duration) (tool.CommandResult, tool.SoftwareResult, error) {
	res, out, err := t.M.RunAsRoot(ctx, agentCall(taskID, title), command, dir, timeout)
	return res, result(out), err
}

func (t Tools) Checkpoint(ctx context.Context, taskID, name string) (string, error) {
	cp, err := t.M.Checkpoint(ctx, Call{TaskID: taskID, Actor: "agent"}, name)
	if err != nil {
		return "", err
	}
	return cp.Id, nil
}

func result(o Outcome) tool.SoftwareResult {
	r := tool.SoftwareResult{Output: o.Output, Waited: o.Waited, Changes: o.Describe()}
	if o.Checkpoint != nil {
		r.Checkpoint, r.CheckpointName = o.Checkpoint.Id, o.Checkpoint.Name
	}
	return r
}

// Describe says what the operation changed, one line per top-level package,
// with dependencies and files counted.
func (o Outcome) Describe() []string {
	if o.Op == nil {
		return nil
	}
	var lines []string
	deps, files := 0, 0
	for _, c := range o.Op.Changes {
		switch c.Kind {
		case KindPackage:
			var before, after Pkg
			_ = json.Unmarshal([]byte(c.Before), &before)
			_ = json.Unmarshal([]byte(c.After), &after)
			if before.Auto || after.Auto {
				deps++
				continue
			}
			switch {
			case c.Before == "":
				lines = append(lines, fmt.Sprintf("installed %s %s (%s)", c.Name, after.Version, c.Manager))
			case c.After == "":
				lines = append(lines, fmt.Sprintf("removed %s %s (%s)", c.Name, before.Version, c.Manager))
			case before.Version != after.Version:
				lines = append(lines, fmt.Sprintf("changed %s %s → %s (%s)", c.Name, before.Version, after.Version, c.Manager))
			}
		case KindFile:
			files++
		case KindService:
			lines = append(lines, "changed Service "+c.Name)
		}
	}
	if deps > 0 {
		lines = append(lines, fmt.Sprintf("%d dependencies changed", deps))
	}
	if files > 0 {
		lines = append(lines, fmt.Sprintf("%d paths under /etc changed", files))
	}
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return lines
}
