package service

import (
	"context"
	"errors"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
	"github.com/amantiwari/agentic-os/internal/tool"
)

// Tools gives Agents the Supervisor (tool.Services).
type Tools struct {
	S *Supervisor
	// Checkpoint takes the Task's Checkpoint before its first change, if it
	// has none yet: creating a Service is a change a Restore undoes.
	Checkpoint func(ctx context.Context, taskID, title string) (id, name string, created bool, err error)
}

var _ tool.Services = Tools{}

func (t Tools) checkpoint(ctx context.Context, taskID, title string) (tool.ServiceChange, error) {
	var c tool.ServiceChange
	if t.Checkpoint == nil || taskID == "" {
		return c, nil
	}
	id, name, created, err := t.Checkpoint(ctx, taskID, title)
	if created {
		c.Checkpoint, c.CheckpointName = id, name
	}
	return c, err
}

func (t Tools) Create(ctx context.Context, taskID, title string, d tool.ServiceDefinition) (tool.ServiceChange, error) {
	c, err := t.checkpoint(ctx, taskID, title)
	if err != nil {
		return c, err
	}
	info, err := t.S.Create(ctx, Definition{Name: d.Name, Command: d.Command, Dir: d.Dir, Env: d.Env, Root: d.Root,
		Autostart: d.Autostart, Restart: d.Restart, TaskID: taskID}, "agent")
	if err != nil {
		return c, err
	}
	c.Status = Status(info)
	return c, nil
}

func (t Tools) Remove(ctx context.Context, taskID, title, name string) (tool.ServiceChange, error) {
	if _, err := t.S.Get(name); err != nil {
		return tool.ServiceChange{}, err
	}
	c, err := t.checkpoint(ctx, taskID, title)
	if err != nil {
		return c, err
	}
	return c, t.S.Remove(ctx, name, "agent", taskID)
}

func (t Tools) Start(_ context.Context, name string) (tool.ServiceStatus, error) {
	return status(t.S.Start(name))
}

func (t Tools) Stop(_ context.Context, name string) (tool.ServiceStatus, error) {
	return status(t.S.Stop(name))
}

func (t Tools) Restart(_ context.Context, name string) (tool.ServiceStatus, error) {
	return status(t.S.Restart(name))
}

func (t Tools) Status(name string) ([]tool.ServiceStatus, error) {
	t.S.Scan() // the Agent may have just started a server: current ports
	if name != "" {
		s, err := status(t.S.Get(name))
		if err != nil {
			return nil, err
		}
		return []tool.ServiceStatus{s}, nil
	}
	var out []tool.ServiceStatus
	for _, i := range t.S.List() {
		out = append(out, Status(i))
	}
	return out, nil
}

func (t Tools) Logs(name string, limit int) (string, error) {
	l, err := t.S.Logs(name)
	if err != nil {
		return "", err
	}
	return string(l.Tail(limit)), nil
}

func status(i *aosv1.ServiceInfo, err error) (tool.ServiceStatus, error) {
	if err != nil {
		return tool.ServiceStatus{}, err
	}
	return Status(i), nil
}

// Status converts a Service's description for the Tools.
func Status(i *aosv1.ServiceInfo) tool.ServiceStatus {
	s := tool.ServiceStatus{Name: i.Name, Command: i.Command, LastExit: i.LastExit, PID: int(i.Pid), Restarts: int(i.Restarts), Root: i.Root, State: StateName(i.State)}
	for _, p := range i.Ports {
		s.Ports = append(s.Ports, int(p))
	}
	return s
}

// StateName is a Service state in words.
func StateName(s aosv1.ServiceState) string {
	switch s {
	case aosv1.ServiceState_SERVICE_STATE_RUNNING:
		return "running"
	case aosv1.ServiceState_SERVICE_STATE_RESTARTING:
		return "restarting"
	case aosv1.ServiceState_SERVICE_STATE_FAILED:
		return "failed"
	}
	return "stopped"
}

// IsNotFound reports whether err is about an unknown Service.
func IsNotFound(err error) bool { return errors.Is(err, ErrNoService) }
