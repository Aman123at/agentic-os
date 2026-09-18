package task

import (
	"context"
	"fmt"
	"strings"
	"testing"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/llm/fake"
	"github.com/Aman123at/agentic-os/internal/policy"
	"github.com/Aman123at/agentic-os/internal/tool"
)

func TestConversationsStartWithTheContextAndFollowUpsGetItWhenItChanged(t *testing.T) {
	profile := "# Machine Profile\n- nginx is not installed\n"
	h := newHarness(t, policy.Auto, fake.Say("a"), fake.Say("b"), fake.Say("c"))
	h.m.Close()
	h.cfg.Context = func(context.Context) string { return profile }
	h.m, _ = New(h.cfg)
	t.Cleanup(h.m.Close)

	created, _ := h.m.Create(context.Background(), "hello", 0, true)
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	if _, err := h.m.FollowUp(context.Background(), created.Id, "again", true); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	profile = "# Machine Profile\n- nginx 1.24 is installed\n"
	if _, err := h.m.FollowUp(context.Background(), created.Id, "more", true); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)

	var got []string
	for _, req := range h.model.Requests() {
		var roles []string
		for _, it := range req.Input {
			roles = append(roles, it.Role+":"+strings.TrimPrefix(strings.TrimSpace(it.Text), "# Machine Profile\n- "))
		}
		got = append(got, strings.Join(roles, " "))
	}
	want := []string{
		"developer:nginx is not installed user:hello",
		"developer:nginx is not installed user:hello assistant:a user:again",
		"developer:nginx is not installed user:hello assistant:a user:again assistant:b developer:nginx 1.24 is installed user:more",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRememberSavesDirectlyOnlyWhenTheUserSaidRemember(t *testing.T) {
	var saved []string
	h := newHarness(t, policy.Auto,
		fake.Calls("", fake.Call{Name: "remember", Args: map[string]any{"text": "The user indents with tabs.", "user_asked": true}}), fake.Say("ok"),
		fake.Calls("", fake.Call{Name: "remember", Args: map[string]any{"text": "The site deploys with rsync.", "user_asked": true}}), fake.Say("ok"),
	)
	h.m.Close()
	newEnv := h.cfg.NewEnv
	h.cfg.NewEnv = func(env *tool.Env) (func(), error) {
		env.Remember = func(_ context.Context, text string, direct bool) error {
			saved = append(saved, fmt.Sprintf("%v: %s", direct, text))
			return nil
		}
		return newEnv(env)
	}
	h.m, _ = New(h.cfg)
	t.Cleanup(h.m.Close)

	first, _ := h.m.Create(context.Background(), "Remember that I indent with tabs.", 0, true)
	h.waitState(t, first.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	// The Agent claims the user asked, but the user never said so.
	second, _ := h.m.Create(context.Background(), "Deploy the site.", 0, true)
	h.waitState(t, second.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	if want := "true: The user indents with tabs.|false: The site deploys with rsync."; strings.Join(saved, "|") != want {
		t.Errorf("saved %q, want %q", strings.Join(saved, "|"), want)
	}
}
