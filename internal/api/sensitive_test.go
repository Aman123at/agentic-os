package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/gen/go/aos/v1/aosv1connect"
	"github.com/Aman123at/agentic-os/internal/audit"
	"github.com/Aman123at/agentic-os/internal/code"
	"github.com/Aman123at/agentic-os/internal/files"
	"github.com/Aman123at/agentic-os/internal/store"
)

// sensitiveMachine is FileService over a temp folder standing in for /, where
// <root>/etc/sudoers (and anything in <root>/etc/sudoers.d) is the Sensitive
// System File, since the real list names the real /etc.
type sensitiveMachine struct {
	c        aosv1connect.FileServiceClient
	url      string
	root     string
	sudoers  string
	log      *audit.Log
	rootMode *bool
}

func newSensitiveMachine(t *testing.T) sensitiveMachine {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "fs")
	sudoers := filepath.Join(root, "etc", "sudoers")
	if err := os.MkdirAll(filepath.Join(root, "etc", "sudoers.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sudoers, []byte("root ALL=(ALL) ALL\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	log := &audit.Log{DB: db}
	auth, _ := newAuth(t)
	rootMode := true
	etc := filepath.Join(root, "etc")
	s := &Server{
		Auth: auth, Audit: log, Home: root, Protected: noLocks{},
		UserFiles: files.InProcess{Ops: files.Ops{Home: root, UID: os.Getuid(), Scratch: []string{filepath.Join(dir, "tmp")}}},
		Info:      func() *aosv1.InfoResponse { return &aosv1.InfoResponse{RootMode: rootMode} },
		Backups:   &code.Backups{Dir: filepath.Join(dir, "backups")},
		Checker: code.Checker{
			Rules: func(path, file string) (string, []string, string) {
				if path != sudoers {
					return "", nil, ""
				}
				return "visudo", []string{"-c", "-f", file}, "visudo -c"
			},
			LookPath: func(name string) (string, error) { return name, nil },
			Command: func(ctx context.Context, _ string, args ...string) ([]byte, error) {
				return exec.CommandContext(ctx, "sh", "-c", `grep -q ALL "$1" || { echo "syntax error"; exit 1; }`, "sh", args[len(args)-1]).CombinedOutput()
			},
		},
	}
	s.sensitivity = func(p string, tree bool) (string, bool) {
		why := "A mistake here can lock everyone out of sudo."
		if p == sudoers || strings.HasPrefix(p, sudoers+".d/") || (tree && (p == etc || p == root)) {
			return why, true
		}
		return "", false
	}
	srv := httptest.NewServer(auth.TCP(s.Handler()))
	t.Cleanup(srv.Close)
	return sensitiveMachine{c: aosv1connect.NewFileServiceClient(&http.Client{Transport: bearer{}}, srv.URL), url: srv.URL, root: root, sudoers: sudoers, log: log, rootMode: &rootMode}
}

func (m sensitiveMachine) read(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(m.sudoers)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInRootModeASensitiveSystemFileChangesOnlyWhenConfirmed(t *testing.T) {
	m := newSensitiveMachine(t)
	ctx := context.Background()

	info, err := m.c.Stat(ctx, connect.NewRequest(&aosv1.StatRequest{Path: m.sudoers}))
	if err != nil || !strings.Contains(info.Msg.Info.SensitiveReason, "sudo") {
		t.Fatalf("Stat: %v, %v; want its sensitive_reason", info, err)
	}

	// Unconfirmed: refused, with the path and the reason, and nothing written.
	_, err = m.c.Write(ctx, connect.NewRequest(&aosv1.WriteRequest{Path: m.sudoers, Content: []byte("oops\n"), Overwrite: true}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("unconfirmed write: %v, want FailedPrecondition", err)
	}
	var detail *aosv1.SensitivePath
	for _, d := range err.(*connect.Error).Details() {
		if v, derr := d.Value(); derr == nil {
			detail, _ = v.(*aosv1.SensitivePath)
		}
	}
	if detail == nil || detail.Path != m.sudoers || !strings.Contains(detail.Reason, "sudo") {
		t.Fatalf("the refusal's detail = %v, want the path and the reason", detail)
	}
	if m.read(t) != "root ALL=(ALL) ALL\n" {
		t.Fatal("a refused write changed the file")
	}
	for name, call := range map[string]func() error{
		"move": func() error {
			_, err := m.c.Move(ctx, connect.NewRequest(&aosv1.MoveRequest{Source: m.sudoers, Destination: m.sudoers + ".bak"}))
			return err
		},
		"copy": func() error {
			_, err := m.c.Copy(ctx, connect.NewRequest(&aosv1.CopyRequest{Source: m.sudoers, Destination: m.sudoers + ".d/new"}))
			return err
		},
		"delete": func() error {
			_, err := m.c.Delete(ctx, connect.NewRequest(&aosv1.DeleteRequest{Path: filepath.Join(m.root, "etc")}))
			return err
		},
		"mkdir": func() error {
			_, err := m.c.MakeFolder(ctx, connect.NewRequest(&aosv1.MakeFolderRequest{Path: m.sudoers + ".d/sub"}))
			return err
		},
	} {
		if err := call(); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Errorf("unconfirmed %s: %v, want FailedPrecondition", name, err)
		}
	}

	// Confirmed: written, the old version kept, and audited as write_sensitive.
	if _, err := m.c.Write(ctx, connect.NewRequest(&aosv1.WriteRequest{Path: m.sudoers, Content: []byte("aos ALL=(ALL) ALL\n"), Overwrite: true, ConfirmSensitive: true})); err != nil {
		t.Fatal(err)
	}
	if m.read(t) != "aos ALL=(ALL) ALL\n" {
		t.Fatalf("after a confirmed write: %q", m.read(t))
	}
	kept, err := m.c.ListSensitiveBackups(ctx, connect.NewRequest(&aosv1.ListSensitiveBackupsRequest{Path: m.sudoers}))
	if err != nil || len(kept.Msg.Backups) != 1 {
		t.Fatalf("kept versions: %v, %v; want one", kept, err)
	}
	entries, _ := m.log.List(ctx, "", 0, 0)
	if len(entries) == 0 || entries[0].Tool != "write_sensitive" {
		t.Errorf("the Audit Log's newest entry is %v, want write_sensitive", entries[0])
	}

	// Restore puts the kept version back, and keeps the one it replaced.
	if _, err := m.c.RestoreSensitiveBackup(ctx, connect.NewRequest(&aosv1.RestoreSensitiveBackupRequest{Path: m.sudoers, Id: kept.Msg.Backups[0].Id})); err != nil {
		t.Fatal(err)
	}
	if m.read(t) != "root ALL=(ALL) ALL\n" {
		t.Errorf("after restoring: %q", m.read(t))
	}
	kept, _ = m.c.ListSensitiveBackups(ctx, connect.NewRequest(&aosv1.ListSensitiveBackupsRequest{Path: m.sudoers}))
	if len(kept.Msg.Backups) != 2 {
		t.Errorf("%d kept after the restore, want 2", len(kept.Msg.Backups))
	}
}

func TestCheckSensitiveRunsTheChecker(t *testing.T) {
	m := newSensitiveMachine(t)
	ctx := context.Background()
	for content, want := range map[string]bool{"root ALL=(ALL) ALL\n": true, "root ALL=(ALL\n": true, "nonsense\n": false} {
		resp, err := m.c.CheckSensitive(ctx, connect.NewRequest(&aosv1.CheckSensitiveRequest{Path: m.sudoers, Content: []byte(content)}))
		if err != nil || resp.Msg.Checker != "visudo -c" || resp.Msg.Passed != want {
			t.Errorf("%q: %v, %v; want passed=%v", content, resp, err, want)
		}
	}
	// Not a Sensitive System File: nothing to check.
	resp, err := m.c.CheckSensitive(ctx, connect.NewRequest(&aosv1.CheckSensitiveRequest{Path: filepath.Join(m.root, "notes.md")}))
	if err != nil || resp.Msg.Checker != "" {
		t.Errorf("an ordinary file: %v, %v", resp, err)
	}
}

func TestStandardModeHasNoSensitiveSystemFiles(t *testing.T) {
	m := newSensitiveMachine(t)
	*m.rootMode = false
	ctx := context.Background()
	info, err := m.c.Stat(ctx, connect.NewRequest(&aosv1.StatRequest{Path: m.sudoers}))
	if err != nil || info.Msg.Info.SensitiveReason != "" {
		t.Errorf("Stat in Standard: %v, %v; want no sensitive_reason", info, err)
	}
	// Whatever aos may write it writes as before; the real sudoers it can't.
	if _, err := m.c.Write(ctx, connect.NewRequest(&aosv1.WriteRequest{Path: m.sudoers, Content: []byte("x\n"), Overwrite: true})); err != nil {
		t.Errorf("a write in Standard: %v", err)
	}
	if kept, _ := m.c.ListSensitiveBackups(ctx, connect.NewRequest(&aosv1.ListSensitiveBackupsRequest{Path: m.sudoers})); len(kept.Msg.Backups) != 0 {
		t.Errorf("Standard kept a version: %v", kept.Msg.Backups)
	}
}

func TestAnUploadNeverReplacesASensitiveSystemFileInRootMode(t *testing.T) {
	m := newSensitiveMachine(t)
	req, err := http.NewRequest(http.MethodPost, m.url+"/upload?overwrite=1&path="+url.QueryEscape(m.sudoers), strings.NewReader("oops\n"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: bearer{}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusPreconditionFailed || m.read(t) != "root ALL=(ALL) ALL\n" {
		t.Errorf("upload over it: %d, file %q; want 412 and the file unchanged", resp.StatusCode, m.read(t))
	}
}
