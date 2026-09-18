package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/Aman123at/agentic-os/internal/policy"
)

// MaxNotifications is how many notifications one Task may send, so an Agent
// in a loop cannot bury the user's own.
const MaxNotifications = 5

// Notice is a notification an Agent sends. Port, if set, offers an Open button
// for the page a program in the Machine serves on it.
type Notice struct {
	Title, Body string
	Port        int
}

// Desktop is a Task's way to the Desktop. Tools find it nil in cli Mode.
type Desktop struct {
	Notify func(ctx context.Context, n Notice) error
	// Open shows a file or folder in the Desktop.
	Open func(ctx context.Context, path string, dir bool) error

	notified atomic.Int32
}

// DesktopTools returns the Desktop group.
func DesktopTools() []Tool {
	return []Tool{notify{}, openInDesktop{}}
}

const noDesktop = "There is no Desktop in cli Mode, so nothing was shown. Tell the user in your answer instead."

type notify struct{}

func (notify) Spec() Spec {
	return Spec{Name: "notify", Description: fmt.Sprintf("Show the user a notification in the Desktop, for something they should know while they are busy elsewhere, such as a long job finishing. At most %d per Task; your final answer is shown anyway.", MaxNotifications),
		Parameters: object(map[string]any{
			"title": str("A few words"),
			"body":  optStr("One or two sentences"),
		})}
}

func (notify) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct{ Title, Body string }
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Title) == "" {
		return nil, errors.New("title is empty")
	}
	n := Notice{Title: oneLine(a.Title, 80), Body: oneLine(a.Body, 500)}
	return &Call{Summary: "Notify: " + n.Title, Policy: policy.Call{Tool: "notify", Coordination: true},
		Run: func(ctx context.Context, r Run) Result {
			d := env.Desktop
			if d == nil || d.Notify == nil {
				return Result{Output: noDesktop}
			}
			if d.notified.Add(1) > MaxNotifications {
				return Errorf("Not sent: this Task reached its limit of %d notifications. Put anything else in your final answer.", MaxNotifications)
			}
			if err := d.Notify(ctx, n); err != nil {
				return Errorf("%v", err)
			}
			return Result{Output: "Sent the notification."}
		}}, nil
}

type openInDesktop struct{}

func (openInDesktop) Spec() Spec {
	return Spec{Name: "open_in_desktop", Description: "Show the user a result in the Desktop: open a file or folder (give path), or offer a button that opens the web page a program in this Machine serves (give port). It can't open websites on the internet.",
		Parameters: object(map[string]any{
			"path": optStr("A file or folder in the Machine"),
			"port": optInt("A port a program in the Machine listens on, for its web page"),
		})}
}

func (openInDesktop) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct {
		Path string
		Port int
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	switch {
	case a.Path != "" && a.Port != 0, a.Path == "" && a.Port == 0:
		return nil, errors.New("give either path or port")
	case a.Port != 0 && (a.Port < 1 || a.Port > 65535):
		return nil, fmt.Errorf("port %d is not a valid port", a.Port)
	case strings.Contains(a.Path, "://"):
		return nil, fmt.Errorf("%s is a web address, and open_in_desktop only opens files, folders and ports in the Machine; give the user the link in your answer", a.Path)
	}
	call := &Call{Policy: policy.Call{Tool: "open_in_desktop", Coordination: true}}
	if a.Port != 0 {
		call.Summary = fmt.Sprintf("Offer to open port %d", a.Port)
		call.Run = func(ctx context.Context, r Run) Result {
			d := env.Desktop
			if d == nil || d.Notify == nil {
				return Result{Output: noDesktop}
			}
			n := Notice{Title: fmt.Sprintf("Open port %d", a.Port), Body: fmt.Sprintf("The Agent offers a page served on port %d.", a.Port), Port: a.Port}
			if err := d.Notify(ctx, n); err != nil {
				return Errorf("%v", err)
			}
			return Result{Output: fmt.Sprintf("The user got a notification with an Open button for port %d; the page opens when they click it.", a.Port)}
		}
		return call, nil
	}
	path := env.Abs(a.Path)
	call.Summary = "Open in the Desktop: " + env.Display(path)
	call.Run = func(ctx context.Context, r Run) Result {
		d := env.Desktop
		if d == nil || d.Open == nil {
			return Result{Output: noDesktop}
		}
		exists, dir := env.stat(path)
		if !exists {
			return Errorf("no such file or folder: %s", env.Display(path))
		}
		if err := d.Open(ctx, path, dir); err != nil {
			return Errorf("%v", err)
		}
		return Result{Output: "Opened " + env.Display(path) + " in the Desktop."}
	}
	return call, nil
}
