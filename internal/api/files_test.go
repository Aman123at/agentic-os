package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/gen/go/aos/v1/aosv1connect"
	"github.com/Aman123at/agentic-os/internal/audit"
	"github.com/Aman123at/agentic-os/internal/files"
)

// fileClient is FileService over fileServer's temp home.
func fileClient(t *testing.T) (aosv1connect.FileServiceClient, string, *audit.Log) {
	t.Helper()
	h, home, log := fileServer(t)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return aosv1connect.NewFileServiceClient(&http.Client{Transport: bearer{}}, srv.URL), home, log
}

func TestMakeFolderMakesANewFolderAndAuditsIt(t *testing.T) {
	c, home, log := fileClient(t)
	ctx := context.Background()
	if _, err := c.MakeFolder(ctx, connect.NewRequest(&aosv1.MakeFolderRequest{Path: "~/project/src"})); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(home, "project", "src")); err != nil || !fi.IsDir() {
		t.Fatalf("no folder: %v", err)
	}
	_, err := c.MakeFolder(ctx, connect.NewRequest(&aosv1.MakeFolderRequest{Path: "~/project/src"}))
	if connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("again: %v, want AlreadyExists", err)
	}
	entries, err := log.List(ctx, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	made := 0
	for _, e := range entries {
		if e.Tool == "make_folder" {
			made++
		}
	}
	if made != 2 {
		t.Errorf("%d make_folder entries in the Audit Log, want 2 (one ok, one error)", made)
	}
}

func TestFileInfoReportsAOSStateNotWritable(t *testing.T) {
	home := t.TempDir()
	auth, _ := newAuth(t)
	ops := files.Ops{Home: home, UID: os.Getuid()}
	state := filepath.Join(home, "state")
	for _, p := range []string{filepath.Join(state, "aos.db"), filepath.Join(home, "notes.md")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{Auth: auth, Home: home, UserFiles: files.InProcess{Ops: ops}, Protected: noLocks{}, OwnState: []string{state}}
	srv := httptest.NewServer(auth.TCP(s.Handler()))
	t.Cleanup(srv.Close)
	c := aosv1connect.NewFileServiceClient(&http.Client{Transport: bearer{}}, srv.URL)
	for p, want := range map[string]bool{"~/notes.md": true, "~/state": false, "~/state/aos.db": false} {
		resp, err := c.Stat(context.Background(), connect.NewRequest(&aosv1.StatRequest{Path: p}))
		if err != nil {
			t.Fatal(err)
		}
		if resp.Msg.Info.Writable != want {
			t.Errorf("%s: writable %v, want %v", p, resp.Msg.Info.Writable, want)
		}
	}
}
