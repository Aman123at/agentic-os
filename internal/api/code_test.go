package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/gen/go/aos/v1/aosv1connect"
	"github.com/Aman123at/agentic-os/internal/events"
	"github.com/Aman123at/agentic-os/internal/files"
)

type codeHarness struct {
	s      *Server
	code   aosv1connect.CodeServiceClient
	events aosv1connect.EventServiceClient
	home   string
}

func newCodeHarness(t *testing.T) codeHarness {
	t.Helper()
	home := t.TempDir()
	auth, _ := newAuth(t)
	ops := files.Ops{Home: home, UID: os.Getuid(), Scratch: []string{filepath.Join(home, "tmp")}}
	s := &Server{Auth: auth, Home: home, Bus: events.New(), UserFiles: files.InProcess{Ops: ops}, Protected: noLocks{}, WatchInterval: 20 * time.Millisecond,
		Info: func() *aosv1.InfoResponse { return &aosv1.InfoResponse{RootMode: true} }}
	srv := httptest.NewServer(auth.TCP(s.Handler()))
	t.Cleanup(srv.Close)
	hc := &http.Client{Transport: bearer{}}
	return codeHarness{s: s, code: aosv1connect.NewCodeServiceClient(hc, srv.URL), events: aosv1connect.NewEventServiceClient(hc, srv.URL), home: home}
}

// subscribe opens a tab's event stream and returns its subscription id.
func (h codeHarness) subscribe(t *testing.T, ctx context.Context) (*connect.ServerStreamForClient[aosv1.SubscribeResponse], string) {
	t.Helper()
	stream, err := h.events.Subscribe(ctx, connect.NewRequest(&aosv1.SubscribeRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() {
		t.Fatalf("no first message: %v", stream.Err())
	}
	id := stream.Msg().SubscriptionId
	if id == "" {
		t.Fatal("the first message carries no subscription id")
	}
	return stream, id
}

func (h codeHarness) waitClosed(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(h.s.Workspaces().List()) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("still open: %+v", h.s.Workspaces().List())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAWorkspaceRidesItsTabsEventStream(t *testing.T) {
	h := newCodeHarness(t)
	if err := os.MkdirAll(filepath.Join(h.home, "project"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, sub := h.subscribe(t, ctx)

	resp, err := h.code.OpenWorkspace(ctx, connect.NewRequest(&aosv1.OpenWorkspaceRequest{SubscriptionId: sub, Path: "~/project"}))
	if err != nil {
		t.Fatal(err)
	}
	ws := resp.Msg
	if ws.Root != filepath.Join(h.home, "project") || !ws.RootMode || ws.Id == "" {
		t.Fatalf("opened %+v, want the Workspace in Root Mode", ws)
	}
	if open := h.s.Workspaces().List(); len(open) != 1 || open[0].ID != ws.Id || open[0].Owner != sub {
		t.Fatalf("open Workspaces %+v", open)
	}

	unsaved := []string{filepath.Join(h.home, "project", "main.go")}
	if _, err := h.code.ReportDirty(ctx, connect.NewRequest(&aosv1.ReportDirtyRequest{WorkspaceId: ws.Id, Paths: unsaved})); err != nil {
		t.Fatal(err)
	}
	if got := h.s.Workspaces().List()[0].Unsaved; !slices.Equal(got, unsaved) {
		t.Fatalf("unsaved %v, want %v", got, unsaved)
	}

	// Its watch arrives on the event stream: the status, then a file that appears.
	if err := os.WriteFile(unsaved[0], []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sawWatch, sawFile := false, false
	for !sawWatch || !sawFile {
		if !stream.Receive() {
			t.Fatalf("the stream ended: %v", stream.Err())
		}
		wc := stream.Msg().GetEvent().GetWorkspace()
		if wc == nil {
			continue
		}
		if wc.WorkspaceId != ws.Id {
			t.Fatalf("an event for Workspace %q, want %q", wc.WorkspaceId, ws.Id)
		}
		sawWatch = sawWatch || wc.Watch.GetState() != aosv1.WorkspaceWatch_STATE_UNSPECIFIED
		for _, c := range wc.Changes {
			sawFile = sawFile || c.Kind == aosv1.FileChange_KIND_CREATED && c.Path == unsaved[0]
		}
	}

	// The window closes it.
	if _, err := h.code.CloseWorkspace(ctx, connect.NewRequest(&aosv1.CloseWorkspaceRequest{WorkspaceId: ws.Id})); err != nil {
		t.Fatal(err)
	}
	h.waitClosed(t)
	_, err = h.code.CloseWorkspace(ctx, connect.NewRequest(&aosv1.CloseWorkspaceRequest{WorkspaceId: ws.Id}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("closing it again: %v, want NotFound", err)
	}
	_, err = h.code.ReportDirty(ctx, connect.NewRequest(&aosv1.ReportDirtyRequest{WorkspaceId: ws.Id}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("ReportDirty after it closed: %v, want NotFound", err)
	}
}

func TestATabsWorkspacesCloseWithItsEventStream(t *testing.T) {
	h := newCodeHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tabCtx, closeTab := context.WithCancel(ctx)
	_, sub := h.subscribe(t, tabCtx)
	_, other := h.subscribe(t, ctx)
	for _, s := range []string{sub, sub, other} {
		if _, err := h.code.OpenWorkspace(ctx, connect.NewRequest(&aosv1.OpenWorkspaceRequest{SubscriptionId: s, Path: "~"})); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(h.s.Workspaces().List()); got != 3 {
		t.Fatalf("%d open, want 3", got)
	}

	closeTab()
	deadline := time.Now().Add(5 * time.Second)
	for {
		open := h.s.Workspaces().List()
		if len(open) == 1 && open[0].Owner == other {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the tab closed: %+v", open)
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, err := h.code.OpenWorkspace(ctx, connect.NewRequest(&aosv1.OpenWorkspaceRequest{SubscriptionId: sub, Path: "~"}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("opening for the ended subscription: %v, want NotFound", err)
	}
}

func TestOpenWorkspaceRefusesWhatItCannotOpen(t *testing.T) {
	h := newCodeHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, sub := h.subscribe(t, ctx)
	if err := os.WriteFile(filepath.Join(h.home, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		sub, path string
		want      connect.Code
	}{
		{sub, "~/notes.txt", connect.CodeInvalidArgument},
		{sub, "~/missing", connect.CodeNotFound},
		{sub, "", connect.CodeInvalidArgument},
		{"sub_unknown", "~", connect.CodeNotFound},
	} {
		_, err := h.code.OpenWorkspace(ctx, connect.NewRequest(&aosv1.OpenWorkspaceRequest{SubscriptionId: c.sub, Path: c.path}))
		if connect.CodeOf(err) != c.want {
			t.Errorf("%q for %q: %v, want %v", c.path, c.sub, err, c.want)
		}
	}
	if got := h.s.Workspaces().List(); len(got) != 0 {
		t.Errorf("a refused Workspace was recorded: %+v", got)
	}
}
