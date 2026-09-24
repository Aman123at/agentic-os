package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/gen/go/aos/v1/aosv1connect"
)

// recordingSessions notes the folder each Session was asked to start in.
type recordingSessions struct{ cwds []string }

func (r *recordingSessions) Create(_ context.Context, _, _ uint16, cwd string) (*aosv1.SessionInfo, error) {
	r.cwds = append(r.cwds, cwd)
	return &aosv1.SessionInfo{Id: "u_1", Cwd: cwd}, nil
}
func (r *recordingSessions) List() []*aosv1.SessionInfo      { return nil }
func (r *recordingSessions) Close(string) error              { return nil }
func (r *recordingSessions) Attach(string) (Terminal, error) { return nil, nil }

// TestCreateSessionPassesTheFolderToStartIn covers M8.6: Code's Terminal
// panel asks for its Workspace. ~ and relative paths start at the Realm's
// home, as in FileService; no folder is the home folder, left to the shell.
func TestCreateSessionPassesTheFolderToStartIn(t *testing.T) {
	auth, _ := newAuth(t)
	home := t.TempDir()
	rec := &recordingSessions{}
	srv := httptest.NewServer(auth.TCP((&Server{Auth: auth, Home: home, Sessions: rec}).Handler()))
	t.Cleanup(srv.Close)
	client := aosv1connect.NewSessionServiceClient(&http.Client{Transport: bearer{}}, srv.URL)

	for _, cwd := range []string{"/srv/app/", "~/project", "project/src", ""} {
		if _, err := client.CreateSession(context.Background(), connect.NewRequest(&aosv1.CreateSessionRequest{Cols: 80, Rows: 24, Cwd: cwd})); err != nil {
			t.Fatalf("CreateSession(%q): %v", cwd, err)
		}
	}
	want := []string{"/srv/app", filepath.Join(home, "project"), filepath.Join(home, "project/src"), ""}
	if len(rec.cwds) != len(want) {
		t.Fatalf("started in %q, want %q", rec.cwds, want)
	}
	for i := range want {
		if rec.cwds[i] != want[i] {
			t.Errorf("Session %d started in %q, want %q", i, rec.cwds[i], want[i])
		}
	}
}
