package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/coder/websocket"

	"github.com/Aman123at/agentic-os/internal/browser"
)

// control is a text frame from a viewer; binary frames are keystrokes.
type control struct {
	Type string `json:"type"` // "resize"
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// sessionSocket attaches a viewer to a Session (PLAN.md §10): binary frames carry
// output to the viewer and keystrokes back.
func (s *Server) sessionSocket(w http.ResponseWriter, r *http.Request) {
	term, err := s.Sessions.Attach(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	// Origin was checked by Auth.TCP; the socket path needs no browser check.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	recent, output, stop := term.Watch()
	defer stop()
	go func() {
		defer cancel()
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			switch typ {
			case websocket.MessageBinary:
				_ = term.Write(data)
			case websocket.MessageText:
				var c control
				if json.Unmarshal(data, &c) == nil && c.Type == "resize" && c.Cols > 0 && c.Rows > 0 {
					_ = term.Resize(c.Cols, c.Rows)
				}
			}
		}
	}()
	if len(recent) > 0 {
		if err := conn.Write(ctx, websocket.MessageBinary, recent); err != nil {
			return
		}
	}
	for {
		select {
		case chunk, ok := <-output:
			if !ok {
				_ = conn.Close(websocket.StatusNormalClosure, "the Session ended")
				return
			}
			// A slow viewer blocks here, which applies backpressure to this viewer only.
			if err := conn.Write(ctx, websocket.MessageBinary, chunk); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// browserUnavailable is what /ws/browser answers when the Browser isn't in this Machine.
const browserUnavailable = "the Browser is not included: set INCLUDE_BROWSER=true in .env, then run docker compose up --build"

// browserSocket attaches a viewer to the Browser's page (PLAN.md M5.2): binary
// frames to the viewer are JPEG screenshots of the page; text frames carry the
// toolbar state and notices one way and Commands the other.
func (s *Server) browserSocket(w http.ResponseWriter, r *http.Request) {
	if s.Browser == nil {
		http.Error(w, browserUnavailable, http.StatusNotFound)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	// A pasted text is the largest thing a viewer sends.
	conn.SetReadLimit(1 << 17)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	send := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return conn.Write(ctx, websocket.MessageText, b)
	}
	v, err := s.Browser.Attach(ctx)
	if err != nil {
		_ = send(map[string]string{"type": "notice", "message": err.Error()})
		_ = conn.Close(websocket.StatusInternalError, "the browser didn't start")
		return
	}
	defer s.Browser.Detach(v)

	go func() {
		defer cancel()
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var c browser.Command
			if typ != websocket.MessageText || json.Unmarshal(data, &c) != nil {
				continue
			}
			if err := v.Do(ctx, c); err != nil {
				if errors.Is(err, browser.ErrClosed) {
					return
				}
				if ctx.Err() == nil {
					v.Notice(err.Error())
				}
			}
		}
	}()
	for {
		u, err := v.Next(ctx)
		if err != nil {
			if errors.Is(err, browser.ErrClosed) {
				_ = conn.Close(websocket.StatusNormalClosure, "the browser stopped")
			}
			return
		}
		if u.State != nil {
			if send(struct {
				Type string `json:"type"`
				*browser.State
			}{"state", u.State}) != nil {
				return
			}
		}
		for _, n := range u.Notices {
			if send(map[string]string{"type": "notice", "message": n}) != nil {
				return
			}
		}
		if u.Frame != nil {
			// A slow viewer holds up only its own frames; the page waits for
			// the fastest viewer (Viewer.FrameSent) before sending the next.
			if conn.Write(ctx, websocket.MessageBinary, u.Frame) != nil {
				return
			}
			v.FrameSent()
		}
	}
}
