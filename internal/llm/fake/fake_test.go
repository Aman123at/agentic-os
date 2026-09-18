package fake

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Aman123at/agentic-os/internal/llm"
)

func TestTheLibraryContinuesAWholeTranscriptAfterItsResponses(t *testing.T) {
	dir := t.TempDir()
	cassette := `{"prompt":"e2e-resume","turns":[{"calls":[{"name":"run_command","arguments":{"command":"sleep 300"}}]},{"text":"Done after the restart."}]}`
	if err := os.WriteFile(filepath.Join(dir, "resume.json"), []byte(cassette), 0o644); err != nil {
		t.Fatal(err)
	}
	l := &Library{Dir: dir}
	first, err := l.Respond(context.Background(), llm.Request{Input: []llm.Item{llm.UserMessage("e2e-resume: wait")}}, nil)
	if err != nil || len(first.Output) != 1 || first.Output[0].Name != "run_command" {
		t.Fatalf("first turn %+v, %v", first, err)
	}
	// After a restart the whole transcript arrives, without a previous response.
	transcript := append([]llm.Item{llm.UserMessage("e2e-resume: wait")}, first.Output...)
	transcript = append(transcript, llm.Item{Type: llm.FunctionCallOutput, CallID: first.Output[0].CallID, Output: "cut short"}, llm.DeveloperMessage("AOS restarted"))
	second, err := l.Respond(context.Background(), llm.Request{Input: transcript}, nil)
	if err != nil || len(second.Output) != 1 || second.Output[0].Text != "Done after the restart." {
		t.Fatalf("second turn %+v, %v", second, err)
	}
}
