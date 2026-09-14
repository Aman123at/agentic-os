package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/amantiwari/agentic-os/internal/policy"
)

// CoordinationTools returns the Coordination group, except create_checkpoint (M2).
func CoordinationTools() []Tool {
	return []Tool{askUser{}, remember{}}
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
	return Spec{Name: "remember", Description: "Propose a lasting preference or fact for future Tasks (Memory). It is saved only if the user accepts it.",
		Parameters: object(map[string]any{"text": str("The preference or fact, in one sentence")})}
}

func (remember) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct{ Text string }
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Text) == "" {
		return nil, errors.New("text is empty")
	}
	return &Call{Summary: "Propose to remember: " + oneLine(a.Text, 200), Policy: policy.Call{Tool: "remember", Coordination: true},
		Run: func(ctx context.Context, r Run) Result {
			if env.Remember == nil {
				return Errorf("Memory is not available")
			}
			if err := env.Remember(ctx, a.Text); err != nil {
				return Errorf("%v", err)
			}
			return Result{Output: "Proposed to the user; it is saved only if they accept."}
		}}, nil
}
