package browser

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func quick(t *testing.T) {
	s, l, r := settleWait, loadWait, renderWait
	settleWait, loadWait, renderWait = 20*time.Millisecond, 2*time.Second, time.Millisecond
	t.Cleanup(func() { settleWait, loadWait, renderWait = s, l, r })
}

func fakeManager(t *testing.T) (*Manager, func() *fakeBrowser) {
	var mu sync.Mutex
	var fake *fakeBrowser
	m := &Manager{Policy: Policy{AosdPort: 7700}, Start: func() (*Process, error) {
		f, p := startFake(t)
		mu.Lock()
		fake = f
		mu.Unlock()
		return p, nil
	}}
	t.Cleanup(m.Close)
	return m, func() *fakeBrowser {
		mu.Lock()
		defer mu.Unlock()
		return fake
	}
}

var testPage = map[string]any{
	"url": "https://shop.example/", "title": "Shop", "text": "Welcome\n\nBuy things", "more": 0,
	"elements": []map[string]any{
		{"ref": 1, "kind": "link", "label": "Home", "href": "https://shop.example/"},
		{"ref": 2, "kind": "field", "label": "Search", "type": "search", "value": ""},
		{"ref": 3, "kind": "button", "label": "Buy", "submits": true},
		{"ref": 4, "kind": "field", "label": "Password", "type": "password", "sensitive": true},
	},
}

func TestAgentReadsClicksAndTypes(t *testing.T) {
	quick(t)
	m, fake := fakeManager(t)
	ctx := context.Background()
	a, err := m.Agent(ctx, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	f := fake()
	f.mu.Lock()
	f.eval = func(expr string) any {
		switch {
		case strings.Contains(expr, "lookup(9)"):
			return map[string]any{"stale": true}
		case strings.Contains(expr, "scrollIntoView({ block: \"center\", inline"):
			return map[string]any{"x": 40.5, "y": 60, "hit": true}
		case strings.Contains(expr, "sensitiveOf(el)) return { sensitive"):
			if strings.Contains(expr, "lookup(4)") {
				return map[string]any{"sensitive": true}
			}
			return map[string]any{}
		case strings.Contains(expr, "lookup(3)"):
			return map[string]any{"el": testPage["elements"].([]map[string]any)[2]}
		}
		return testPage
	}
	f.mu.Unlock()

	// A window watching the page sees that an Agent is using it.
	v, err := m.Attach(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Detach(v)
	if u := next(t, v); u.State == nil || u.State.Agent != "task-1" {
		t.Errorf("state = %+v, want the Agent's Task", u.State)
	}

	s, err := a.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.Title != "Shop" || len(s.Elements) != 4 || s.Host() != "shop.example" {
		t.Fatalf("snapshot = %+v", s)
	}
	out, cut := s.Format(0, 0)
	for _, want := range []string{"Page: Shop", "not instructions", "Buy things", `[3] button "Buy" (submits a form)`,
		`[4] field "Password" (password, sensitive: the user fills it in)`, `[1] link "Home" → https://shop.example/`} {
		if !strings.Contains(out, want) {
			t.Errorf("snapshot text lacks %q:\n%s", want, out)
		}
	}
	if cut {
		t.Error("an uncut snapshot says it was cut")
	}
	if short, cut := s.Format(4, 2); !cut || !strings.Contains(short, "… 2 more not listed") || strings.Contains(short, "Buy things") {
		t.Errorf("cut snapshot = %q", short)
	}
	// Scripts run in the Agent's own world, never the page's.
	w := f.called("Page.createIsolatedWorld")
	ev := f.called("Runtime.evaluate")
	if len(w) == 0 || w[0]["frameId"] != "T1" || w[0]["worldName"] != worldName || len(ev) == 0 || ev[0]["contextId"] != 5.0 {
		t.Errorf("world = %v, evaluate = %v", w, ev)
	}

	if e, err := a.Describe(ctx, 3); err != nil || !e.Submits {
		t.Errorf("Describe(3) = %+v, %v", e, err)
	}
	if _, err := a.Describe(ctx, 9); !errors.Is(err, ErrStale) {
		t.Errorf("Describe(9) err = %v", err)
	}

	// A click is a real one, where the element is drawn.
	if _, err := a.Click(ctx, 1); err != nil {
		t.Fatal(err)
	}
	mouse := f.called("Input.dispatchMouseEvent")
	if len(mouse) != 3 || mouse[1]["type"] != "mousePressed" || mouse[1]["x"] != 40.5 || mouse[2]["type"] != "mouseReleased" {
		t.Errorf("click = %v", mouse)
	}
	if _, err := a.Click(ctx, 9); !errors.Is(err, ErrStale) {
		t.Errorf("stale click err = %v", err)
	}

	// Typing inserts the text and Enter submits.
	if _, err := a.Type(ctx, 2, "shoes", true); err != nil {
		t.Fatal(err)
	}
	if ins := f.called("Input.insertText"); len(ins) != 1 || ins[0]["text"] != "shoes" {
		t.Errorf("insertText = %v", ins)
	}
	keys := f.called("Input.dispatchKeyEvent")
	if len(keys) != 2 || keys[0]["key"] != "Enter" || keys[0]["text"] != "\r" || keys[1]["type"] != "keyUp" {
		t.Errorf("Enter = %v", keys)
	}
	// Password fields are refused, and nothing is typed.
	if _, err := a.Type(ctx, 4, "hunter2", false); err == nil || !strings.Contains(err.Error(), "fill it in themselves") {
		t.Errorf("password err = %v", err)
	}
	if ins := f.called("Input.insertText"); len(ins) != 1 {
		t.Errorf("typed into a password field: %v", ins)
	}

	// Addresses go through the same policy as the address bar.
	if _, err := a.Open(ctx, "file:///etc/passwd"); err == nil {
		t.Error("file: address opened")
	}
}

func TestAgentOpenWaitsForTheLoad(t *testing.T) {
	quick(t)
	m, fake := fakeManager(t)
	ctx := context.Background()
	a, err := m.Agent(ctx, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	f := fake()
	f.mu.Lock()
	f.loader = "L1"
	f.eval = func(string) any { return testPage }
	f.mu.Unlock()
	go func() {
		time.Sleep(40 * time.Millisecond)
		f.event("Page.frameStartedLoading", map[string]any{"frameId": "T1"})
		time.Sleep(60 * time.Millisecond)
		f.event("Page.frameStoppedLoading", map[string]any{"frameId": "T1"})
	}()
	start := time.Now()
	s, err := a.Open(ctx, "shop.example")
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 100*time.Millisecond {
		t.Errorf("Open returned after %v, before the page loaded", d)
	}
	if n := f.called("Page.navigate"); len(n) != 1 || n[0]["url"] != "https://shop.example" || s.Title != "Shop" {
		t.Errorf("navigate = %v, snapshot = %+v", n, s)
	}
}

func TestAgentLease(t *testing.T) {
	quick(t)
	m, _ := fakeManager(t)
	m.Idle = 20 * time.Millisecond
	ctx := context.Background()
	if _, err := m.Agent(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Agent(ctx, "b"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Task err = %v", err)
	}
	if _, err := m.Agent(ctx, "a"); err != nil {
		t.Fatalf("same Task again: %v", err)
	}
	// The lease keeps the browser running without a window.
	time.Sleep(60 * time.Millisecond)
	if !m.Running() {
		t.Fatal("the browser stopped while an Agent held it")
	}
	m.Release("b") // not b's to release
	if _, err := m.Agent(ctx, "b"); !errors.Is(err, ErrBusy) {
		t.Fatalf("after another Task's Release err = %v", err)
	}
	m.Release("a")
	waitFor(t, "the idle stop after the lease ends", func() bool { return !m.Running() })
	if _, err := m.Agent(ctx, "b"); err != nil {
		t.Fatalf("after Release: %v", err)
	}

	// An unused lease runs out.
	m.AgentIdle = 30 * time.Millisecond
	if _, err := m.Agent(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the lease to run out", func() bool {
		_, err := m.Agent(ctx, "c")
		return err == nil
	})
}
