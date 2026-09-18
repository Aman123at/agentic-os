package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Aman123at/agentic-os/internal/policy"
)

// retryGuard pauses a Task that is stuck (PLAN.md §8.3): a goal that keeps
// failing, or the same call made again and again even if it succeeds. max is
// AOS_MAX_RETRIES, the attempts allowed after the first.
type retryGuard struct {
	max      int
	failures map[string][]string // goal → each consecutive failed attempt
	last     string              // signature of the previous call
	repeats  int                 // times last was repeated in a row
}

// attempt is a finished Tool call as the guard sees it.
type attempt struct {
	// goal is what the call tries to achieve: the Tool, and for commands the program.
	goal string
	// signature is the Tool with its exact arguments.
	signature string
	summary   string
	failed    bool
	result    string
}

func newRetryGuard(max int) *retryGuard {
	return &retryGuard{max: max, failures: map[string][]string{}}
}

// newAttempt describes a finished call of tool with JSON arguments args.
func newAttempt(tool, args, summary, result string, failed bool) attempt {
	a := attempt{goal: tool, signature: tool + " " + canonicalJSON(args), summary: summary, failed: failed, result: result}
	if tool == "run_command" {
		var c struct{ Command string }
		_ = json.Unmarshal([]byte(args), &c)
		a.goal += " " + policy.Program(c.Command)
	}
	if a.summary == "" {
		a.summary = tool
	}
	return a
}

// observe records a finished call and returns why the Task must pause, or "".
func (g *retryGuard) observe(a attempt) string {
	if a.failed {
		g.failures[a.goal] = append(g.failures[a.goal], a.summary+" → "+gist(a.result))
	} else {
		delete(g.failures, a.goal)
	}
	if a.signature == g.last {
		g.repeats++
	} else {
		g.last, g.repeats = a.signature, 0
	}
	if tried := g.failures[a.goal]; len(tried) > g.max {
		var b strings.Builder
		fmt.Fprintf(&b, "The Agent tried the same step %d times without success (AOS_MAX_RETRIES=%d):\n", len(tried), g.max)
		for _, t := range tried {
			fmt.Fprintf(&b, "  • %s\n", t)
		}
		b.WriteString(`Reply with a hint, or "try another way", and it continues; or cancel the Task.`)
		return b.String()
	}
	if g.repeats > 0 && g.repeats >= g.max {
		return fmt.Sprintf("The Agent made the same call %d times in a row (AOS_MAX_RETRIES=%d): %s\nReply with a hint and it continues; or cancel the Task.",
			g.repeats+1, g.max, a.summary)
	}
	return ""
}

// reset forgets the attempts so far, after the user replied with a hint.
func (g *retryGuard) reset() {
	g.failures = map[string][]string{}
	g.last, g.repeats = "", 0
}

// canonicalJSON re-encodes JSON so equal arguments compare equal.
func canonicalJSON(s string) string {
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return s
	}
	return string(b)
}

// gist is a failed call's result in one line: for commands, the exit code and
// the last line of output.
func gist(result string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(result), "\n")
	if head, out, ok := strings.Cut(result, "\nOutput:\n"); ok {
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		status, _, _ := strings.Cut(head, " after ")
		line = status + ": " + strings.TrimSpace(lines[len(lines)-1])
	}
	if r := []rune(line); len(r) > 160 {
		line = string(r[:160]) + "…"
	}
	return line
}
