package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ops := files.Ops{Home: home, UID: os.Getuid()}
	m, err := task.New(task.Config{
		DB: db, Bus: events.New(), Provider: fake.New(turns...), Model: "gpt-test", Autonomy: policy.Auto, Home: home,
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
	srv := httptest.NewServer(auth.TCP((&Server{Auth: auth, Audit: &audit.Log{DB: db}, Tasks: m}).Handler()))
	t.Cleanup(srv.Close)
	return aosv1connect.NewTaskServiceClient(&http.Client{Transport: bearer{}}, srv.URL)
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
