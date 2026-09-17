package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/amantiwari/agentic-os/internal/browser"
	"github.com/amantiwari/agentic-os/internal/policy"
)

// BrowserPage is the Desktop Browser's page as a Task uses it (browser.Agent).
type BrowserPage interface {
	Open(ctx context.Context, input string) (browser.Snapshot, error)
	Read(ctx context.Context) (browser.Snapshot, error)
	Describe(ctx context.Context, ref int) (browser.Element, error)
	Click(ctx context.Context, ref int) (browser.Snapshot, error)
	Type(ctx context.Context, ref int, text string, submit bool) (browser.Snapshot, error)
	Back(ctx context.Context) (browser.Snapshot, error)
	// URL is the page's address now.
	URL() string
}

// Browser is a Task's way to the Desktop's Browser (PLAN.md M5.3). Tools find
// it nil unless the Machine includes the Browser.
type Browser struct {
	// Page gives the Task the page, or says why it can't have it now.
	Page func(ctx context.Context) (BrowserPage, error)
	// Show opens the Browser window in the Desktop, so the user watches.
	Show func(ctx context.Context)

	shown atomic.Bool
}

// page gets the page and, the first time in a Task, shows the window.
func (b *Browser) page(ctx context.Context) (BrowserPage, error) {
	if b == nil || b.Page == nil {
		return nil, errors.New("the Browser isn't available in this Machine")
	}
	p, err := b.Page(ctx)
	if err != nil {
		return nil, err
	}
	if b.Show != nil && b.shown.CompareAndSwap(false, true) {
		b.Show(ctx)
	}
	return p, nil
}

// BrowserTools returns the Browser group.
func BrowserTools() []Tool {
	return []Tool{browserOpen{}, browserRead{}, browserClick{}, browserType{}, browserBack{}}
}

// How much of a page a result shows; the whole page goes to read_output.
const (
	browserText     = 12 << 10
	browserElements = 150
)

func snapshotResult(env *Env, r Run, s browser.Snapshot, err error) Result {
	if err != nil {
		return Errorf("%v", err)
	}
	short, cut := s.Format(browserText, browserElements)
	if !cut || env.Outputs == nil {
		return Result{Output: short}
	}
	full, _ := s.Format(0, 0)
	name, f, err := env.Outputs.Create(env.TaskID, r.StepID+".out")
	if err != nil {
		return Result{Output: short}
	}
	_, _ = f.WriteString(full)
	_ = f.Close()
	return Result{Output: fmt.Sprintf("%s\n[This is part of the page; read_output ref=%q offset=0 shows all %d bytes.]", short, name, len(full)), OutputRef: name}
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

// ---------------------------------------------------------------- browser_open

type browserOpen struct{}

func (browserOpen) Spec() Spec {
	return Spec{Name: "browser_open", Description: "Open a web page in the Desktop's Browser, where the user watches: an address (github.com, https://…, localhost:8000 for the Machine's own servers) or words to search for. Waits for the page to load and returns its text and numbered interactive elements. Use it when the user asks to open or use a website in the browser; use http_request to just fetch something.",
		Parameters: object(map[string]any{
			"url": str("An address, or words to search for"),
		})}
}

func (browserOpen) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct{ URL string }
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.URL) == "" {
		return nil, errors.New("url is empty")
	}
	// The host it will open, for confirm-all grants; the page checks the address itself.
	host := ""
	if u, err := (browser.Policy{}).Normalize(a.URL); err == nil {
		host = hostOf(u)
	}
	return &Call{Summary: "Open in the Browser: " + oneLine(a.URL, 160), Policy: policy.Call{Tool: "browser_open", Folder: host},
		Run: func(ctx context.Context, r Run) Result {
			p, err := env.Browser.page(ctx)
			if err != nil {
				return Errorf("%v", err)
			}
			s, err := p.Open(ctx, a.URL)
			return snapshotResult(env, r, s, err)
		}}, nil
}

// ---------------------------------------------------------------- browser_read

type browserRead struct{}

func (browserRead) Spec() Spec {
	return Spec{Name: "browser_read", Description: "Read the page open in the Desktop's Browser again: its text and numbered interactive elements. Numbers from an earlier read stop working once the page changes.",
		Parameters: object(map[string]any{})}
}

func (browserRead) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct{}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	return &Call{Summary: "Read the Browser's page", Policy: policy.Call{Tool: "browser_read"},
		Run: func(ctx context.Context, r Run) Result {
			p, err := env.Browser.page(ctx)
			if err != nil {
				return Errorf("%v", err)
			}
			s, err := p.Read(ctx)
			return snapshotResult(env, r, s, err)
		}}, nil
}

// ---------------------------------------------------------------- browser_back

type browserBack struct{}

func (browserBack) Spec() Spec {
	return Spec{Name: "browser_back", Description: "Go back to the previous page in the Desktop's Browser and read it.",
		Parameters: object(map[string]any{})}
}

func (browserBack) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct{}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	return &Call{Summary: "Go back in the Browser", Policy: policy.Call{Tool: "browser_back"},
		Run: func(ctx context.Context, r Run) Result {
			p, err := env.Browser.page(ctx)
			if err != nil {
				return Errorf("%v", err)
			}
			s, err := p.Back(ctx)
			return snapshotResult(env, r, s, err)
		}}, nil
}

// element looks up ref on the page for Prepare, which classifies the call.
func element(ctx context.Context, env *Env, ref int) (browser.Element, string, error) {
	if ref < 1 {
		return browser.Element{}, "", errors.New("ref is an element number from browser_open or browser_read")
	}
	p, err := env.Browser.page(ctx)
	if err != nil {
		return browser.Element{}, "", err
	}
	e, err := p.Describe(ctx, ref)
	if err != nil {
		return browser.Element{}, "", err
	}
	return e, hostOf(p.URL()), nil
}

// ---------------------------------------------------------------- browser_click

type browserClick struct{}

func (browserClick) Spec() Spec {
	return Spec{Name: "browser_click", Description: "Click an element on the Browser's page, by its number from browser_open or browser_read, and read the page that follows. A click that submits a form is a Risky Action.",
		Parameters: object(map[string]any{
			"ref": map[string]any{"type": "integer", "description": "The element's number"},
		})}
}

func (browserClick) Prepare(ctx context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct{ Ref int }
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	e, host, err := element(ctx, env, a.Ref)
	if err != nil {
		return nil, err
	}
	c := &Call{Summary: fmt.Sprintf("Click [%d] %s %q on %s", a.Ref, e.Kind, oneLine(e.Label, 60), host),
		Policy: policy.Call{Tool: "browser_click", Folder: host}}
	if e.Submits {
		c.Policy.Risky = true
		c.Policy.Reasons = []string{"submits a form on " + host}
	}
	c.Run = func(ctx context.Context, r Run) Result {
		p, err := env.Browser.page(ctx)
		if err != nil {
			return Errorf("%v", err)
		}
		s, err := p.Click(ctx, a.Ref)
		return snapshotResult(env, r, s, err)
	}
	return c, nil
}

// ---------------------------------------------------------------- browser_type

type browserType struct{}

func (browserType) Spec() Spec {
	return Spec{Name: "browser_type", Description: "Type into a field on the Browser's page, by its number from browser_open or browser_read, replacing what it holds (in a list, pick the option with this text). submit presses Enter afterwards, which usually sends the form, and is then a Risky Action. Never type passwords, card numbers or codes: ask the user to fill those in themselves.",
		Parameters: object(map[string]any{
			"ref":    map[string]any{"type": "integer", "description": "The field's number"},
			"text":   str("What to type"),
			"submit": optBool("Press Enter afterwards"),
		})}
}

func (browserType) Prepare(ctx context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct {
		Ref    int
		Text   string
		Submit bool
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	e, host, err := element(ctx, env, a.Ref)
	if err != nil {
		return nil, err
	}
	if e.Sensitive {
		return nil, fmt.Errorf("[%d] is a password, card or code field: ask the user to fill it in themselves in the Browser", a.Ref)
	}
	summary := fmt.Sprintf("Type %q into [%d] %s %q on %s", oneLine(a.Text, 60), a.Ref, e.Kind, oneLine(e.Label, 60), host)
	if a.Submit {
		summary += " and press Enter"
	}
	c := &Call{Summary: summary, Policy: policy.Call{Tool: "browser_type", Folder: host}}
	if a.Submit {
		c.Policy.Risky = true
		c.Policy.Reasons = []string{"sends what it typed to " + host}
	}
	c.Run = func(ctx context.Context, r Run) Result {
		p, err := env.Browser.page(ctx)
		if err != nil {
			return Errorf("%v", err)
		}
		s, err := p.Type(ctx, a.Ref, a.Text, a.Submit)
		return snapshotResult(env, r, s, err)
	}
	return c, nil
}
