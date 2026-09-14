// Package fake is a scripted model provider: tests and recorded cassettes replay
// conversations deterministically, at no cost (PLAN.md §17).
package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/amantiwari/agentic-os/internal/llm"
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

// Cassette is a recorded conversation: one entry per model turn.
type Cassette []struct {
	Text  string `json:"text,omitempty"`
	Calls []Call `json:"calls,omitempty"`
}

// Load reads a cassette file.
func Load(path string) (*Provider, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Cassette
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("cassette %s: %w", path, err)
	}
	turns := make([]Turn, len(c))
	for i, t := range c {
		turns[i] = Calls(t.Text, t.Calls...)
	}
	return New(turns...), nil
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
