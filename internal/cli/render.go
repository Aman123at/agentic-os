package cli

import (
	"fmt"
	"strings"
	"time"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
)

// styles are ANSI styles, or empty when output isn't a terminal.
type styles struct {
	dim, bold, green, red, yellow, cyan, reset string
}

func newStyles(color bool) styles {
	if !color {
		return styles{}
	}
	return styles{dim: "\x1b[2m", bold: "\x1b[1m", green: "\x1b[32m", red: "\x1b[31m", yellow: "\x1b[33m", cyan: "\x1b[36m", reset: "\x1b[0m"}
}

// toolLine describes a Tool call step in one line.
func (s styles) toolLine(step *aosv1.TaskStep) string {
	c := step.ToolCall
	summary := step.Text
	if summary == "" {
		summary = c.Tool
	}
	switch c.Status {
	case aosv1.ToolCallStatus_TOOL_CALL_STATUS_SUCCEEDED:
		return fmt.Sprintf("%s✓%s %s%s", s.green, s.reset, summary, s.duration(c))
	case aosv1.ToolCallStatus_TOOL_CALL_STATUS_FAILED:
		return fmt.Sprintf("%s✗%s %s%s%s", s.red, s.reset, summary, s.duration(c), s.detail(c.Result))
	case aosv1.ToolCallStatus_TOOL_CALL_STATUS_DENIED:
		return fmt.Sprintf("%s⊘%s %s %s(denied)%s", s.yellow, s.reset, summary, s.dim, s.reset)
	case aosv1.ToolCallStatus_TOOL_CALL_STATUS_CANCELLED:
		return fmt.Sprintf("%s⊘%s %s %s(cancelled)%s", s.dim, s.reset, summary, s.dim, s.reset)
	case aosv1.ToolCallStatus_TOOL_CALL_STATUS_AWAITING_APPROVAL:
		return fmt.Sprintf("%s?%s %s", s.yellow, s.reset, summary)
	}
	return fmt.Sprintf("%s▸%s %s", s.cyan, s.reset, summary)
}

func (s styles) duration(c *aosv1.ToolCall) string {
	if c.StartedAt == nil || c.FinishedAt == nil {
		return ""
	}
	d := c.FinishedAt.AsTime().Sub(c.StartedAt.AsTime())
	if d < 500*time.Millisecond {
		return ""
	}
	return fmt.Sprintf(" %s(%s)%s", s.dim, d.Round(100*time.Millisecond), s.reset)
}

// detail shows the first line of a failed call's result.
func (s styles) detail(result string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(result), "\n")
	if strings.HasPrefix(line, "Exit code") {
		// Command results start with the exit code; the useful part follows.
		parts := strings.SplitN(strings.TrimSpace(result), "\nOutput:\n", 2)
		if len(parts) == 2 {
			line, _, _ = strings.Cut(strings.TrimSpace(lastLines(parts[1], 1)), "\n")
		}
	}
	if len(line) > 160 {
		line = line[:160] + "…"
	}
	if line == "" {
		return ""
	}
	return "\n    " + s.dim + line + s.reset
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// approvalBlock describes an Approval and its choices.
func (s styles) approvalBlock(a *aosv1.Approval) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s⚠ Approval needed:%s %s\n", s.yellow+s.bold, s.reset, a.Summary)
	for _, r := range a.Reasons {
		fmt.Fprintf(&b, "  %s%s%s\n", s.dim, r, s.reset)
	}
	if len(a.ProtectedPaths) > 0 {
		fmt.Fprintf(&b, "  %sWritable for this call only: %s%s\n", s.dim, strings.Join(a.ProtectedPaths, ", "), s.reset)
	}
	choices := "[a] allow once"
	if a.Grantable {
		choices += "  [t] allow for the rest of this Task"
	}
	choices += "  [d] deny"
	b.WriteString("  " + choices)
	return b.String()
}

func stateName(s aosv1.TaskState) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(s.String(), "TASK_STATE_")), "_", " ")
}

func finished(s aosv1.TaskState) bool {
	switch s {
	case aosv1.TaskState_TASK_STATE_SUCCEEDED, aosv1.TaskState_TASK_STATE_FAILED, aosv1.TaskState_TASK_STATE_CANCELLED, aosv1.TaskState_TASK_STATE_INTERRUPTED:
		return true
	}
	return false
}

func usageLine(t *aosv1.Task) string {
	u := t.GetUsage()
	if u.GetInputTokens() == 0 && u.GetOutputTokens() == 0 {
		return ""
	}
	return fmt.Sprintf("%d input tokens (%d cached), %d output tokens", u.InputTokens, u.CachedInputTokens, u.OutputTokens)
}
