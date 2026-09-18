package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Aman123at/agentic-os/internal/browser"
)

func TestBrowserSocketNeedsSignInAndTheBrowser(t *testing.T) {
	auth, _ := newAuth(t)
	h := auth.TCP((&Server{Auth: auth}).Handler())
	upgrade := map[string]string{"Upgrade": "websocket", "Connection": "Upgrade", "Origin": "http://localhost:7700"}
	if rec := do(t, h, "GET", "/ws/browser", upgrade); rec.Code != http.StatusUnauthorized {
		t.Errorf("without credentials: %d, want 401", rec.Code)
	}
	upgrade["Authorization"] = "Bearer " + testToken
	rec := do(t, h, "GET", "/ws/browser", upgrade)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "INCLUDE_BROWSER=true") {
		t.Errorf("without the Browser: %d %q, want 404 naming INCLUDE_BROWSER", rec.Code, rec.Body.String())
	}
}

// devtools answers every DevTools call with an empty result, except the two
// that name the page, and sends one frame once the screencast starts.
func devtools(t *testing.T) *browser.Process {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = inR.Close()
			_ = outW.Close()
			close(done)
		})
	}
	var wmu sync.Mutex
	send := func(v any) {
		b, _ := json.Marshal(v)
		wmu.Lock()
		defer wmu.Unlock()
		_, _ = outW.Write(append(b, 0))
	}
	go func() {
		br := bufio.NewReader(inR)
		for {
			b, err := br.ReadBytes(0)
			if err != nil {
				return
			}
			var m struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			_ = json.Unmarshal(b[:len(b)-1], &m)
			result := map[string]any{}
			switch m.Method {
			case "Target.createTarget":
				result["targetId"] = "T1"
			case "Target.attachToTarget":
				result["sessionId"] = "S1"
			}
			send(map[string]any{"id": m.ID, "result": result})
			if m.Method == "Page.startScreencast" {
				send(map[string]any{"sessionId": "S1", "method": "Page.screencastFrame", "params": map[string]any{"data": "SlBFRw==", "sessionId": 1}})
			}
			if m.Method == "Browser.close" {
				stop()
				return
			}
		}
	}()
	return &browser.Process{In: inW, Out: outR, Done: done, Stop: stop}
}

func TestBrowserSocketStreamsTheState(t *testing.T) {
	auth, _ := newAuth(t)
	mgr := &browser.Manager{Policy: browser.Policy{AosdPort: 7700}, Start: func() (*browser.Process, error) { return devtools(t), nil }}
	defer mgr.Close()
	srv := httptest.NewServer((&Server{Auth: auth, Browser: mgr}).Handler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/browser", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()

	var gotState, gotFrame bool
	for !gotState || !gotFrame {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v (state %v, frame %v)", err, gotState, gotFrame)
		}
		if typ == websocket.MessageBinary {
			gotFrame = string(data) == "JPEG"
			continue
		}
		var msg map[string]any
		_ = json.Unmarshal(data, &msg)
		if msg["type"] == "state" && msg["url"] == browser.Blank {
			gotState = true
		}
	}

	// A refused address comes back as a notice, not a closed socket.
	_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"navigate","url":"file:///etc/shadow"}`))
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if typ == websocket.MessageText && strings.Contains(string(data), `"notice"`) {
			if !strings.Contains(string(data), "only opens web pages") {
				t.Errorf("notice = %s", data)
			}
			break
		}
	}
}
