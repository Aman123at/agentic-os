package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Aman123at/agentic-os/internal/policy"
)

// CoordinationTools returns the Coordination group.
func CoordinationTools() []Tool {
	return []Tool{askUser{}, remember{}, createCheckpoint{}}
}

type askUser struct{}

func (askUser) Spec() Spec {
	return Spec{Name: "ask_user", Description: "Ask the user a question and wait for the answer. Use it when the Task is ambiguous or a decision is theirs; don't ask for permission to use Tools, since the user is asked automatically when needed.",
		Parameters: object(map[string]any{"question": str("The question, in plain words")})}
}

func (askUser) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct{ Question string }
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Question) == "" {
		return nil, errors.New("question is empty")
	}
	return &Call{Summary: "Ask: " + oneLine(a.Question, 200), Policy: policy.Call{Tool: "ask_user", Coordination: true},
		Run: func(ctx context.Context, r Run) Result {
			if !env.Interactive || env.AskUser == nil {
				return Result{Output: "Nobody is available to answer. Continue with your best judgement and state your assumptions in the final answer, or stop and explain what you need."}
			}
			answer, err := env.AskUser(ctx, a.Question)
			if err != nil {
				return Errorf("%v", err)
			}
			return Result{Output: "The user answered: " + answer}
		}}, nil
}

type remember struct{}

func (remember) Spec() Spec {
	return Spec{Name: "remember", Description: "Keep a lasting preference or fact for future Tasks (Memory). It is saved once the user accepts it, or at once when the user asked you to remember it.",
		Parameters: object(map[string]any{
			"text":       str("The preference or fact, in one sentence"),
			"user_asked": optBool("True only if the user explicitly asked you to remember this"),
		})}
}

func (remember) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct {
		Text      string
		UserAsked bool `json:"user_asked"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Text) == "" {
		return nil, errors.New("text is empty")
	}
	// The Agent's word alone is not enough: the user must have said "remember".
	direct := a.UserAsked && env.UserMessages != nil && mentionsRemember(env.UserMessages())
	summary := "Propose to remember: "
	if direct {
		summary = "Remember: "
	}
	return &Call{Summary: summary + oneLine(a.Text, 200), Policy: policy.Call{Tool: "remember", Coordination: true},
		Run: func(ctx context.Context, r Run) Result {
			if env.Remember == nil {
				return Errorf("Memory is not available")
			}
			if err := env.Remember(ctx, a.Text, direct); err != nil {
				return Errorf("%v", err)
			}
			if direct {
				return Result{Output: "Saved to Memory; future Tasks are given it."}
			}
			return Result{Output: "Proposed to the user; it is saved only if they accept."}
		}}, nil
}

func mentionsRemember(messages []string) bool {
	for _, m := range messages {
		if strings.Contains(strings.ToLower(m), "remember") {
			return true
		}
	}
	return false
}
