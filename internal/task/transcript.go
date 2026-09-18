package task

import (
	"encoding/json"

	"github.com/Aman123at/agentic-os/internal/llm"
)

// cutShort is the output of a call that never finished because AOS stopped:
// the model needs an output for every call.
const cutShort = "This call was cut short when AOS stopped; its outcome is unknown."

// restartNote tells a resumed Agent what happened (PLAN.md §8.1).
const restartNote = "AOS restarted while you were working on this Task, which interrupted it. " +
	"Background processes you started have stopped and your Session was reset. " +
	"Check the Machine's current state (files, installed software, running processes) before continuing, " +
	"and don't assume a step finished unless you can verify it."

// buildTranscript rebuilds a Task's conversation from the model items stored
// with its steps, in order. The calls of one response come first and their
// outputs after them, as the model sent and received them.
func buildTranscript(stored []string) []llm.Item {
	var items []llm.Item
	for _, s := range stored {
		var its []llm.Item
		if s != "" && json.Unmarshal([]byte(s), &its) == nil {
			items = append(items, its...)
		}
	}
	answered := map[string]bool{}
	for _, it := range items {
		if it.Type == llm.FunctionCallOutput {
			answered[it.CallID] = true
		}
	}
	var out, calls, outputs []llm.Item
	flush := func() {
		out = append(append(out, calls...), outputs...)
		calls, outputs = nil, nil
	}
	for _, it := range items {
		switch it.Type {
		case llm.FunctionCall:
			calls = append(calls, it)
			if !answered[it.CallID] {
				outputs = append(outputs, llm.Item{Type: llm.FunctionCallOutput, CallID: it.CallID, Output: cutShort})
				answered[it.CallID] = true
			}
		case llm.FunctionCallOutput:
			outputs = append(outputs, it)
		default:
			flush()
			out = append(out, it)
		}
	}
	flush()
	return out
}
