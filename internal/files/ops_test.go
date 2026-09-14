package files

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCreatesFoldersAndOnlyOverwritesWhenAsked(t *testing.T) {
	ops, home, _ := machine(t)
	p := filepath.Join(home, "new", "deep", "notes.md")
	if err := ops.Write(p, []byte("one"), false); err != nil {
		t.Fatal(err)
	}
	if err := ops.Write(p, []byte("two"), false); !errors.Is(err, ErrExists) {
		t.Fatalf("second Write without overwrite: %v, want ErrExists", err)
	}
	if err := ops.Write(p, []byte("two"), true); err != nil || readFile(t, p) != "two" {
		t.Fatalf("overwrite: %v, content %q", err, readFile(t, p))
	}
}

func TestEditReplacesExactlyOneMatch(t *testing.T) {
	ops, home, _ := machine(t)
	p := filepath.Join(home, "app.conf")
	writeFile(t, p, "port=80\nhost=a\nport=80\n")

	err := ops.Edit(p, []Edit{{Old: "port=80", New: "port=8080"}})
	if err == nil || !strings.Contains(err.Error(), "2 times") {
		t.Fatalf("ambiguous edit: %v, want an error naming 2 matches", err)
	}
	if err := ops.Edit(p, []Edit{{Old: "host=a", New: "host=b"}, {Old: "missing", New: "x"}}); err == nil {
		t.Fatal("edit with a missing match succeeded")
	}
	if readFile(t, p) != "port=80\nhost=a\nport=80\n" {
		t.Fatal("a failed edit changed the file")
	}
	if err := ops.Edit(p, []Edit{{Old: "host=a", New: "host=b"}, {Old: "port=80", New: "port=8080", All: true}}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p); got != "port=8080\nhost=b\nport=8080\n" {
		t.Errorf("content %q", got)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o644 {
		t.Errorf("edit changed the mode to %v", fi.Mode())
	}
}

func TestMoveAndCopyNeverReplaceUnlessAsked(t *testing.T) {
	ops, home, _ := machine(t)
	src := filepath.Join(home, "src")
	writeFile(t, filepath.Join(src, "a.txt"), "a")
	writeFile(t, filepath.Join(src, "sub", "b.sh"), "b")
	if err := os.Chmod(filepath.Join(src, "sub", "b.sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	cp := filepath.Join(home, "backup", "src")
	if err := ops.Copy(src, cp, false); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(cp, "sub", "b.sh")) != "b" {
		t.Error("folder not copied recursively")
	}
	if fi, err := os.Stat(filepath.Join(cp, "sub", "b.sh")); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("copy lost the mode: %v %v", fi.Mode(), err)
	}
	if err := ops.Copy(src, cp, false); !errors.Is(err, ErrExists) {
		t.Errorf("copy onto an existing folder: %v, want ErrExists", err)
	}

	dst := filepath.Join(home, "moved.txt")
	writeFile(t, dst, "existing")
	if err := ops.Move(filepath.Join(src, "a.txt"), dst, false); !errors.Is(err, ErrExists) {
		t.Fatalf("move onto an existing file: %v, want ErrExists", err)
	}
	if err := ops.Move(filepath.Join(src, "a.txt"), dst, true); err != nil || readFile(t, dst) != "a" {
		t.Fatalf("move with overwrite: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(src, "a.txt")); !os.IsNotExist(err) {
		t.Error("source still exists after move")
	}
}

func TestReadTextReturnsLineRangesAndRecognisesBinaryFiles(t *testing.T) {
	ops, home, _ := machine(t)
	var b strings.Builder
	for i := 1; i <= 500; i++ {
		b.WriteString("line " + strings.Repeat("x", i%7) + "\n")
	}
	p := filepath.Join(home, "big.txt")
	writeFile(t, p, b.String())

	got, err := ops.ReadText(p, 10, 12, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "line xxx\nline xxxx\nline xxxxx\n" || got.FirstLine != 10 || got.LastLine != 12 || got.TotalLines != 500 || got.Truncated {
		t.Errorf("lines 10-12: %+v", got)
	}

	got, err = ops.ReadText(p, 0, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated || len(got.Content) > 100 || !strings.HasSuffix(got.Content, "\n") || got.FirstLine != 1 || got.LastLine < 5 {
		t.Errorf("whole file capped at 100 bytes: %+v", got)
	}

	bin := filepath.Join(home, "blob.bin")
	writeFile(t, bin, "\x7fELF\x00\x01\x02")
	if got, err := ops.ReadText(bin, 0, 0, 1<<20); err != nil || !got.Binary || got.Content != "" {
		t.Errorf("binary file: %+v, %v", got, err)
	}
}

func TestReadReturnsRawChunksAndSignalsEOF(t *testing.T) {
	ops, home, _ := machine(t)
	// A binary payload with a NUL byte, which ReadText would refuse.
	blob := []byte("PNG\x00\x01\x02\x03rest of the bytes")
	p := filepath.Join(home, "pic.png")
	writeFile(t, p, string(blob))

	got, err := ops.Read(p, 0, 4)
	if err != nil || string(got.Content) != "PNG\x00" || got.EOF {
		t.Errorf("first 4 bytes: %q eof=%v err=%v", got.Content, got.EOF, err)
	}
	got, err = ops.Read(p, 4, 0) // limit 0 reads to the end
	if err != nil || string(got.Content) != string(blob[4:]) || !got.EOF {
		t.Errorf("rest: %q eof=%v err=%v", got.Content, got.EOF, err)
	}
	if _, err := ops.Read(home, 0, 0); err == nil {
		t.Error("reading a folder should fail")
	}
}

func TestListStatAndSearch(t *testing.T) {
	ops, home, _ := machine(t)
	writeFile(t, filepath.Join(home, "proj", "main.go"), "package main\n// TODO: port\n")
	writeFile(t, filepath.Join(home, "proj", "README.md"), "todo list\n")
	writeFile(t, filepath.Join(home, "proj", ".git", "config"), "TODO in git internals\n")
	writeFile(t, filepath.Join(home, "proj", "node_modules", "x", "index.js"), "// TODO\n")
	if err := os.Symlink("main.go", filepath.Join(home, "proj", "link.go")); err != nil {
		t.Fatal(err)
	}

	entries, err := ops.List(filepath.Join(home, "proj"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != ".git,node_modules,link.go,main.go,README.md" {
		t.Errorf("List = %v (folders first, then files by name, ignoring case)", names)
	}
	link, err := ops.Stat(filepath.Join(home, "proj", "link.go"))
	if err != nil || !link.Symlink || link.LinkTarget != "main.go" || link.Dir {
		t.Errorf("Stat(link) = %+v, %v", link, err)
	}

	matches, err := ops.Search(home, SearchQuery{Content: "TODO", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Path != filepath.Join(home, "proj", "main.go") || matches[0].Line != 2 || matches[0].Text != "// TODO: port" {
		t.Errorf("content search skipping .git and node_modules: %+v", matches)
	}
	matches, err = ops.Search(home, SearchQuery{Name: "*.md", Limit: 10})
	if err != nil || len(matches) != 1 || matches[0].Line != 0 {
		t.Errorf("name search: %+v, %v", matches, err)
	}
}
