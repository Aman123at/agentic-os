package notify

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
	"github.com/amantiwari/agentic-os/internal/events"
	"github.com/amantiwari/agentic-os/internal/store"
)

func newCenter(t *testing.T) (*Center, <-chan *aosv1.Event, *time.Time) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	bus := events.New()
	clock := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	c := &Center{DB: db, Bus: bus, Now: func() time.Time { return clock }}
	return c, bus.Subscribe(t.Context(), ""), &clock
}

func titles(ns []*aosv1.Notification) string {
	s := ""
	for _, n := range ns {
		s += n.Title + ";"
	}
	return s
}

func TestNotificationsSurviveUntilDismissed(t *testing.T) {
	c, sub, clock := newCenter(t)
	ctx := context.Background()

	posted, err := c.Post(ctx, &aosv1.Notification{Title: "Remember this?", Body: "The user deploys with rsync.", TaskId: "t_1", MemoryId: "m_1", Port: 8080})
	if err != nil || posted.Id == "" || !posted.CreatedAt.AsTime().Equal(*clock) {
		t.Fatalf("Post = %+v, %v", posted, err)
	}
	if n := (<-sub).GetNotification(); n.GetId() != posted.Id || n.MemoryId != "m_1" || n.Dismissed {
		t.Errorf("published %+v", n)
	}
	*clock = clock.Add(time.Minute)
	second, _ := c.Post(ctx, &aosv1.Notification{Title: "Replay", Body: "Replay failed"})
	<-sub

	// Newest first, and a fresh read has every field (the Center reloads them after a restart).
	list, err := c.List(ctx)
	if err != nil || titles(list) != "Replay;Remember this?;" || list[1].TaskId != "t_1" || list[1].Port != 8080 || list[1].Body != "The user deploys with rsync." ||
		!list[1].CreatedAt.AsTime().Equal(posted.CreatedAt.AsTime()) {
		t.Fatalf("List = %v, %v", list, err)
	}

	if err := c.Dismiss(ctx, posted.Id); err != nil {
		t.Fatal(err)
	}
	if n := (<-sub).GetNotification(); n.GetId() != posted.Id || !n.Dismissed {
		t.Errorf("dismissal published as %+v", n)
	}
	if list, _ := c.List(ctx); titles(list) != "Replay;" {
		t.Errorf("after Dismiss: %s", titles(list))
	}
	if err := c.Dismiss(ctx, posted.Id); !errors.Is(err, ErrNoNotification) {
		t.Errorf("dismissing twice: %v", err)
	}

	// Dismissing all of them.
	if err := c.DismissAll(ctx); err != nil {
		t.Fatal(err)
	}
	if n := (<-sub).GetNotification(); n.GetId() != second.Id || !n.Dismissed {
		t.Errorf("dismissal published as %+v", n)
	}
	if list, _ := c.List(ctx); len(list) != 0 {
		t.Errorf("after DismissAll: %s", titles(list))
	}
}

func TestOnlyTheNewestNotificationsAreKept(t *testing.T) {
	c, _, clock := newCenter(t)
	ctx := context.Background()
	for i := range Keep + 5 {
		*clock = clock.Add(time.Second)
		if _, err := c.Post(ctx, &aosv1.Notification{Title: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := c.List(ctx)
	if err != nil || len(list) != Keep || list[0].Title != fmt.Sprint(Keep+4) || list[Keep-1].Title != "5" {
		t.Errorf("kept %d, newest %q, oldest %q, %v", len(list), list[0].Title, list[len(list)-1].Title, err)
	}
}
