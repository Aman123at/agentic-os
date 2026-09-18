// Package fake is a scripted model provider: tests and recorded cassettes replay
// conversations deterministically, at no cost (PLAN.md §17).
package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Aman123at/agentic-os/internal/llm"
)

// Turn produces the model's response to one request.
type Turn func(req llm.Request) (llm.Response, error)

// Provider replays turns in order and records every request.
type Provider struct {
	mu       sync.Mutex
	turns    []Turn
	requests []llm.Request
}

// New returns a Provider that answers with turns, one per request.
func New(turns ...Turn) *Provider { return &Provider{turns: turns} }

// Requests returns the requests received so far.
func (p *Provider) Requests() []llm.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]llm.Request{}, p.requests...)
}

// Respond implements llm.Provider. Text is streamed word by word.
func (p *Provider) Respond(ctx context.Context, req llm.Request, onText func(string)) (llm.Response, error) {
	p.mu.Lock()
	n := len(p.requests)
	p.requests = append(p.requests, req)
	var turn Turn
	if n < len(p.turns) {
		turn = p.turns[n]
	}
	p.mu.Unlock()
	if turn == nil {
		return llm.Response{}, fmt.Errorf("fake provider: no turn scripted for request %d", n+1)
	}
	if err := ctx.Err(); err != nil {
		return llm.Response{}, err
	}
	resp, err := turn(req)
	if err != nil {
		return llm.Response{}, err
	}
	if resp.ID == "" {
		resp.ID = fmt.Sprintf("resp_fake_%d", n+1)
	}
	for _, item := range resp.Output {
		if item.Type == llm.Message && onText != nil {
			for _, w := range strings.SplitAfter(item.Text, " ") {
				onText(w)
			}
		}
	}
	return resp, nil
}

// Say answers with text only, which ends the Task.
func Say(text string) Turn {
	return func(llm.Request) (llm.Response, error) {
		return llm.Response{Output: []llm.Item{{Type: llm.Message, Role: "assistant", Text: text}}}, nil
	}
}

// Call is one function call in a scripted turn.
type Call struct {
	Name string         `json:"name"`
	Args map[string]any `json:"arguments"`
}

// Calls answers with optional text followed by function calls.
func Calls(text string, calls ...Call) Turn {
	return func(req llm.Request) (llm.Response, error) {
		var out []llm.Item
		if text != "" {
			out = append(out, llm.Item{Type: llm.Message, Role: "assistant", Text: text})
		}
		for i, c := range calls {
			args, err := json.Marshal(c.Args)
			if err != nil {
				return llm.Response{}, err
			}
			out = append(out, llm.Item{Type: llm.FunctionCall, CallID: fmt.Sprintf("call_%d_%d", len(req.Input), i+1), Name: c.Name, Arguments: string(args)})
		}
		return llm.Response{Output: out, Usage: llm.Usage{InputTokens: 100, OutputTokens: 20}}, nil
	}
}

// Cassette is a recorded conversation for a Task prompt: one entry per model turn.
type Cassette struct {
	// Prompt selects the cassette: a Task whose prompt contains it plays these turns.
	Prompt string `json:"prompt"`
	Turns  []struct {
		Text  string `json:"text,omitempty"`
		Calls []Call `json:"calls,omitempty"`
	} `json:"turns"`
}

// Library plays cassettes from a folder of JSON files, choosing one per Task by
// its prompt, so one aosd can replay many scenarios. Files are read when a Task
// starts, so cassettes can be added while aosd runs.
type Library struct {
	Dir string

	mu   sync.Mutex
	next map[string]libraryTurn // response id → the cassette and turn that follow
	n    int
}

type libraryTurn struct {
	cassette *Cassette
	turn     int
}

// Respond implements llm.Provider.
func (l *Library) Respond(ctx context.Context, req llm.Request, onText func(string)) (llm.Response, error) {
	l.mu.Lock()
	if l.next == nil {
		l.next = map[string]libraryTurn{}
	}
	pos, ok := l.next[req.PreviousResponseID]
	l.mu.Unlock()
	if req.PreviousResponseID == "" || !ok {
		prompt := ""
		for _, it := range req.Input {
			if it.Type == llm.Message && it.Role == "user" {
				prompt = it.Text
				break
			}
		}
		c, err := l.find(prompt)
		if err != nil {
			return llm.Response{}, err
		}
		// A whole transcript (a Follow-up or Resume) continues after the
		// responses it already holds.
		pos = libraryTurn{cassette: c, turn: responsesIn(req.Input)}
	}
	if pos.turn >= len(pos.cassette.Turns) {
		return llm.Response{}, fmt.Errorf("fake provider: cassette %q has no turn %d", pos.cassette.Prompt, pos.turn+1)
	}
	t := pos.cassette.Turns[pos.turn]
	resp, err := New(Calls(t.Text, t.Calls...)).Respond(ctx, req, onText)
	if err != nil {
		return resp, err
	}
	l.mu.Lock()
	l.n++
	resp.ID = fmt.Sprintf("resp_cassette_%d", l.n)
	l.next[resp.ID] = libraryTurn{cassette: pos.cassette, turn: pos.turn + 1}
	l.mu.Unlock()
	return resp, nil
}

func (l *Library) find(prompt string) (*Cassette, error) {
	paths, err := filepath.Glob(filepath.Join(l.Dir, "*.json"))
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var c Cassette
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, fmt.Errorf("cassette %s: %w", p, err)
		}
		if c.Prompt != "" && strings.Contains(prompt, c.Prompt) {
			return &c, nil
		}
	}
	return nil, fmt.Errorf("fake provider: no cassette in %s matches the Task %q", l.Dir, prompt)
}

// responsesIn counts the model responses in a transcript: runs of assistant
// messages, function calls and opaque items.
func responsesIn(items []llm.Item) int {
	n, inResponse := 0, false
	for _, it := range items {
		model := it.Type == llm.FunctionCall || it.Type == llm.Opaque || it.Type == llm.Message && it.Role == "assistant"
		if model && !inResponse {
			n++
		}
		inResponse = model
	}
	return n
}

// LastOutputs returns the function call outputs in a request, by call id.
func LastOutputs(req llm.Request) map[string]string {
	out := map[string]string{}
	for _, it := range req.Input {
		if it.Type == llm.FunctionCallOutput {
			out[it.CallID] = it.Output
		}
	}
	return out
}
