package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
	"github.com/amantiwari/agentic-os/gen/go/aos/v1/aosv1connect"
	"github.com/amantiwari/agentic-os/internal/files"
)

// noLocks is a Protected with nothing locked.
type noLocks struct{}

func (noLocks) List(context.Context) ([]*aosv1.ListProtectedResponse_Entry, error) { return nil, nil }
func (noLocks) Protect(context.Context, string) error                              { return nil }
func (noLocks) Unprotect(context.Context, string) error                            { return nil }
func (noLocks) IsProtected(string) bool                                            { return false }

// bearer adds the access token to every request, as the in-container CLI does.
type bearer struct{}

func (bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+testToken)
	return http.DefaultTransport.RoundTrip(r)
}

func TestWatchSendsAFolderAgainWhenItChanges(t *testing.T) {
	home := t.TempDir()
	auth, _ := newAuth(t)
	ops := files.Ops{Home: home, Shared: filepath.Join(home, "shared"), UID: os.Getuid(), Scratch: []string{filepath.Join(home, "tmp")}}
	s := &Server{Auth: auth, Home: home, UserFiles: files.InProcess{Ops: ops}, Protected: noLocks{}, WatchInterval: 20 * time.Millisecond}
	srv := httptest.NewServer(auth.TCP(s.Handler()))
	defer srv.Close()
	client := aosv1connect.NewFileServiceClient(&http.Client{Transport: bearer{}}, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := client.Watch(ctx, connect.NewRequest(&aosv1.WatchRequest{Path: "~"}))
	if err != nil {
		t.Fatal(err)
	}
	names := func() []string {
		t.Helper()
		if !stream.Receive() {
			t.Fatalf("the stream ended: %v", stream.Err())
		}
		var out []string
		for _, e := range stream.Msg().Entries {
			out = append(out, e.Name)
		}
		return out
	}

	if got := names(); len(got) != 0 {
		t.Fatalf("first listing of an empty home: %v", got)
	}
	if err := os.WriteFile(filepath.Join(home, "new.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := names(); !slices.Equal(got, []string{"new.txt"}) {
		t.Fatalf("after a file appeared: %v", got)
	}
	if err := os.Remove(filepath.Join(home, "new.txt")); err != nil {
		t.Fatal(err)
	}
	if got := names(); len(got) != 0 {
		t.Fatalf("after the file went: %v", got)
	}

	// Leaving ends the stream.
	cancel()
	if stream.Receive() {
		t.Error("the stream sent a listing after the client left")
	}

	missing, err := client.Watch(context.Background(), connect.NewRequest(&aosv1.WatchRequest{Path: "~/missing"}))
	if err != nil {
		t.Fatal(err)
	}
	if missing.Receive() || connect.CodeOf(missing.Err()) != connect.CodeNotFound {
		t.Errorf("watching a missing folder: %v, want NotFound", missing.Err())
	}
	var ce *connect.Error
	if !errors.As(missing.Err(), &ce) {
		t.Errorf("want a Connect error, got %v", missing.Err())
	}
}
