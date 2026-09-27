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
	"github.com/Aman123at/agentic-os/internal/audit"
	"github.com/Aman123at/agentic-os/internal/events"
	"github.com/Aman123at/agentic-os/internal/files"
	"github.com/Aman123at/agentic-os/internal/llm/fake"
	"github.com/Aman123at/agentic-os/internal/policy"
	"github.com/Aman123at/agentic-os/internal/store"
	"github.com/Aman123at/agentic-os/internal/task"
	"github.com/Aman123at/agentic-os/internal/tool"
)

// realmTasks is one Realm's TaskService: its own database and outputs, with a
// model that writes a file.
func realmTasks(t *testing.T, home string, turns ...fake.Turn) aosv1connect.TaskServiceClient {
	t.Helper()
	c, _ := realmTasksWith(t, home, nil, turns...)
	return c
}

// realmTasksWith is realmTasks with the Realm's file access (in Root Mode, with
// sensitive the Sensitive System File), and its Audit Log.
func realmTasksWith(t *testing.T, home string, sensitive *string, turns ...fake.Turn) (aosv1connect.TaskServiceClient, *audit.Log) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ops := files.Ops{Home: home, UID: os.Getuid()}
	m, err := task.New(task.Config{
		// In Root Mode nothing is Protected, not even a temp home under /var;
		// and with Landlock, Auto runs a Risky Action without asking.
		DB: db, Bus: events.New(), Provider: fake.New(turns...), Model: "gpt-test", Autonomy: policy.Auto, Home: home, Root: sensitive != nil, Landlock: true,
		Tools: tool.NewRegistry(tool.FilesTools()...), Outputs: filepath.Join(dir, "outputs"),
		NewEnv: func(env *tool.Env) (func(), error) {
			env.Files = func([]string) files.Runner { return files.InProcess{Ops: ops} }
			return func() {}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	auth, _ := newAuth(t)
	log := &audit.Log{DB: db}
	s := &Server{Auth: auth, Audit: log, Tasks: m, Home: home, UserFiles: files.InProcess{Ops: ops}}
	if sensitive != nil {
		s.Info = func() *aosv1.InfoResponse { return &aosv1.InfoResponse{RootMode: true} }
		s.sensitivity = func(p string, _ bool) (string, bool) {
			return "A mistake here can lock everyone out of sudo.", p == *sensitive
		}
	}
	srv := httptest.NewServer(auth.TCP(s.Handler()))
	t.Cleanup(srv.Close)
	return aosv1connect.NewTaskServiceClient(&http.Client{Transport: bearer{}}, srv.URL), log
}

func TestListChangesAndGetBeforeServeATasksBeforeImagesInItsRealmOnly(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "app.go")
	if err := os.WriteFile(path, []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	std := realmTasks(t, home,
		fake.Calls("", fake.Call{Name: "write_file", Args: map[string]any{"path": path, "content": "package app // changed\n", "overwrite": true}}),
		fake.Say("Changed app.go."))
	root := realmTasks(t, home)
	ctx := context.Background()

	created, err := std.CreateTask(ctx, connect.NewRequest(&aosv1.CreateTaskRequest{Prompt: "Change app.go", Interactive: true,
		Editor: &aosv1.EditorContext{Workspace: home}}))
	if err != nil {
		t.Fatal(err)
	}
	id := created.Msg.Task.Id
	if created.Msg.Task.Workspace != home {
		t.Errorf("Workspace = %q", created.Msg.Task.Workspace)
	}
	var changes []*aosv1.TaskFileChange
	for deadline := time.Now().Add(5 * time.Second); len(changes) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the Task's change never showed")
		}
		list, err := std.ListChanges(ctx, connect.NewRequest(&aosv1.ListChangesRequest{TaskId: id}))
		if err != nil {
			t.Fatal(err)
		}
		changes = list.Msg.Changes
	}
	if c := changes[0]; c.Path != path || c.Tool != "write_file" || !c.Existed || !c.Kept {
		t.Errorf("change %+v", c)
	}
	got, err := std.GetBefore(ctx, connect.NewRequest(&aosv1.GetBeforeRequest{TaskId: id, Path: path}))
	if err != nil || string(got.Msg.Content) != "package app\n" {
		t.Fatalf("GetBefore = %q, %v", got.Msg.GetContent(), err)
	}
	if _, err := std.GetBefore(ctx, connect.NewRequest(&aosv1.GetBeforeRequest{TaskId: id, Path: filepath.Join(home, "other.go")})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("GetBefore of a file it didn't change: %v", err)
	}
	listed, err := std.ListTasks(ctx, connect.NewRequest(&aosv1.ListTasksRequest{Workspace: home}))
	if err != nil || len(listed.Msg.Tasks) != 1 {
		t.Errorf("ListTasks(workspace) = %v, %v", listed, err)
	}

	// The other Realm has its own history: the Task is not there.
	if _, err := root.ListChanges(ctx, connect.NewRequest(&aosv1.ListChangesRequest{TaskId: id})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("ListChanges from the other Realm: %v", err)
	}
	if _, err := root.GetBefore(ctx, connect.NewRequest(&aosv1.GetBeforeRequest{TaskId: id, Path: path})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("GetBefore from the other Realm: %v", err)
	}

	// They go with the Task.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := std.DeleteTask(ctx, connect.NewRequest(&aosv1.DeleteTaskRequest{Id: id})); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatal(err)
		}
	}
	if _, err := std.ListChanges(ctx, connect.NewRequest(&aosv1.ListChangesRequest{TaskId: id})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("ListChanges after DeleteTask: %v", err)
	}
}

func TestChangeReviewKeepsAndDiscardsWhatTheAgentChanged(t *testing.T) {
	home := t.TempDir()
	at := func(name string) string { return filepath.Join(home, name) }
	for name, text := range map[string]string{"app.go": "a\nb\nc\n", "gone.txt": "x\n", "sudoers": "root\n"} {
		if err := os.WriteFile(at(name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sudoers := at("sudoers")
	write := func(name, text string) fake.Call {
		return fake.Call{Name: "write_file", Args: map[string]any{"path": at(name), "content": text, "overwrite": true}}
	}
	c, log := realmTasksWith(t, home, &sudoers,
		fake.Calls("", write("app.go", "A\nb\nC\n"), write("new.txt", "new\n"), fake.Call{Name: "delete", Args: map[string]any{"path": at("gone.txt")}}, write("sudoers", "root\nme\n")),
		fake.Say("Done."))
	ctx := context.Background()
	created, err := c.CreateTask(ctx, connect.NewRequest(&aosv1.CreateTaskRequest{Prompt: "Change things", Interactive: true, Autonomy: aosv1.Autonomy_AUTONOMY_AUTO}))
	if err != nil {
		t.Fatal(err)
	}
	id := created.Msg.Task.Id
	pending := func() map[string]*aosv1.TaskFileChange {
		t.Helper()
		list, err := c.ListChanges(ctx, connect.NewRequest(&aosv1.ListChangesRequest{TaskId: id}))
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]*aosv1.TaskFileChange{}
		for _, ch := range list.Msg.Changes {
			out[filepath.Base(ch.Path)] = ch
		}
		return out
	}
	for deadline := time.Now().Add(5 * time.Second); len(pending()) < 4; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("changes %v", pending())
		}
	}
	for name, ch := range pending() {
		if !ch.Pending || ch.Reviewed {
			t.Errorf("%s at first: %+v, want pending and not reviewed", name, ch)
		}
	}

	// Keep the first hunk of app.go: it is compared with that from now on.
	kept, err := c.ReviewChange(ctx, connect.NewRequest(&aosv1.ReviewChangeRequest{TaskId: id, Path: at("app.go"), Base: []byte("A\nb\nc\n")}))
	if err != nil || !kept.Msg.Change.Pending || !kept.Msg.Change.Reviewed {
		t.Fatalf("ReviewChange = %+v, %v; want reviewed, the second hunk still pending", kept.Msg.GetChange(), err)
	}
	if b, _ := c.GetBefore(ctx, connect.NewRequest(&aosv1.GetBeforeRequest{TaskId: id, Path: at("app.go")})); string(b.Msg.Content) != "A\nb\nc\n" {
		t.Errorf("GetBefore after a keep = %q", b.Msg.Content)
	}
	// Discarding the second hunk is the Desktop's own write of the lines.
	if err := os.WriteFile(at("app.go"), []byte("A\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ch := pending()["app.go"]; ch.Pending {
		t.Errorf("app.go after keep + discard: %+v, want decided", ch)
	}

	// Discarding a file the Agent created moves it to the Trash; one it
	// deleted comes back.
	if _, err := c.DiscardChange(ctx, connect.NewRequest(&aosv1.DiscardChangeRequest{TaskId: id, Path: at("new.txt")})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(at("new.txt")); !os.IsNotExist(err) {
		t.Errorf("new.txt after discard: %v", err)
	}
	if trashed, _ := filepath.Glob(filepath.Join(home, ".local/share/Trash/files/new.txt*")); len(trashed) != 1 {
		t.Errorf("new.txt is not in the Trash: %v", trashed)
	}
	if _, err := c.DiscardChange(ctx, connect.NewRequest(&aosv1.DiscardChangeRequest{TaskId: id, Path: at("gone.txt")})); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(at("gone.txt")); string(got) != "x\n" {
		t.Errorf("gone.txt after discard = %q", got)
	}

	// A Sensitive System File asks first, as FileService does.
	if _, err := c.DiscardChange(ctx, connect.NewRequest(&aosv1.DiscardChangeRequest{TaskId: id, Path: sudoers})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("an unconfirmed discard of a Sensitive System File: %v", err)
	}
	if got, _ := os.ReadFile(sudoers); string(got) != "root\nme\n" {
		t.Errorf("sudoers changed without confirming: %q", got)
	}
	if _, err := c.DiscardChange(ctx, connect.NewRequest(&aosv1.DiscardChangeRequest{TaskId: id, Path: sudoers, ConfirmSensitive: true})); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(sudoers); string(got) != "root\n" {
		t.Errorf("sudoers after a confirmed discard = %q", got)
	}
	for name, ch := range pending() {
		if ch.Pending {
			t.Errorf("%s is still pending: %+v", name, ch)
		}
	}
	entries, err := log.List(ctx, "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	var tools []string
	for _, e := range entries {
		tools = append(tools, e.Tool)
	}
	if !slices.Contains(tools, "discard_change") || !slices.Contains(tools, "write_sensitive") {
		t.Errorf("audit %v, want discard_change and write_sensitive", tools)
	}

	// Keep all takes the file as it is now.
	if err := os.WriteFile(at("app.go"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ch := pending()["app.go"]; !ch.Pending {
		t.Errorf("app.go changed again is not pending")
	}
	if all, err := c.ReviewChange(ctx, connect.NewRequest(&aosv1.ReviewChangeRequest{TaskId: id, Path: at("app.go"), KeepAll: true})); err != nil || all.Msg.Change.Pending {
		t.Errorf("keep all: %+v, %v", all.Msg.GetChange(), err)
	}
}
