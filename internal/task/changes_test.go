package task

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/files"
	"github.com/Aman123at/agentic-os/internal/llm"
	"github.com/Aman123at/agentic-os/internal/llm/fake"
	"github.com/Aman123at/agentic-os/internal/policy"
	"github.com/Aman123at/agentic-os/internal/tool"
)

// withOutputs keeps before-images in the harness's outputs folder, as aosd does.
func withOutputs(t *testing.T, h *harness) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "outputs")
	h.m.cfg.Outputs = dir
	return dir
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func changesOf(t *testing.T, h *harness, id string) map[string]*aosv1.TaskFileChange {
	t.Helper()
	list, err := h.m.Changes(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*aosv1.TaskFileChange{}
	for _, c := range list {
		out[strings.TrimPrefix(c.Path, h.home+"/")] = c
	}
	return out
}

func beforeOf(t *testing.T, h *harness, id, rel string) string {
	t.Helper()
	_, content, err := h.m.Before(context.Background(), id, filepath.Join(h.home, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestATaskKeepsEachFilesBeforeImageOnceAcrossItsRuns(t *testing.T) {
	h := newHarness(t, policy.Auto,
		fake.Calls("",
			fake.Call{Name: "write_file", Args: map[string]any{"path": "~/src/app.go", "content": "package app // one\n", "overwrite": true}},
			fake.Call{Name: "edit_file", Args: map[string]any{"path": "~/src/app.go", "edits": []any{map[string]any{"old": "one", "new": "two", "all": nil}}}},
			fake.Call{Name: "write_file", Args: map[string]any{"path": "~/src/new.go", "content": "package app\n", "overwrite": nil}},
			// Fails: it exists and overwrite is not set, so nothing changed.
			fake.Call{Name: "write_file", Args: map[string]any{"path": "~/src/keep.go", "content": "x", "overwrite": nil}},
		),
		fake.Say("Done."),
		// The Follow-up edits app.go again and moves one file, deletes another.
		fake.Calls("",
			fake.Call{Name: "edit_file", Args: map[string]any{"path": "~/src/app.go", "edits": []any{map[string]any{"old": "two", "new": "three", "all": nil}}}},
			fake.Call{Name: "move", Args: map[string]any{"source": "~/src/old.go", "destination": "~/src/renamed.go", "overwrite": nil}},
			fake.Call{Name: "delete", Args: map[string]any{"path": "~/src/gone"}},
		),
		fake.Say("Done again."),
	)
	outputs := withOutputs(t, h)
	write(t, filepath.Join(h.home, "src/app.go"), "package app\n")
	write(t, filepath.Join(h.home, "src/keep.go"), "keep")
	write(t, filepath.Join(h.home, "src/old.go"), "package old\n")
	write(t, filepath.Join(h.home, "src/gone/a.txt"), "a")

	created, err := h.m.Create(context.Background(), "Change app.go", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	got := changesOf(t, h, created.Id)
	if len(got) != 2 {
		t.Fatalf("changes %v, want app.go and new.go", got)
	}
	if c := got["src/app.go"]; c == nil || c.Tool != "write_file" || !c.Existed || !c.Kept || c.Whole || c.Size != int64(len("package app\n")) {
		t.Errorf("app.go: %+v", c)
	}
	if b := beforeOf(t, h, created.Id, "src/app.go"); b != "package app\n" {
		t.Errorf("app.go before = %q, want the file before the Task's first change", b)
	}
	if c := got["src/new.go"]; c == nil || c.Existed || c.Kept {
		t.Errorf("new.go: %+v, want did not exist", c)
	}

	if _, err := h.m.FollowUp(context.Background(), created.Id, "Once more", true); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	got = changesOf(t, h, created.Id)
	if b := beforeOf(t, h, created.Id, "src/app.go"); b != "package app\n" {
		t.Errorf("after the Follow-up app.go before = %q; the first change's image must stay", b)
	}
	if c := got["src/old.go"]; c == nil || c.Tool != "move" || !c.Kept || beforeOf(t, h, created.Id, "src/old.go") != "package old\n" {
		t.Errorf("old.go (moved away): %+v", c)
	}
	if c := got["src/renamed.go"]; c == nil || c.Existed {
		t.Errorf("renamed.go (moved to): %+v", c)
	}
	if c := got["src/gone"]; c == nil || c.Tool != "delete" || !c.Folder || c.Kept {
		t.Errorf("gone/ (deleted folder): %+v", c)
	}

	// Deleting the Task deletes them.
	if err := h.m.Delete(context.Background(), created.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Changes(context.Background(), created.Id); !errors.Is(err, ErrNotFound) {
		t.Errorf("Changes after delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputs, created.Id, "before")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the before-images outlived the Task: %v", err)
	}
}

func TestBeforeOfAFileTheTaskDidNotChangeIsNotFound(t *testing.T) {
	h := newHarness(t, policy.Auto, fake.Say("Nothing to do."))
	withOutputs(t, h)
	created, _ := h.m.Create(context.Background(), "Nothing", 0, true)
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	if _, _, err := h.m.Before(context.Background(), created.Id, filepath.Join(h.home, "x")); !errors.Is(err, ErrNotFound) {
		t.Errorf("Before: %v, want not found", err)
	}
	if _, err := h.m.Changes(context.Background(), "t_nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Changes of an unknown Task: %v", err)
	}
}

// memBefores records what the Files Tools keep, for the size and type rules.
type memBefores map[string]tool.Before

func (m memBefores) Has(p string) bool { _, ok := m[p]; return ok }
func (m memBefores) Keep(_ context.Context, b tool.Before) error {
	m[b.Path] = b
	return nil
}

func TestBigAndBinaryFilesAreKeptWholeAndHugeOnesNotAtAll(t *testing.T) {
	home := t.TempDir()
	ops := files.Ops{Home: home, UID: os.Getuid()}
	kept := memBefores{}
	env := &tool.Env{Home: home, Files: func([]string) files.Runner { return files.InProcess{Ops: ops} }, Befores: kept,
		Stat: func(p string) (bool, bool) { fi, err := os.Stat(p); return err == nil, err == nil && fi.IsDir() }}
	big := strings.Repeat("line\n", tool.MaxBeforeLines/5+1)
	write(t, filepath.Join(home, "big.txt"), big)
	write(t, filepath.Join(home, "bin.dat"), "a\x00b")
	huge, err := os.Create(filepath.Join(home, "huge.img"))
	if err != nil {
		t.Fatal(err)
	}
	if err := huge.Truncate(tool.MaxBeforeKeep + 1); err != nil { // sparse: no 64 MiB written
		t.Fatal(err)
	}
	huge.Close()

	for _, name := range []string{"big.txt", "bin.dat", "huge.img"} {
		w, _ := tool.NewRegistry(tool.FilesTools()...).Get("write_file")
		call, err := w.Prepare(context.Background(), env, []byte(`{"path":"~/`+name+`","content":"new","overwrite":true}`))
		if err != nil {
			t.Fatal(err)
		}
		if res := call.Run(context.Background(), tool.Run{}); res.Error {
			t.Fatalf("%s: %s", name, res.Output)
		}
	}
	if b := kept[filepath.Join(home, "big.txt")]; !b.Kept || !b.Whole || string(b.Content) != big {
		t.Errorf("big.txt: kept %v whole %v, %d bytes", b.Kept, b.Whole, len(b.Content))
	}
	if b := kept[filepath.Join(home, "bin.dat")]; !b.Kept || !b.Whole {
		t.Errorf("bin.dat: kept %v whole %v", b.Kept, b.Whole)
	}
	if b := kept[filepath.Join(home, "huge.img")]; b.Kept || !b.Whole || !b.Existed || b.Size != tool.MaxBeforeKeep+1 {
		t.Errorf("huge.img: %+v", b)
	}
}

func TestATaskFromCodeBelongsToItsWorkspaceAndTheAgentHearsWhereTheUserIs(t *testing.T) {
	h := newHarness(t, policy.Auto, fake.Say("Looked."), fake.Say("Looked again."))
	ws := filepath.Join(h.home, "project")
	editor := &aosv1.EditorContext{Workspace: ws + "/", ActiveFile: filepath.Join(ws, "main.go"), OpenFiles: []string{filepath.Join(ws, "main.go")},
		Selection: "func main() {}\n", SelectionStartLine: 3, SelectionEndLine: 3}
	created, err := h.m.CreateInEditor(context.Background(), "Explain main", 0, true, editor)
	if err != nil {
		t.Fatal(err)
	}
	if created.Workspace != ws {
		t.Errorf("Workspace = %q, want %q", created.Workspace, ws)
	}
	other, _ := h.m.Create(context.Background(), "Something else", 0, true)
	h.waitState(t, created.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)
	h.waitState(t, other.Id, aosv1.TaskState_TASK_STATE_SUCCEEDED)

	in, err := h.m.ListIn(context.Background(), 10, ws)
	if err != nil || len(in) != 1 || in[0].Id != created.Id || in[0].Workspace != ws {
		t.Errorf("ListIn(%s) = %v, %v", ws, in, err)
	}
	if all, _ := h.m.List(context.Background(), 10); len(all) != 2 {
		t.Errorf("List = %d Tasks, want both", len(all))
	}

	var note string
	for _, req := range h.model.Requests() {
		for _, it := range req.Input {
			if it.Type == llm.Message && it.Role == "developer" && strings.HasPrefix(it.Text, "# Working in Code") {
				note = it.Text
			}
		}
	}
	for _, want := range []string{ws + " (the Workspace)", "The file in front: main.go", "lines 3–3 of main.go"} {
		if !strings.Contains(note, want) {
			t.Errorf("the Agent's note lacks %q:\n%s", want, note)
		}
	}
}
