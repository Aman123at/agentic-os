package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"
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
