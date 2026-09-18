package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Aman123at/agentic-os/internal/browser"
)

type fakePage struct {
	els    map[int]browser.Element
	opened []string
	typed  []string
	clicks []int
}

func (f *fakePage) snap() browser.Snapshot {
	return browser.Snapshot{URL: "https://shop.example/cart", Title: "Cart", Text: strings.Repeat("words ", 5000)}
}
func (f *fakePage) Open(_ context.Context, in string) (browser.Snapshot, error) {
	f.opened = append(f.opened, in)
	return f.snap(), nil
}
func (f *fakePage) Read(context.Context) (browser.Snapshot, error) { return f.snap(), nil }
func (f *fakePage) Back(context.Context) (browser.Snapshot, error) { return f.snap(), nil }
func (f *fakePage) URL() string                                    { return "https://shop.example/cart" }
func (f *fakePage) Describe(_ context.Context, ref int) (browser.Element, error) {
	e, ok := f.els[ref]
	if !ok {
		return e, browser.ErrStale
	}
	return e, nil
}
func (f *fakePage) Click(_ context.Context, ref int) (browser.Snapshot, error) {
	f.clicks = append(f.clicks, ref)
	return f.snap(), nil
}
func (f *fakePage) Type(_ context.Context, ref int, text string, submit bool) (browser.Snapshot, error) {
	f.typed = append(f.typed, text)
	return f.snap(), nil
}

func prepare(t *testing.T, tl Tool, env *Env, args string) (*Call, error) {
	t.Helper()
	return tl.Prepare(context.Background(), env, json.RawMessage(args))
}

func TestBrowserTools(t *testing.T) {
	page := &fakePage{els: map[int]browser.Element{
		1: {Ref: 1, Kind: "link", Label: "Home"},
		2: {Ref: 2, Kind: "field", Label: "Coupon"},
		3: {Ref: 3, Kind: "button", Label: "Pay now", Submits: true},
		4: {Ref: 4, Kind: "field", Label: "Password", Type: "password", Sensitive: true},
	}}
	shows := 0
	busy := false
	env := &Env{TaskID: "t1", Outputs: &Outputs{Dir: t.TempDir()}, Browser: &Browser{
		Page: func(context.Context) (BrowserPage, error) {
			if busy {
				return nil, browser.ErrBusy
			}
			return page, nil
		},
		Show: func(context.Context) { shows++ },
	}}
	ctx := context.Background()

	// Opening shows the window once per Task and returns the page, cut to size
	// with the rest in read_output.
	c, err := prepare(t, browserOpen{}, env, `{"url":"github.com"}`)
	if err != nil || c.Policy.Risky || c.Policy.Folder != "github.com" {
		t.Fatalf("open = %+v, %v", c, err)
	}
	res := c.Run(ctx, Run{StepID: "s1"})
	if res.Error || !strings.Contains(res.Output, "Page: Cart") || res.OutputRef == "" || !strings.Contains(res.Output, "read_output") {
		t.Errorf("open result = %+v", res)
	}
	if len(res.Output) > browserText+2<<10 {
		t.Errorf("open result is %d bytes", len(res.Output))
	}
	if c, _ := prepare(t, browserRead{}, env, `{}`); c.Run(ctx, Run{StepID: "s2"}).Error {
		t.Error("read failed")
	}
	if shows != 1 || len(page.opened) != 1 {
		t.Errorf("shows = %d, opened = %v", shows, page.opened)
	}

	// A link is an ordinary click; a submit button is Risky, per site.
	if c, err := prepare(t, browserClick{}, env, `{"ref":1}`); err != nil || c.Policy.Risky || c.Policy.Folder != "shop.example" {
		t.Errorf("link click = %+v, %v", c, err)
	}
	c, err = prepare(t, browserClick{}, env, `{"ref":3}`)
	if err != nil || !c.Policy.Risky || c.Policy.Tool != "browser_click" || c.Policy.Folder != "shop.example" ||
		!strings.Contains(c.Summary, `button "Pay now" on shop.example`) || !strings.Contains(c.Policy.Reasons[0], "submits a form") {
		t.Fatalf("submit click = %+v, %v", c, err)
	}
	c.Run(ctx, Run{})
	if len(page.clicks) != 1 || page.clicks[0] != 3 {
		t.Errorf("clicks = %v", page.clicks)
	}
	if _, err := prepare(t, browserClick{}, env, `{"ref":9}`); !errors.Is(err, browser.ErrStale) {
		t.Errorf("stale ref err = %v", err)
	}
	if _, err := prepare(t, browserClick{}, env, `{"ref":0}`); err == nil {
		t.Error("ref 0 accepted")
	}

	// Typing is Risky only when it submits; password fields are refused.
	if c, err := prepare(t, browserType{}, env, `{"ref":2,"text":"SAVE10","submit":null}`); err != nil || c.Policy.Risky {
		t.Errorf("type = %+v, %v", c, err)
	}
	c, err = prepare(t, browserType{}, env, `{"ref":2,"text":"SAVE10","submit":true}`)
	if err != nil || !c.Policy.Risky || !strings.HasSuffix(c.Summary, "and press Enter") {
		t.Errorf("type and submit = %+v, %v", c, err)
	}
	if _, err := prepare(t, browserType{}, env, `{"ref":4,"text":"hunter2","submit":false}`); err == nil || !strings.Contains(err.Error(), "fill it in themselves") {
		t.Errorf("password err = %v", err)
	}
	if len(page.typed) != 0 {
		t.Errorf("typed = %v", page.typed)
	}

	// Another Task holding the Browser is an error the model can read.
	busy = true
	c, _ = prepare(t, browserRead{}, env, `{}`)
	if res := c.Run(ctx, Run{}); !res.Error || !strings.Contains(res.Output, "another Task") {
		t.Errorf("busy = %+v", res)
	}
	if _, err := prepare(t, browserClick{}, env, `{"ref":1}`); !errors.Is(err, browser.ErrBusy) {
		t.Errorf("busy click err = %v", err)
	}

	// Without the Browser, the Tools say so.
	c, _ = prepare(t, browserOpen{}, &Env{}, `{"url":"github.com"}`)
	if res := c.Run(ctx, Run{}); !res.Error || !strings.Contains(res.Output, "isn't available") {
		t.Errorf("no Browser = %+v", res)
	}
}
