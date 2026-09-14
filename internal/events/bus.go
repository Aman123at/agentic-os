// Package events is the in-process publish/subscribe bus behind the event stream
// (PLAN.md §4.1). Every state change is published here.
package events

import (
	"context"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
)

// buffer is how far a subscriber may fall behind before it is dropped. A dropped
// subscriber's channel is closed; clients re-subscribe and re-read state.
const buffer = 4096

// Bus fans events out to subscribers.
type Bus struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
}

type subscriber struct {
	ch     chan *aosv1.Event
	taskID string
}

// New returns an empty Bus.
func New() *Bus {
	return &Bus{subs: map[*subscriber]struct{}{}}
}

// Publish sends e to every matching subscriber without blocking.
func (b *Bus) Publish(e *aosv1.Event) {
	if e.Time == nil {
		e.Time = timestamppb.New(time.Now())
	}
	task := TaskID(e)
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		if s.taskID != "" && task != s.taskID {
			continue
		}
		select {
		case s.ch <- e:
		default:
			delete(b.subs, s)
			close(s.ch)
		}
	}
}

// Subscribe returns events for taskID (all Tasks if empty) until ctx ends or the
// subscriber falls too far behind; then the channel is closed.
func (b *Bus) Subscribe(ctx context.Context, taskID string) <-chan *aosv1.Event {
	s := &subscriber{ch: make(chan *aosv1.Event, buffer), taskID: taskID}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subs[s]; ok {
			delete(b.subs, s)
			close(s.ch)
		}
	}()
	return s.ch
}

// TaskID returns the Task an event belongs to, or "".
func TaskID(e *aosv1.Event) string {
	switch k := e.Kind.(type) {
	case *aosv1.Event_TaskChanged:
		return k.TaskChanged.GetTask().GetId()
	case *aosv1.Event_TaskStep:
		return k.TaskStep.GetStep().GetTaskId()
	case *aosv1.Event_TextDelta:
		return k.TextDelta.GetTaskId()
	case *aosv1.Event_Approval:
		return k.Approval.GetApproval().GetTaskId()
	case *aosv1.Event_DownloadProgress:
		return k.DownloadProgress.GetTaskId()
	case *aosv1.Event_Notification:
		return k.Notification.GetTaskId()
	}
	return ""
}
