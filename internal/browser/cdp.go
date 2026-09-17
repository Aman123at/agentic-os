package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// conn speaks the DevTools protocol over --remote-debugging-pipe: JSON
// messages, each ended by a NUL byte. Replies are matched to calls by id;
// everything else is an event, handed to onEvent in arrival order.
//
// onEvent runs on the reader goroutine, so it must never wait for a call's
// reply: the reply could only be read by the goroutine that is waiting.
type conn struct {
	w       io.Writer
	wmu     sync.Mutex
	next    atomic.Int64
	onEvent func(session, method string, params json.RawMessage)

	mu      sync.Mutex
	pending map[int64]chan reply
	err     error
	done    chan struct{}
}

type reply struct {
	result json.RawMessage
	err    error
}

type message struct {
	ID        int64           `json:"id,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func newConn(w io.Writer, r io.Reader, onEvent func(session, method string, params json.RawMessage)) *conn {
	c := &conn{w: w, onEvent: onEvent, pending: map[int64]chan reply{}, done: make(chan struct{})}
	go c.read(r)
	return c
}

func (c *conn) read(r io.Reader) {
	br := bufio.NewReaderSize(r, 1<<16)
	var err error
	for {
		var b []byte
		if b, err = br.ReadBytes(0); err != nil {
			break
		}
		var m message
		if json.Unmarshal(b[:len(b)-1], &m) != nil {
			continue
		}
		if m.ID == 0 {
			if m.Method != "" && c.onEvent != nil {
				c.onEvent(m.SessionID, m.Method, m.Params)
			}
			continue
		}
		c.mu.Lock()
		ch := c.pending[m.ID]
		delete(c.pending, m.ID)
		c.mu.Unlock()
		if ch == nil {
			continue
		}
		if m.Error != nil {
			ch <- reply{err: fmt.Errorf("%s (%d)", m.Error.Message, m.Error.Code)}
		} else {
			ch <- reply{result: m.Result}
		}
	}
	if errors.Is(err, io.EOF) {
		err = errors.New("the browser closed its DevTools pipe")
	}
	c.mu.Lock()
	c.err = err
	for id, ch := range c.pending {
		ch <- reply{err: err}
		delete(c.pending, id)
	}
	c.mu.Unlock()
	close(c.done)
}

// call sends method and waits for its reply, decoding the result into out
// when out is non-nil. session is "" for the browser itself.
func (c *conn) call(ctx context.Context, session, method string, params, out any) error {
	id := c.next.Add(1)
	m := struct {
		ID        int64  `json:"id"`
		SessionID string `json:"sessionId,omitempty"`
		Method    string `json:"method"`
		Params    any    `json:"params,omitempty"`
	}{id, session, method, params}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	ch := make(chan reply, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.pending[id] = ch
	c.mu.Unlock()

	c.wmu.Lock()
	_, err = c.w.Write(append(b, 0))
	c.wmu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return fmt.Errorf("%s: %w", method, r.err)
		}
		if out != nil {
			return json.Unmarshal(r.result, out)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	}
}
