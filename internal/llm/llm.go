// Package llm is the model provider interface (PLAN.md §4.1). The openai package
// implements it with the Responses API; fake replays scripted conversations.
package llm

import (
	"context"
	"encoding/json"
	"errors"
)

// ItemType is the kind of a conversation item.
type ItemType string

const (
	Message            ItemType = "message"
	FunctionCall       ItemType = "function_call"
	FunctionCallOutput ItemType = "function_call_output"
	// Opaque items (reasoning, hosted web searches) are kept as Raw and sent back as they are.
	Opaque ItemType = "opaque"
)

// Item is one entry of a transcript: model input and output alike.
type Item struct {
	Type ItemType `json:"type"`
	// Message
	Role string `json:"role,omitempty"` // user | assistant | developer
	Text string `json:"text,omitempty"`
	// FunctionCall and FunctionCallOutput
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
	// Opaque
	Raw json.RawMessage `json:"raw,omitempty"`
}

// UserMessage returns a user message item.
func UserMessage(text string) Item { return Item{Type: Message, Role: "user", Text: text} }

// DeveloperMessage returns a message from AOS to the model, such as the
// Machine Profile or the note that AOS restarted.
func DeveloperMessage(text string) Item { return Item{Type: Message, Role: "developer", Text: text} }

// ToolSpec describes a Tool to the model.
type ToolSpec struct {
	Name        string
	Description string
	// Parameters is a JSON Schema object.
	Parameters map[string]any
	// Hosted names a provider-side tool ("web_search") instead of a function.
	Hosted string
}

// Request is one model turn.
type Request struct {
	Model           string
	ReasoningEffort string
	Instructions    string
	Tools           []ToolSpec
	// Input is the whole transcript, or only the items after PreviousResponseID.
	Input              []Item
	PreviousResponseID string
	// CacheKey groups requests that share a prompt prefix, for prompt caching.
	CacheKey string
}

// Usage counts tokens of one response.
type Usage struct {
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	ReasoningTokens   int64
}

// Response is a finished model turn.
type Response struct {
	ID     string
	Output []Item
	Usage  Usage
}

// Provider runs model turns. onText receives assistant text as it streams.
type Provider interface {
	Respond(ctx context.Context, req Request, onText func(delta string)) (Response, error)
}

// ErrPreviousResponseUnavailable means PreviousResponseID can't be used; the
// caller sends the whole transcript instead.
var ErrPreviousResponseUnavailable = errors.New("previous response unavailable")

// TransientError is a model request that failed for a reason that may pass: a
// rate limit, a server error, a timeout or a dropped connection. The provider
// has already retried it with backoff (PLAN.md §8.3).
type TransientError struct{ Err error }

func (e *TransientError) Error() string { return e.Err.Error() }
func (e *TransientError) Unwrap() error { return e.Err }

// IsTransient reports whether err is a TransientError.
func IsTransient(err error) bool {
	var t *TransientError
	return errors.As(err, &t)
}
