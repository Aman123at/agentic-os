// Package openai implements llm.Provider with the OpenAI Responses API.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"

	"github.com/Aman123at/agentic-os/internal/llm"
)

// DefaultModel is aosd's built-in model (PLAN.md §6.4).
const DefaultModel = "gpt-5.6-terra"

// DefaultBaseURL is the OpenAI API.
const DefaultBaseURL = "https://api.openai.com/v1/"

// Provider calls the Responses API.
type Provider struct {
	// Key returns the API key; it is read when needed so a new key applies at once.
	Key func() string
	// BaseURL is an OpenAI-compatible endpoint; empty means DefaultBaseURL.
	BaseURL string
	// HTTPClient is the SDK's default when nil.
	HTTPClient *http.Client
	// MaxRetries is how often rate limits, server errors and timeouts are
	// retried, with backoff, before the request fails (AOS_MAX_RETRIES).
	MaxRetries int
}

// ErrNoKey means no API key is configured.
var ErrNoKey = errors.New("no OpenAI API key: set OPENAI_API_KEY in .env and run `docker compose up -d` again")

// Respond implements llm.Provider.
func (p *Provider) Respond(ctx context.Context, req llm.Request, onText func(string)) (llm.Response, error) {
	key := ""
	if p.Key != nil {
		key = strings.TrimSpace(p.Key())
	}
	if key == "" {
		return llm.Response{}, ErrNoKey
	}
	// The base URL is always explicit: the SDK would otherwise read
	// OPENAI_BASE_URL, which Compose sets even when it is empty.
	base := p.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	opts := []option.RequestOption{option.WithAPIKey(key), option.WithBaseURL(base), option.WithMaxRetries(p.MaxRetries)}
	if p.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(p.HTTPClient))
	}
	client := sdk.NewClient(opts...)

	body, err := requestBody(req)
	if err != nil {
		return llm.Response{}, err
	}
	var params responses.ResponseNewParams
	param.SetJSON(body, &params)
	stream := client.Responses.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()

	for stream.Next() {
		ev := stream.Current()
		switch ev.Type {
		case "response.output_text.delta":
			if onText != nil {
				onText(ev.Delta)
			}
		case "response.completed":
			return response(ev.Response)
		case "response.failed", "response.incomplete":
			msg := ev.Response.Error.Message
			if msg == "" {
				msg = ev.Response.IncompleteDetails.Reason
			}
			err := fmt.Errorf("the model response %s: %s", strings.TrimPrefix(ev.Type, "response."), msg)
			return llm.Response{}, transientIf(transientCode(string(ev.Response.Error.Code)), err)
		case "error":
			err := fmt.Errorf("the model returned an error: %s %s", ev.Code, ev.Message)
			return llm.Response{}, transientIf(transientCode(ev.Code), err)
		}
	}
	if err := stream.Err(); err != nil {
		var apiErr *sdk.Error
		if errors.As(err, &apiErr) {
			if apiErr.Code == "previous_response_not_found" {
				return llm.Response{}, llm.ErrPreviousResponseUnavailable
			}
			// Never include the request: only the status and the API's message.
			err := fmt.Errorf("OpenAI API: %d %s: %s", apiErr.StatusCode, apiErr.Code, apiErr.Message)
			// 429 also means an exhausted quota, which waiting doesn't fix.
			transient := apiErr.StatusCode == http.StatusTooManyRequests && apiErr.Code != "insufficient_quota" ||
				apiErr.StatusCode == http.StatusRequestTimeout || apiErr.StatusCode >= 500
			return llm.Response{}, transientIf(transient, err)
		}
		// A dropped connection or a timeout, unless the Task was cancelled.
		return llm.Response{}, transientIf(ctx.Err() == nil, err)
	}
	return llm.Response{}, transientIf(ctx.Err() == nil, errors.New("the model stream ended without a response"))
}

func transientIf(transient bool, err error) error {
	if transient {
		return &llm.TransientError{Err: err}
	}
	return err
}

// transientCode reports whether an error code in a stream means "try later".
func transientCode(code string) bool {
	return code == "server_error" || code == "rate_limit_exceeded" || strings.Contains(code, "timeout")
}

// requestBody builds the Responses API request as JSON.
func requestBody(req llm.Request) ([]byte, error) {
	body := map[string]any{
		"model":               req.Model,
		"input":               inputItems(req.Input),
		"store":               true,
		"parallel_tool_calls": true,
	}
	if req.Instructions != "" {
		body["instructions"] = req.Instructions
	}
	if req.PreviousResponseID != "" {
		body["previous_response_id"] = req.PreviousResponseID
	}
	if req.CacheKey != "" {
		body["prompt_cache_key"] = req.CacheKey
	}
	if req.ReasoningEffort != "" {
		body["reasoning"] = map[string]any{"effort": req.ReasoningEffort}
	}
	var tools []map[string]any
	for _, t := range req.Tools {
		if t.Hosted != "" {
			tools = append(tools, map[string]any{"type": t.Hosted})
			continue
		}
		tools = append(tools, map[string]any{"type": "function", "name": t.Name, "description": t.Description, "parameters": t.Parameters, "strict": true})
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	return json.Marshal(body)
}

func inputItems(items []llm.Item) []any {
	out := make([]any, 0, len(items))
	for _, it := range items {
		switch it.Type {
		case llm.Message:
			contentType := "input_text"
			if it.Role == "assistant" {
				contentType = "output_text"
			}
			out = append(out, map[string]any{"type": "message", "role": it.Role, "content": []any{map[string]any{"type": contentType, "text": it.Text}}})
		case llm.FunctionCall:
			out = append(out, map[string]any{"type": "function_call", "call_id": it.CallID, "name": it.Name, "arguments": it.Arguments})
		case llm.FunctionCallOutput:
			out = append(out, map[string]any{"type": "function_call_output", "call_id": it.CallID, "output": it.Output})
		case llm.Opaque:
			// Reasoning and hosted-tool items are only valid within the response
			// chain; a full transcript is sent without them.
		}
	}
	return out
}

// response converts a completed Responses API response.
func response(r responses.Response) (llm.Response, error) {
	out := llm.Response{ID: r.ID, Usage: llm.Usage{
		InputTokens:       r.Usage.InputTokens,
		CachedInputTokens: r.Usage.InputTokensDetails.CachedTokens,
		OutputTokens:      r.Usage.OutputTokens,
		ReasoningTokens:   r.Usage.OutputTokensDetails.ReasoningTokens,
	}}
	for _, item := range r.Output {
		switch item.Type {
		case "message":
			var text strings.Builder
			for _, c := range item.Content {
				switch c.Type {
				case "output_text":
					text.WriteString(c.Text)
				case "refusal":
					text.WriteString(c.Refusal)
				}
			}
			out.Output = append(out.Output, llm.Item{Type: llm.Message, Role: "assistant", Text: text.String()})
		case "function_call":
			out.Output = append(out.Output, llm.Item{Type: llm.FunctionCall, CallID: item.CallID, Name: item.Name, Arguments: item.Arguments.OfString})
		default:
			out.Output = append(out.Output, llm.Item{Type: llm.Opaque, Name: item.Type, Raw: json.RawMessage(item.RawJSON())})
		}
	}
	return out, nil
}
