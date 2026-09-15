package session

import (
	"fmt"
	"testing"
	"time"
)

// The Registry lists Sessions for viewers (PLAN.md §10): a User Session goes
// when it ends; an Agent Session stays, ended and read-only, for a late viewer.

func TestRemoveDropsAUserSessionButKeepsAnEndedAgentSession(t *testing.T) {
	r := NewRegistry()
	user, agent := &Session{}, &Session{}
	typed := ""
	r.Add(&Entry{ID: "u", Session: user, Created: time.Now()})
	r.Add(&Entry{ID: "a", TaskID: "t1", Agent: true, Session: agent, Created: time.Now(), input: func(b []byte) { typed += string(b) }})

	r.Remove("u", user)
	if _, ok := r.Get("u"); ok {
		t.Error("an ended User Session is still listed")
	}

	r.Remove("a", agent)
	e, ok := r.Get("a")
	if !ok || e.Ended.IsZero() {
		t.Fatalf("the Agent Session should stay listed as ended: ok=%v ended=%v", ok, e != nil && !e.Ended.IsZero())
	}
	e.Typed([]byte("ls\r"))
	if typed != "" {
		t.Errorf("an ended Session passed on what a viewer typed: %q", typed)
	}
}

func TestRemoveIgnoresASessionThatWasReplaced(t *testing.T) {
	// A re-sandboxed Agent Session is listed again under its Task's id; the old
	// shell ending must not unlist or end the new one.
	r := NewRegistry()
	old, current := &Session{}, &Session{}
	r.Add(&Entry{ID: "a", Agent: true, Session: old, Created: time.Now()})
	r.Add(&Entry{ID: "a", Agent: true, Session: current, Created: time.Now()})

	r.Remove("a", old)
	e, ok := r.Get("a")
	if !ok || e.Session != current || !e.Ended.IsZero() {
		t.Fatalf("the replacement Session was changed: ok=%v current=%v ended=%v", ok, ok && e.Session == current, ok && !e.Ended.IsZero())
	}
}

func TestEndedAgentSessionsExpireAfterTheirTTL(t *testing.T) {
	r := NewRegistry()
	now := time.Now()
	r.Add(&Entry{ID: "stale", Agent: true, Session: &Session{}, Created: now.Add(-time.Hour), Ended: now.Add(-endedTTL - time.Second)})
	r.Add(&Entry{ID: "recent", Agent: true, Session: &Session{}, Created: now.Add(-time.Hour), Ended: now.Add(-endedTTL + time.Minute)})

	if _, ok := r.Get("stale"); ok {
		t.Error("a Session that ended longer ago than the TTL is still listed")
	}
	if _, ok := r.Get("recent"); !ok {
		t.Error("a Session that ended within the TTL was dropped")
	}
}

func TestOnlyTheNewestEndedSessionsAreKept(t *testing.T) {
	r := NewRegistry()
	now := time.Now()
	r.Add(&Entry{ID: "running", Agent: true, Session: &Session{}, Created: now.Add(-time.Hour)})
	for i := range keepEnded + 3 {
		// ended-0 ended most recently.
		r.Add(&Entry{ID: fmt.Sprintf("ended-%d", i), Agent: true, Session: &Session{},
			Created: now.Add(-time.Hour), Ended: now.Add(-time.Duration(i) * time.Second)})
	}

	listed := map[string]bool{}
	for _, e := range r.List() {
		listed[e.ID] = true
	}
	if !listed["running"] {
		t.Error("a running Session was pruned")
	}
	for i := range keepEnded + 3 {
		id := fmt.Sprintf("ended-%d", i)
		if want := i < keepEnded; listed[id] != want {
			t.Errorf("%s listed=%v, want %v", id, listed[id], want)
		}
	}
}

func TestGetAndListHandOutCopies(t *testing.T) {
	r := NewRegistry()
	r.Add(&Entry{ID: "a", Agent: true, Session: &Session{}, Created: time.Now()})

	e, _ := r.Get("a")
	e.Ended = time.Now()
	r.List()[0].TaskID = "changed"

	e, _ = r.Get("a")
	if !e.Ended.IsZero() || e.TaskID != "" {
		t.Errorf("changing a returned entry changed the Registry: %+v", e)
	}
}
