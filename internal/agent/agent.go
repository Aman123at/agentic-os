// Package agent is the Agent loop (PLAN.md §8.2): it asks the model for the next
// step, runs the Tool calls it requests through policy, and repeats until the
// model gives a final answer.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
	"github.com/amantiwari/agentic-os/internal/audit"
	"github.com/amantiwari/agentic-os/internal/llm"
	"github.com/amantiwari/agentic-os/internal/policy"
	"github.com/amantiwari/agentic-os/internal/tool"
)

// Config is how Agents think.
type Config struct {
	Provider        llm.Provider
	Tools           *tool.Registry
	Model           string
	ReasoningEffort string
	Instructions    string
	// MaxRetries is AOS_MAX_RETRIES: attempts allowed after a failure (PLAN.md §8.3).
	MaxRetries int
}

// replyHint ends what a paused Task tells the user.
// cancelled is what a Tool call shows once the Task has been cancelled: the
// same sentence wherever the cancel lands, never a Go error.
const cancelled = "Cancelled."

const replyHint = `Reply with a hint, or "try another way", and the Agent continues; or cancel the Task.`

// Host is the Task an Agent works on: it records steps and decides Approvals.
type Host interface {
	// AddStep records a new step standing for the given model items.
	AddStep(ctx context.Context, s *aosv1.TaskStep, items []llm.Item) error
	// UpdateStep saves a changed step.
	UpdateStep(ctx context.Context, s *aosv1.TaskStep, items []llm.Item) error
	TextDelta(stepID, delta string)
	// Responded records a finished model response (usage, response id).
	Responded(ctx context.Context, resp llm.Response) error
	Env() *tool.Env
	Decide(c policy.Call) policy.Decision
	// Approve asks the user about a call and waits for the decision.
	Approve(ctx context.Context, step *aosv1.TaskStep, c *tool.Call, d policy.Decision) (aosv1.ApprovalDecision, error)
	Audit(ctx context.Context, e audit.Entry)
	Progress(stepID, url, path string, n, total int64)
	// Pause makes the Task Awaiting User and returns the user's reply (PLAN.md
	// §8.3). It fails when nobody can reply.
	Pause(ctx context.Context, kind aosv1.AwaitingKind, text string) (string, error)
	// Budget returns when the Task may make another model request; it pauses
	// the Task while a Cost Limit is reached (PLAN.md §8.4).
	Budget(ctx context.Context) error
}

// Run works on the Task until the model answers without calling Tools, and
// returns that answer. transcript holds the conversation so far.
func Run(ctx context.Context, cfg Config, h Host, transcript []llm.Item) (string, error) {
	input, previous := transcript, ""
	guard := newRetryGuard(cfg.MaxRetries)
	for {
		if err := h.Budget(ctx); err != nil {
			return "", err
		}
		req := llm.Request{
			Model:              cfg.Model,
			ReasoningEffort:    cfg.ReasoningEffort,
			Instructions:       cfg.Instructions,
			Tools:              cfg.Tools.Specs(),
			Input:              input,
			PreviousResponseID: previous,
			CacheKey:           "aos-agent",
		}
		resp, textStep, err := respond(ctx, cfg, h, req)
		for err != nil && ctx.Err() == nil {
			switch {
			case errors.Is(err, llm.ErrPreviousResponseUnavailable) && req.PreviousResponseID != "":
				req.Input, req.PreviousResponseID = transcript, ""
			case llm.IsTransient(err):
				// The provider already retried with backoff: now the user decides.
				why := fmt.Sprintf("The model API kept failing (%d attempts): %v", cfg.MaxRetries+1, err)
				reply, perr := h.Pause(ctx, aosv1.AwaitingKind_AWAITING_KIND_RETRIES, why+"\n"+replyHint)
				if perr != nil {
					return "", fmt.Errorf("%s (%w)", why, perr)
				}
				hint := llm.UserMessage(reply)
				req.Input = append(slices.Clip(req.Input), hint)
				transcript = append(transcript, hint)
			default:
				return "", err
			}
			resp, textStep, err = respond(ctx, cfg, h, req)
		}
		if err != nil {
			return "", err
		}
		if err := h.Responded(ctx, resp); err != nil {
			return "", err
		}
		transcript = append(transcript, resp.Output...)

		var text strings.Builder
		var calls, others []llm.Item
		for _, item := range resp.Output {
			switch item.Type {
			case llm.FunctionCall:
				calls = append(calls, item)
			case llm.Message:
				text.WriteString(item.Text)
				others = append(others, item)
			default:
				others = append(others, item)
			}
		}
		if textStep != nil {
			textStep.Text = text.String()
			if err := h.UpdateStep(ctx, textStep, others); err != nil {
				return "", err
			}
			others = nil
		}
		if len(calls) == 0 {
			return text.String(), nil
		}
		outputs, attempts, err := runCalls(ctx, cfg, h, calls, others)
		transcript = append(transcript, outputs...)
		if err != nil {
			return "", err
		}
		why := ""
		for _, a := range attempts {
			if w := guard.observe(a); w != "" && why == "" {
				why = w
			}
		}
		if why != "" {
			reply, err := h.Pause(ctx, aosv1.AwaitingKind_AWAITING_KIND_RETRIES, why+"\n"+replyHint)
			if err != nil {
				return "", fmt.Errorf("%s (%w)", why, err)
			}
			guard.reset()
			hint := llm.UserMessage(reply)
			outputs = append(outputs, hint)
			transcript = append(transcript, hint)
		}
		input, previous = outputs, resp.ID
	}
}

// respond streams one model response; the text step is created on the first delta.
func respond(ctx context.Context, cfg Config, h Host, req llm.Request) (llm.Response, *aosv1.TaskStep, error) {
	var textStep *aosv1.TaskStep
	var stepErr error
	onText := func(delta string) {
		if stepErr != nil {
			return
		}
		if textStep == nil {
			textStep = &aosv1.TaskStep{Kind: aosv1.StepKind_STEP_KIND_AGENT_TEXT}
			if stepErr = h.AddStep(ctx, textStep, nil); stepErr != nil {
				return
			}
		}
		textStep.Text += delta
		h.TextDelta(textStep.Id, delta)
	}
	resp, err := cfg.Provider.Respond(ctx, req, onText)
	if err == nil {
		err = stepErr
	}
	return resp, textStep, err
}

// pending is one Tool call of a response.
type pending struct {
	item     llm.Item
	extra    []llm.Item // opaque output items stored with this step
	step     *aosv1.TaskStep
	call     *tool.Call
	decision policy.Decision
	output   string
}

// runCalls prepares and decides every call, then runs them: consecutive
// read-only calls that need no Approval run in parallel, the rest in order. It
// returns the outputs for the model and the finished calls for the Retry guard.
func runCalls(ctx context.Context, cfg Config, h Host, items, extra []llm.Item) ([]llm.Item, []attempt, error) {
	env := h.Env()
	calls := make([]*pending, len(items))
	for i, item := range items {
		p := &pending{item: item}
		if i == 0 {
			p.extra = extra
		}
		p.step = &aosv1.TaskStep{Kind: aosv1.StepKind_STEP_KIND_TOOL_CALL, ToolCall: &aosv1.ToolCall{
			CallId: item.CallID, Tool: item.Name, ArgumentsJson: item.Arguments, Status: aosv1.ToolCallStatus_TOOL_CALL_STATUS_PENDING}}
		if err := h.AddStep(ctx, p.step, p.items()); err != nil {
			return nil, nil, err
		}
		if t, ok := cfg.Tools.Get(item.Name); !ok {
			p.output = fmt.Sprintf("error: there is no Tool called %q", item.Name)
		} else if c, err := t.Prepare(ctx, env, json.RawMessage(item.Arguments)); err != nil {
			p.output = "error: " + err.Error()
			if ctx.Err() != nil {
				p.output = cancelled
			}
		} else {
			p.call, p.decision = c, h.Decide(c.Policy)
			p.step.Text = c.Summary
		}
		calls[i] = p
	}

	for i := 0; i < len(calls); {
		j := i
		for j < len(calls) && parallel(calls[j]) {
			j++
		}
		if j == i {
			j = i + 1
		}
		var wg sync.WaitGroup
		for _, p := range calls[i:j] {
			wg.Add(1)
			go func() {
				defer wg.Done()
				execute(ctx, h, p)
			}()
		}
		wg.Wait()
		if err := ctx.Err(); err != nil {
			for _, p := range calls[j:] {
				p.finish(ctx, h, aosv1.ToolCallStatus_TOOL_CALL_STATUS_CANCELLED, cancelled, "")
			}
			return outputsOf(calls), nil, err
		}
		i = j
	}
	var attempts []attempt
	for _, p := range calls {
		switch p.step.ToolCall.Status {
		case aosv1.ToolCallStatus_TOOL_CALL_STATUS_SUCCEEDED, aosv1.ToolCallStatus_TOOL_CALL_STATUS_FAILED, aosv1.ToolCallStatus_TOOL_CALL_STATUS_DENIED:
			failed := p.step.ToolCall.Status != aosv1.ToolCallStatus_TOOL_CALL_STATUS_SUCCEEDED
			attempts = append(attempts, newAttempt(p.item.Name, p.item.Arguments, p.step.Text, p.output, failed))
		}
	}
	return outputsOf(calls), attempts, nil
}

func parallel(p *pending) bool {
	return p.call != nil && p.call.ReadOnly && p.decision.Verdict == policy.Allow
}

func outputsOf(calls []*pending) []llm.Item {
	out := make([]llm.Item, len(calls))
	for i, p := range calls {
		out[i] = llm.Item{Type: llm.FunctionCallOutput, CallID: p.item.CallID, Output: p.output}
	}
	return out
}

func (p *pending) items() []llm.Item {
	items := append(append([]llm.Item{}, p.extra...), p.item)
	if p.output != "" {
		items = append(items, llm.Item{Type: llm.FunctionCallOutput, CallID: p.item.CallID, Output: p.output})
	}
	return items
}

func (p *pending) finish(ctx context.Context, h Host, status aosv1.ToolCallStatus, output, ref string) {
	p.output = output
	c := p.step.ToolCall
	c.Status, c.Result, c.OutputRef, c.FinishedAt = status, output, ref, timestamppb.Now()
	_ = h.UpdateStep(context.WithoutCancel(ctx), p.step, p.items())
}

// execute runs one call through its decision and records the outcome.
func execute(ctx context.Context, h Host, p *pending) {
	entry := audit.Entry{StepID: p.step.Id, Tool: p.item.Name, Arguments: p.item.Arguments, Actor: "agent"}
	defer func() { h.Audit(context.WithoutCancel(ctx), entry) }()
	if p.call == nil {
		entry.Decision, entry.Result = "invalid", p.output
		p.finish(ctx, h, aosv1.ToolCallStatus_TOOL_CALL_STATUS_FAILED, p.output, "")
		return
	}
	d := p.decision
	entry.Decision, entry.DecidedBy = verdict(d.Verdict), d.By
	var widen []string
	switch d.Verdict {
	case policy.Deny:
		out := "denied: " + strings.Join(d.Reasons, "; ")
		entry.Result = out
		p.finish(ctx, h, aosv1.ToolCallStatus_TOOL_CALL_STATUS_DENIED, out, "")
		return
	case policy.Ask:
		d.ProtectedPaths = widenTargets(h.Env(), p.call.Policy.Effects, d.ProtectedPaths)
		decision, err := h.Approve(ctx, p.step, p.call, d)
		if err != nil {
			entry.Result = "cancelled while awaiting Approval"
			p.finish(ctx, h, aosv1.ToolCallStatus_TOOL_CALL_STATUS_CANCELLED, entry.Result, "")
			return
		}
		entry.DecidedBy = "user"
		if decision == aosv1.ApprovalDecision_APPROVAL_DECISION_DENY {
			out := "The user denied this call. Do not try to achieve the same effect another way; continue without it or ask the user."
			entry.Decision, entry.Result = "deny", out
			p.finish(ctx, h, aosv1.ToolCallStatus_TOOL_CALL_STATUS_DENIED, out, "")
			return
		}
		entry.Decision = "allow"
		widen = d.ProtectedPaths
	}

	c := p.step.ToolCall
	c.Status, c.StartedAt = aosv1.ToolCallStatus_TOOL_CALL_STATUS_RUNNING, timestamppb.Now()
	_ = h.UpdateStep(ctx, p.step, p.items())
	start := time.Now()
	res := p.call.Run(ctx, tool.Run{StepID: p.step.Id, Widen: widen, Progress: func(url, path string, n, total int64) {
		h.Progress(p.step.Id, url, path, n, total)
	}})
	entry.Duration, entry.Result = time.Since(start), res.Output
	// A cancelled call reports the cancellation, not whatever the dying
	// command happened to print — the feed used to show the Go error
	// "error: context canceled" (PLAN.md M4.8 item 8.12).
	if ctx.Err() != nil {
		entry.Result = cancelled
		p.finish(ctx, h, aosv1.ToolCallStatus_TOOL_CALL_STATUS_CANCELLED, cancelled, "")
		return
	}
	status := aosv1.ToolCallStatus_TOOL_CALL_STATUS_SUCCEEDED
	if res.Error {
		status = aosv1.ToolCallStatus_TOOL_CALL_STATUS_FAILED
	}
	p.finish(ctx, h, status, res.Output, res.OutputRef)
}

func verdict(v policy.Verdict) string {
	switch v {
	case policy.Allow:
		return "allow"
	case policy.Ask:
		return "ask"
	}
	return "deny"
}

// widenTargets turns the Protected Paths a call changes into the paths the
// sandbox must allow: the path itself when it already exists and is written in
// place, otherwise its folder (creating, deleting or moving needs the folder).
func widenTargets(env *tool.Env, effects []policy.Effect, hits []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range effects {
		if !slices.Contains(hits, e.Path) {
			continue
		}
		target := filepath.Dir(e.Path)
		if e.Op == policy.Write && env.Stat != nil {
			if exists, _ := env.Stat(e.Path); exists {
				target = e.Path
			}
		}
		if !seen[target] {
			seen[target] = true
			out = append(out, target)
		}
	}
	return out
}
