package session

import (
	"sort"
	"sync"
	"time"
)

// Registry lists the running Sessions for viewers (Desktop Watch, aos attach).
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
	// input, if set, is told what a viewer typed (Agent Sessions note it for the Agent).
	input func([]byte)
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{entries: map[string]*Entry{}}
}

func (r *Registry) add(e *Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[e.ID] = e
}

// remove removes id if it still refers to s.
func (r *Registry) remove(id string, s *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[id]; ok && e.Session == s {
		delete(r.entries, id)
	}
}

// Get returns the Session called id.
func (r *Registry) Get(id string) (*Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	return e, ok
}

// List returns every Session, oldest first.
func (r *Registry) List() []*Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Entry, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// Typed tells the Session's owner what a viewer typed.
func (e *Entry) Typed(b []byte) {
	if e.input != nil {
		e.input(b)
	}
}
