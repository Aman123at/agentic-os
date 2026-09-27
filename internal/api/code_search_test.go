package api

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
)

// openProject opens a Workspace on a small project and drains its event
// stream, as the Desktop's tab would.
func openProject(t *testing.T, h codeHarness, ctx context.Context) (root, id string) {
	t.Helper()
	root = filepath.Join(h.home, "project")
	writeFiles(t, root, map[string]string{
		"main.go":                 "package main\n\nfunc main() { Run() }\n",
		"src/util.ts":             "export const run = 1;\n",
		"src/utilities/helper.ts": "// nothing here\n",
		".gitignore":              "*.log\n",
		"debug.log":               "Run\n",
	})
	stream, sub := h.subscribe(t, ctx)
	go func() {
		for stream.Receive() {
		}
	}()
	resp, err := h.code.OpenWorkspace(ctx, connect.NewRequest(&aosv1.OpenWorkspaceRequest{SubscriptionId: sub, Path: root}))
	if err != nil {
		t.Fatal(err)
	}
	return root, resp.Msg.Id
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFindFilesIsQuickOpenOverTheWorkspace(t *testing.T) {
	h := newCodeHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root, id := openProject(t, h, ctx)

	find := func(q string) []string {
		t.Helper()
		resp, err := h.code.FindFiles(ctx, connect.NewRequest(&aosv1.FindFilesRequest{WorkspaceId: id, Query: q}))
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, f := range resp.Msg.Files {
			out = append(out, f.Path)
		}
		return out
	}
	if got, want := find("util"), []string{filepath.Join(root, "src/util.ts"), filepath.Join(root, "src/utilities/helper.ts")}; !slices.Equal(got, want) {
		t.Errorf("util: %q, want %q", got, want)
	}
	if got := find("debug"); len(got) != 0 {
		t.Errorf(".gitignore'd files are not offered: %q", got)
	}
	resp, _ := h.code.FindFiles(ctx, connect.NewRequest(&aosv1.FindFilesRequest{WorkspaceId: id, Query: "main"}))
	if f := resp.Msg.Files[0]; !slices.Equal(f.Positions, []uint32{0, 1, 2, 3}) || resp.Msg.Total != 4 {
		t.Errorf("main: %+v, total %d", f, resp.Msg.Total)
	}

	// A file created afterwards turns up from the watch.
	writeFiles(t, root, map[string]string{"cmd/server.go": "package cmd\n"})
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(find("server"), filepath.Join(root, "cmd/server.go")) {
		if time.Now().After(deadline) {
			t.Fatal("a new file never turned up in Quick Open")
		}
		time.Sleep(20 * time.Millisecond)
	}

	_, err := h.code.FindFiles(ctx, connect.NewRequest(&aosv1.FindFilesRequest{WorkspaceId: "ws_nope", Query: "x"}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("an unknown Workspace: %v, want NotFound", err)
	}
}

func TestSearchTextStreamsMatchesThenASummary(t *testing.T) {
	h := newCodeHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root, id := openProject(t, h, ctx)

	stream, err := h.code.SearchText(ctx, connect.NewRequest(&aosv1.SearchTextRequest{WorkspaceId: id, Query: "run"}))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	var summary *aosv1.SearchTextSummary
	for stream.Receive() {
		for _, f := range stream.Msg().Files {
			paths = append(paths, f.Path)
			if f.Path == filepath.Join(root, "main.go") {
				m := f.Matches[0]
				if m.Line != 3 || m.Text != "func main() { Run() }" || len(m.Ranges) != 1 || m.Ranges[0].Start != 14 || m.Ranges[0].End != 17 {
					t.Errorf("main.go's match: %+v", m)
				}
			}
		}
		if s := stream.Msg().Summary; s != nil {
			summary = s
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(root, "main.go"), filepath.Join(root, "src/util.ts")}; !slices.Equal(paths, want) {
		t.Errorf("files %q, want %q (not the ignored debug.log)", paths, want)
	}
	if summary == nil || summary.Files != 2 || summary.Matches != 2 || summary.Engine == "" {
		t.Errorf("summary %+v", summary)
	}

	bad, _ := h.code.SearchText(ctx, connect.NewRequest(&aosv1.SearchTextRequest{WorkspaceId: id, Query: "(", Regex: true}))
	for bad.Receive() {
	}
	if connect.CodeOf(bad.Err()) != connect.CodeInvalidArgument {
		t.Errorf("a bad regex: %v, want InvalidArgument", bad.Err())
	}
}
