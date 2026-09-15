package session

import (
	"sort"
	"sync"
	"time"
)

// An Agent Session stays listed for a while after its Task ends, so a viewer
// who arrives late (a quick Task) can still replay what its terminal showed.
const (
	endedTTL  = 15 * time.Minute
	keepEnded = 8
)

// Registry lists Sessions for viewers (Desktop Watch, aos attach): the running
// ones, and Agent Sessions that ended recently.
type Registry struct {
	mu      sync.Mutex
	entries map[string]*Entry
}

// Entry is a listed Session. Session is *Session on Linux.
type Entry struct {
	ID      string
	TaskID  string
	Agent   bool
	Session *Session
	Created time.Time
	// Ended is when an Agent Session's shell ended; zero while it runs. An ended
	// Session replays its recent output to a viewer and takes no input.
	Ended time.Time
	// input, if set, is told what a viewer typed (Agent Sessions note it for the Agent).
	input func([]byte)
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{entries: map[string]*Entry{}}
}

// Add lists a Session, replacing any entry with its id (a re-sandboxed Agent
// Session keeps its Task's id).
func (r *Registry) Add(e *Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[e.ID] = e
}

// Remove unlists id if it still refers to s. An Agent Session is kept, marked
// ended, until endedTTL passes or keepEnded newer ones have ended.
func (r *Registry) Remove(id string, s *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok || e.Session != s {
		return
	}
	if !e.Agent {
		delete(r.entries, id)
		return
	}
	e.Ended, e.input = time.Now(), nil
	r.prune(e.Ended)
}

// Get returns a copy of the entry called id.
func (r *Registry) Get(id string) (*Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune(time.Now())
	e, ok := r.entries[id]
	if !ok {
		return nil, false
	}
	c := *e
	return &c, true
}

// List returns a copy of every entry, oldest first.
func (r *Registry) List() []*Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune(time.Now())
	out := make([]*Entry, 0, len(r.entries))
	for _, e := range r.entries {
		c := *e
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// prune drops ended Sessions older than endedTTL, then all but the keepEnded
// most recently ended. Callers hold r.mu.
func (r *Registry) prune(now time.Time) {
	var ended []*Entry
	for id, e := range r.entries {
		switch {
		case e.Ended.IsZero():
		case now.Sub(e.Ended) > endedTTL:
			delete(r.entries, id)
		default:
			ended = append(ended, e)
		}
	}
	if len(ended) <= keepEnded {
		return
	}
	sort.Slice(ended, func(i, j int) bool { return ended[i].Ended.After(ended[j].Ended) })
	for _, e := range ended[keepEnded:] {
		delete(r.entries, e.ID)
	}
}

// Typed tells the Session's owner what a viewer typed.
func (e *Entry) Typed(b []byte) {
	if e.input != nil {
		e.input(b)
	}
}
