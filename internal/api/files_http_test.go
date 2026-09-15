package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amantiwari/agentic-os/internal/audit"
	"github.com/amantiwari/agentic-os/internal/files"
	"github.com/amantiwari/agentic-os/internal/store"
)

// fileServer is the API over a temp home folder, with file operations in
// process; the worker process itself is tested in internal/files.
func fileServer(t *testing.T) (http.Handler, string, *audit.Log) {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	log := &audit.Log{DB: db}
	ops := files.Ops{Home: home, Shared: filepath.Join(dir, "shared"), UID: os.Getuid(), Scratch: []string{filepath.Join(dir, "tmp")}}
	auth, _ := newAuth(t)
	s := &Server{Auth: auth, Audit: log, Home: home, UserFiles: files.InProcess{Ops: ops}}
	return auth.TCP(s.Handler()), home, log
}

func send(t *testing.T, h http.Handler, method, target string, body io.Reader, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	req.Host = "localhost:7700"
	req.Header.Set("Authorization", "Bearer "+testToken)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRawFilesStreamRangesAndNeverRunInTheDesktop(t *testing.T) {
	h, home, _ := fileServer(t)
	video := bytes.Repeat([]byte("0123456789"), 100)
	for name, content := range map[string][]byte{
		"video.mp4": video,
		"page.html": []byte("<script>alert(document.cookie)</script>"),
		"pic.svg":   []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`),
	} {
		if err := os.WriteFile(filepath.Join(home, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rec := send(t, h, "GET", "/files/raw?path=~/video.mp4", nil, nil)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), video) || rec.Header().Get("Content-Type") != "video/mp4" ||
		!strings.HasPrefix(rec.Header().Get("Content-Disposition"), "inline") || rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Errorf("the whole video: %d %v, %d bytes", rec.Code, rec.Header(), rec.Body.Len())
	}
	rec = send(t, h, "GET", "/files/raw?path=~/video.mp4", nil, map[string]string{"Range": "bytes=10-19"})
	if rec.Code != http.StatusPartialContent || rec.Header().Get("Content-Range") != "bytes 10-19/1000" || !bytes.Equal(rec.Body.Bytes(), video[10:20]) {
		t.Errorf("a range: %d %q %q", rec.Code, rec.Header().Get("Content-Range"), rec.Body.String())
	}
	rec = send(t, h, "GET", "/files/raw?path=~/video.mp4", nil, map[string]string{"Range": "bytes=5000-"})
	if rec.Code != http.StatusRequestedRangeNotSatisfiable || rec.Header().Get("Content-Range") != "bytes */1000" {
		t.Errorf("a range past the end: %d %q", rec.Code, rec.Header().Get("Content-Range"))
	}
	if rec := send(t, h, "GET", "/files/raw?path=~/video.mp4&download=1", nil, nil); !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("download=1: %q, want an attachment", rec.Header().Get("Content-Disposition"))
	}

	// Anything that could run script downloads instead, and every response is sandboxed.
	for _, name := range []string{"page.html", "pic.svg", "video.mp4"} {
		rec := send(t, h, "GET", "/files/raw?path=~/"+name, nil, nil)
		if rec.Header().Get("Content-Security-Policy") != "sandbox" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: CSP %q nosniff %q", name, rec.Header().Get("Content-Security-Policy"), rec.Header().Get("X-Content-Type-Options"))
		}
		if name != "video.mp4" && !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") {
			t.Errorf("%s opens in place: %q", name, rec.Header().Get("Content-Disposition"))
		}
	}

	for target, want := range map[string]int{
		"/files/raw?path=~/missing.mp4": http.StatusNotFound,
		"/files/raw?path=~":             http.StatusBadRequest,
		"/files/raw?path=":              http.StatusBadRequest,
	} {
		if rec := send(t, h, "GET", target, nil, nil); rec.Code != want {
			t.Errorf("%s: %d, want %d", target, rec.Code, want)
		}
	}
}

func TestUploadsBecomeFilesAndAreAudited(t *testing.T) {
	h, home, log := fileServer(t)
	p := filepath.Join(home, "up", "a.bin")

	rec := send(t, h, "POST", "/upload?path=~/up/a.bin", strings.NewReader("hello"), nil)
	var reply struct {
		Path string
		Size int64
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &reply) != nil || reply.Path != p || reply.Size != 5 || readFile(t, p) != "hello" {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	if rec := send(t, h, "POST", "/upload?path=~/up/a.bin", strings.NewReader("again"), nil); rec.Code != http.StatusConflict {
		t.Errorf("an upload over an existing file: %d, want 409", rec.Code)
	}
	if rec := send(t, h, "POST", "/upload?path=~/up/a.bin&overwrite=1", strings.NewReader("again"), nil); rec.Code != http.StatusOK || readFile(t, p) != "again" {
		t.Errorf("overwrite: %d, file %q", rec.Code, readFile(t, p))
	}

	// Without a length, a cut-short upload could not be told from a whole one.
	req := httptest.NewRequest("POST", "/upload?path=~/up/b.bin", strings.NewReader("x"))
	req.Host, req.ContentLength = "localhost:7700", -1
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusLengthRequired {
		t.Errorf("an upload without a length: %d, want 411", rec.Code)
	}

	entries, err := log.List(context.Background(), "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].Tool != "upload" || !strings.Contains(entries[1].ResultSummary, "exists") {
		t.Errorf("the Audit Log should hold the three uploads, the refused one with its reason: %v", entries)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}
