package browser

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	p := Policy{AosdPort: 7700}
	for _, tc := range []struct{ in, want, err string }{
		{"https://example.com/a?b=c", "https://example.com/a?b=c", ""},
		{"example.com", "https://example.com", ""},
		{"example.com/path?q=1", "https://example.com/path?q=1", ""},
		{"localhost:8000", "http://localhost:8000", ""},
		{"127.0.0.1:3000/x", "http://127.0.0.1:3000/x", ""},
		{"8000.localhost:7700", "", "inside its own Browser"},
		{"about:blank", Blank, ""},
		{"how do tides work", SearchURL + "how+do+tides+work", ""},
		{"golang", SearchURL + "golang", ""},
		{"a/b c", SearchURL + "a%2Fb+c", ""},
		{"file:///etc/passwd", "", "only opens web pages"},
		{"FILE:/etc/passwd", "", "only opens web pages"},
		{"javascript:alert(1)", "", "only opens web pages"},
		{"data:text/html,hi", "", "only opens web pages"},
		{"chrome://settings", "", "only opens web pages"},
		{"ftp://example.com", "", "only opens web pages"},
		{"http://localhost:7700/", "", "inside its own Browser"},
		{"http://[::1]:7700", "", "inside its own Browser"},
		{"http://localhost:7701", "http://localhost:7701", ""},
		{"http://example.com:7700", "http://example.com:7700", ""},
		{"   ", "", "type an address"},
	} {
		got, err := p.Normalize(tc.in)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("Normalize(%q) err = %v, want %q", tc.in, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestInputCall(t *testing.T) {
	m, p, err := inputCall(Command{Type: "mouse", Event: "down", X: 10, Y: 20, Button: "left", Buttons: 1, Clicks: 9, Modifiers: 0xff})
	if err != nil || m != "Input.dispatchMouseEvent" || p["type"] != "mousePressed" || p["clickCount"] != 3 || p["modifiers"] != 0xf {
		t.Errorf("mouse down = %s %v %v", m, p, err)
	}
	_, p, _ = inputCall(Command{Type: "wheel", X: -5, Y: 1e9, DeltaY: 1e9})
	if p["x"] != 0.0 || p["y"] != float64(maxSide) || p["deltaY"] != 10000.0 {
		t.Errorf("wheel not clamped: %v", p)
	}
	_, p, _ = inputCall(Command{Type: "key", Event: "down", Key: "a", Code: "KeyA", Text: "a", KeyCode: 65})
	if p["type"] != "keyDown" || p["text"] != "a" || p["windowsVirtualKeyCode"] != 65 {
		t.Errorf("key a = %v", p)
	}
	_, p, _ = inputCall(Command{Type: "key", Event: "down", Key: "a", KeyCode: 65, Modifiers: 4, Commands: []string{"selectAll", "runEvilThing"}})
	if p["type"] != "rawKeyDown" || len(p["commands"].([]string)) != 1 {
		t.Errorf("⌘A = %v", p)
	}
	_, p, _ = inputCall(Command{Type: "text", Text: "héllo"})
	if p["text"] != "héllo" {
		t.Errorf("text = %v", p)
	}
	for _, bad := range []Command{
		{Type: "mouse", Event: "explode"},
		{Type: "mouse", Event: "down", Button: "fourth"},
		{Type: "key", Event: "press"},
		{Type: "text"},
		{Type: "Runtime.evaluate"},
	} {
		if _, _, err := inputCall(bad); err == nil {
			t.Errorf("inputCall(%+v) accepted", bad)
		}
	}
}

func TestLimit(t *testing.T) {
	if got := limit("héllo", 2); got != "h" {
		t.Errorf("limit cut inside a rune: %q", got)
	}
}

// fakeBrowser answers DevTools calls over an in-memory pipe and records them.
type fakeBrowser struct {
	t      *testing.T
	in     *io.PipeReader // what aosd sends
	out    *io.PipeWriter // what the browser sends
	wmu    sync.Mutex
	mu     sync.Mutex
	calls  []string
	params map[string][]map[string]any
	done   chan struct{}
	once   sync.Once
	// eval answers Runtime.evaluate; loader is Page.navigate's loaderId.
	eval   func(expr string) any
	loader string
}

func startFake(t *testing.T) (*fakeBrowser, *Process) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	f := &fakeBrowser{t: t, in: inR, out: outW, params: map[string][]map[string]any{}, done: make(chan struct{})}
	go f.serve()
	return f, &Process{In: inW, Out: outR, Done: f.done, Stop: f.stop}
}

func (f *fakeBrowser) stop() {
	f.once.Do(func() {
		_ = f.in.Close()
		_ = f.out.Close()
		close(f.done)
	})
}

func (f *fakeBrowser) send(v any) {
	b, _ := json.Marshal(v)
	f.wmu.Lock()
	defer f.wmu.Unlock()
	_, _ = f.out.Write(append(b, 0))
}

func (f *fakeBrowser) event(method string, params any) {
	f.send(map[string]any{"sessionId": "S1", "method": method, "params": params})
}

func (f *fakeBrowser) serve() {
	br := bufio.NewReader(f.in)
	for {
		b, err := br.ReadBytes(0)
		if err != nil {
			return
		}
		var m struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(b[:len(b)-1], &m) != nil {
			f.t.Errorf("bad message %q", b)
			continue
		}
		f.mu.Lock()
		f.calls = append(f.calls, m.Method)
		f.params[m.Method] = append(f.params[m.Method], m.Params)
		f.mu.Unlock()
		result := map[string]any{}
		switch m.Method {
		case "Target.createTarget":
			result["targetId"] = "T1"
		case "Target.attachToTarget":
			result["sessionId"] = "S1"
		case "Page.createIsolatedWorld":
			result["executionContextId"] = 5
		case "Runtime.evaluate":
			f.mu.Lock()
			eval := f.eval
			f.mu.Unlock()
			if eval != nil {
				expr, _ := m.Params["expression"].(string)
				result["result"] = map[string]any{"type": "object", "value": eval(expr)}
			}
		case "Page.navigate":
			f.mu.Lock()
			if f.loader != "" {
				result["loaderId"] = f.loader
			}
			f.mu.Unlock()
		case "Page.getNavigationHistory":
			result["currentIndex"] = 1
			result["entries"] = []map[string]any{{"id": 10}, {"id": 11}}
		case "Browser.close":
			f.send(map[string]any{"id": m.ID, "result": result})
			f.stop()
			return
		}
		f.send(map[string]any{"id": m.ID, "result": result})
	}
}

func (f *fakeBrowser) called(method string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.params[method]
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func next(t *testing.T, v *Viewer) Update {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	u, err := v.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	return u
}

func TestManagerStreamsAndStops(t *testing.T) {
	var fake *fakeBrowser
	starts := 0
	m := &Manager{Policy: Policy{AosdPort: 7700}, Downloads: "/home/aos/Downloads", Idle: 50 * time.Millisecond,
		Start: func() (*Process, error) {
			starts++
			var p *Process
			fake, p = startFake(t)
			return p, nil
		}}
	defer m.Close()
	ctx := context.Background()
	v, err := m.Attach(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if u := next(t, v); u.State == nil || u.State.URL != Blank {
		t.Fatalf("first update = %+v", u)
	}
	if d := fake.called("Browser.setDownloadBehavior"); len(d) != 1 || d[0]["downloadPath"] != "/home/aos/Downloads" {
		t.Errorf("downloads = %v", d)
	}
	// A visible viewer starts the screencast; a frame reaches it and is acked
	// only once the viewer has sent it.
	waitFor(t, "the screencast", func() bool { return len(fake.called("Page.startScreencast")) == 1 })
	fake.event("Page.screencastFrame", map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("JPEG")), "sessionId": 7})
	u := next(t, v)
	if string(u.Frame) != "JPEG" {
		t.Fatalf("frame = %q", u.Frame)
	}
	time.Sleep(20 * time.Millisecond)
	if len(fake.called("Page.screencastFrameAck")) != 0 {
		t.Error("frame acked before the viewer sent it")
	}
	v.FrameSent()
	v.FrameSent()
	waitFor(t, "the ack", func() bool { return len(fake.called("Page.screencastFrameAck")) == 1 })
	time.Sleep(20 * time.Millisecond)
	if acks := fake.called("Page.screencastFrameAck"); len(acks) != 1 || acks[0]["sessionId"] != 7.0 {
		t.Errorf("acks = %v", acks)
	}

	// Navigation goes through the policy; the page's own navigations update the toolbar.
	if err := v.Do(ctx, Command{Type: "navigate", URL: "example.com"}); err != nil {
		t.Fatal(err)
	}
	if n := fake.called("Page.navigate"); len(n) != 1 || n[0]["url"] != "https://example.com" {
		t.Errorf("navigate = %v", n)
	}
	if err := v.Do(ctx, Command{Type: "navigate", URL: "file:///etc/passwd"}); err == nil {
		t.Error("file: URL accepted")
	}
	fake.event("Page.frameNavigated", map[string]any{"frame": map[string]any{"id": "T1", "url": "https://example.com/"}})
	fake.send(map[string]any{"method": "Target.targetInfoChanged", "params": map[string]any{"targetInfo": map[string]any{"targetId": "T1", "type": "page", "title": "Example", "url": "https://example.com/"}}})
	waitFor(t, "the toolbar state", func() bool {
		ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		u, _ := v.Next(ctx)
		return u.State != nil && u.State.Title == "Example" && u.State.CanBack && !u.State.CanForward
	})

	// A page that failed to load keeps its address and shows Chromium's error page.
	fake.event("Page.frameNavigated", map[string]any{"frame": map[string]any{"id": "T1", "url": "chrome-error://chromewebdata/"}})
	time.Sleep(30 * time.Millisecond)
	if n := fake.called("Page.navigate"); len(n) != 1 {
		t.Errorf("an error page was turned away: %v", n)
	}

	// A redirect to a refused scheme is turned away with a notice.
	fake.event("Page.frameNavigated", map[string]any{"frame": map[string]any{"id": "T1", "url": "file:///etc/passwd"}})
	waitFor(t, "the refusal", func() bool {
		ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		u, _ := v.Next(ctx)
		return len(u.Notices) > 0 && strings.Contains(u.Notices[0], "only opens web pages")
	})
	waitFor(t, "the way back to about:blank", func() bool {
		n := fake.called("Page.navigate")
		return len(n) == 2 && n[1]["url"] == Blank
	})

	// Back goes to the previous history entry.
	if err := v.Do(ctx, Command{Type: "back"}); err != nil {
		t.Fatal(err)
	}
	if h := fake.called("Page.navigateToHistoryEntry"); len(h) != 1 || h[0]["entryId"] != 10.0 {
		t.Errorf("back = %v", h)
	}

	// Hidden: the screencast stops. Resize is clamped.
	if err := v.Do(ctx, Command{Type: "visible", Visible: false}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the screencast to stop", func() bool { return len(fake.called("Page.stopScreencast")) == 1 })
	if err := v.Do(ctx, Command{Type: "resize", Width: 10, Height: 99999, Scale: 3}); err != nil {
		t.Fatal(err)
	}
	em := fake.called("Emulation.setDeviceMetricsOverride")
	if last := em[len(em)-1]; last["width"] != float64(minSide) || last["height"] != float64(maxSide) || last["deviceScaleFactor"] != float64(maxScale) {
		t.Errorf("resize = %v", last)
	}

	// No viewers for Idle: the browser stops; the next viewer starts another.
	m.Detach(v)
	waitFor(t, "the idle stop", func() bool { return !m.Running() })
	if _, err := v.Next(ctx); err != ErrClosed {
		t.Errorf("Next after stop = %v", err)
	}
	v2, err := m.Attach(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if starts != 2 {
		t.Errorf("starts = %d, want 2", starts)
	}
	m.Detach(v2)
}

func TestReattachCancelsIdleStop(t *testing.T) {
	m := &Manager{Idle: 30 * time.Millisecond, Start: func() (*Process, error) {
		_, p := startFake(t)
		return p, nil
	}}
	defer m.Close()
	v, err := m.Attach(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m.Detach(v)
	v2, err := m.Attach(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if !m.Running() {
		t.Fatal("the browser stopped while a viewer was attached")
	}
	m.Detach(v2)
}

func TestPopupOpensInThePage(t *testing.T) {
	var fake *fakeBrowser
	m := &Manager{Start: func() (*Process, error) {
		var p *Process
		fake, p = startFake(t)
		return p, nil
	}}
	defer m.Close()
	v, err := m.Attach(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Detach(v)
	fake.send(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": map[string]any{"targetId": "T2", "type": "page", "url": "https://example.org/"}}})
	fake.send(map[string]any{"method": "Target.targetInfoChanged", "params": map[string]any{"targetInfo": map[string]any{"targetId": "T2", "type": "page", "url": "https://example.org/"}}})
	waitFor(t, "the popup to move into the page", func() bool {
		n := fake.called("Page.navigate")
		return len(n) == 1 && n[0]["url"] == "https://example.org/" && len(fake.called("Target.closeTarget")) == 1
	})
	time.Sleep(20 * time.Millisecond)
	if n := fake.called("Target.closeTarget"); len(n) != 1 {
		t.Errorf("closeTarget called %d times", len(n))
	}
}
