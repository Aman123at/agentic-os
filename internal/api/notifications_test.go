package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
	"github.com/amantiwari/agentic-os/internal/notify"
	"github.com/amantiwari/agentic-os/internal/store"
)

func TestNotificationsCanBeListedAndDismissed(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	center := &notify.Center{DB: db}
	for _, title := range []string{"Replay", "Remember this?"} {
		if _, err := center.Post(context.Background(), &aosv1.Notification{Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	auth, _ := newAuth(t)
	h := auth.TCP((&Server{Auth: auth, Notifications: center}).Handler())

	list := func() []string {
		t.Helper()
		rec := call(t, h, "/aos.v1.SystemService/ListNotifications", `{}`)
		var r struct{ Notifications []struct{ Id, Title string } }
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &r) != nil {
			t.Fatalf("ListNotifications: %d %s", rec.Code, rec.Body)
		}
		var out []string
		for _, n := range r.Notifications {
			out = append(out, n.Id+" "+n.Title)
		}
		return out
	}
	listed := list()
	if len(listed) != 2 {
		t.Fatalf("listed %v", listed)
	}
	id := listed[0][:len("n_0123456789abcdef")]
	if rec := call(t, h, "/aos.v1.SystemService/DismissNotification", `{"id":"`+id+`"}`); rec.Code != http.StatusOK {
		t.Errorf("DismissNotification: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, h, "/aos.v1.SystemService/DismissNotification", `{"id":"`+id+`"}`); rec.Code != http.StatusNotFound {
		t.Errorf("dismissing twice: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, h, "/aos.v1.SystemService/DismissNotification", `{"all":true}`); rec.Code != http.StatusOK {
		t.Errorf("dismissing all: %d %s", rec.Code, rec.Body)
	}
	if listed := list(); len(listed) != 0 {
		t.Errorf("after dismissing all: %v", listed)
	}
}
