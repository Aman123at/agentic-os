package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/amantiwari/agentic-os/internal/llm"
)

const dummyKey = "sk-test-dummy-not-a-real-key"

func sse(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		var v struct{ Type string }
		_ = json.Unmarshal([]byte(e), &v)
		var compact bytes.Buffer
		_ = json.Compact(&compact, []byte(e))
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", v.Type, compact.String())
	}
}

func TestRespondStreamsTextAndReturnsCallsAndUsage(t *testing.T) {
	var body map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		sse(w,
			`{"type":"response.output_text.delta","delta":"Hel","item_id":"msg_1","output_index":0,"content_index":0,"sequence_number":1}`,
			`{"type":"response.output_text.delta","delta":"lo","item_id":"msg_1","output_index":0,"content_index":0,"sequence_number":2}`,
			`{"type":"response.completed","sequence_number":3,"response":{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"gpt-test",
			  "output":[
			    {"type":"reasoning","id":"rs_1","summary":[]},
			    {"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello","annotations":[]}]},
			    {"type":"function_call","id":"fc_1","call_id":"call_1","name":"list_dir","arguments":"{\"path\":\"~\"}","status":"completed"}
			  ],
			  "usage":{"input_tokens":120,"input_tokens_details":{"cached_tokens":100},"output_tokens":30,"output_tokens_details":{"reasoning_tokens":12},"total_tokens":150}}}`,
		)
	}))
	defer srv.Close()

	p := &Provider{Key: func() string { return dummyKey }, BaseURL: srv.URL}
	var text strings.Builder
	resp, err := p.Respond(context.Background(), llm.Request{
		Model: "gpt-test", Instructions: "be brief", PreviousResponseID: "resp_0", CacheKey: "aos-agent", ReasoningEffort: "low",
		Tools: []llm.ToolSpec{{Name: "list_dir", Description: "List", Parameters: map[string]any{"type": "object"}}, {Name: "web_search", Hosted: "web_search"}},
		Input: []llm.Item{{Type: llm.FunctionCallOutput, CallID: "call_0", Output: "done"}},
	}, func(d string) { text.WriteString(d) })
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "Hello" || resp.ID != "resp_1" {
		t.Errorf("text %q, id %q", text.String(), resp.ID)
	}
	if len(resp.Output) != 3 || resp.Output[0].Type != llm.Opaque || resp.Output[1].Text != "Hello" ||
		resp.Output[2].Name != "list_dir" || resp.Output[2].CallID != "call_1" || resp.Output[2].Arguments != `{"path":"~"}` {
		t.Errorf("output %+v", resp.Output)
	}
	if resp.Usage != (llm.Usage{InputTokens: 120, CachedInputTokens: 100, OutputTokens: 30, ReasoningTokens: 12}) {
		t.Errorf("usage %+v", resp.Usage)
	}
	if auth != "Bearer "+dummyKey {
		t.Errorf("Authorization %q", auth)
	}
	for k, want := range map[string]any{"model": "gpt-test", "instructions": "be brief", "previous_response_id": "resp_0", "prompt_cache_key": "aos-agent", "stream": true, "store": true} {
		if body[k] != want {
			t.Errorf("request %s = %v, want %v", k, body[k], want)
		}
	}
	tools, _ := json.Marshal(body["tools"])
	if !strings.Contains(string(tools), `"strict":true`) || !strings.Contains(string(tools), `{"type":"web_search"}`) {
		t.Errorf("tools %s", tools)
	}
	input, _ := json.Marshal(body["input"])
	if string(input) != `[{"call_id":"call_0","output":"done","type":"function_call_output"}]` {
		t.Errorf("input %s", input)
	}
}

func TestAnExpiredPreviousResponseAsksForTheWholeTranscript(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"Previous response with id 'resp_0' not found.","type":"invalid_request_error","param":"previous_response_id","code":"previous_response_not_found"}}`)
	}))
	defer srv.Close()
	p := &Provider{Key: func() string { return dummyKey }, BaseURL: srv.URL}
	_, err := p.Respond(context.Background(), llm.Request{Model: "m", PreviousResponseID: "resp_0"}, nil)
	if !errors.Is(err, llm.ErrPreviousResponseUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if _, err := (&Provider{Key: func() string { return "" }}).Respond(context.Background(), llm.Request{}, nil); !errors.Is(err, ErrNoKey) {
		t.Errorf("without a key: %v", err)
	}
}

// roundTrip answers every request in-process.
type roundTrip func(*http.Request) *http.Response

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r), nil }

// Compose sets OPENAI_BASE_URL even when it is empty; the SDK must not take
// that as the base URL.
func TestAnEmptyBaseURLVariableStillReachesTheOpenAIAPI(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	var got string
	client := &http.Client{Transport: roundTrip(func(r *http.Request) *http.Response {
		got = r.URL.String()
		rec := httptest.NewRecorder()
		sse(rec, `{"type":"response.completed","sequence_number":1,"response":{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"m","output":[],
		  "usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)
		return rec.Result()
	})}
	p := &Provider{Key: func() string { return dummyKey }, HTTPClient: client}
	if _, err := p.Respond(context.Background(), llm.Request{Model: "m"}, nil); err != nil {
		t.Fatal(err)
	}
	if want := DefaultBaseURL + "responses"; got != want {
		t.Errorf("the request went to %q, want %q", got, want)
	}
}
