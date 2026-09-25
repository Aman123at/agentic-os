package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/files"
	"github.com/Aman123at/agentic-os/internal/task"
	"github.com/Aman123at/agentic-os/internal/tool"
)

// Change Review (PLAN.md M8.11–M8.12): the files a Task changed with its
// Tools, what each is compared with — its before-image, with the hunks the
// user kept — and keeping or discarding what is left. Each reads the Realm's
// own history, so it is Realm-scoped by the database it reads; files are read
// and written as the Realm's user.

// ListChanges lists the files a Task changed, each saying whether anything is
// left to decide.
func (t taskService) ListChanges(ctx context.Context, req *connect.Request[aosv1.ListChangesRequest]) (*connect.Response[aosv1.ListChangesResponse], error) {
	changes, err := t.s.Tasks.Changes(ctx, req.Msg.TaskId)
	if err != nil {
		return nil, connectError(err)
	}
	for _, c := range changes {
		b, err := t.s.Tasks.Base(ctx, req.Msg.TaskId, c.Path)
		if err != nil {
			return nil, connectError(err)
		}
		c.Pending = t.pending(ctx, b)
	}
	return connect.NewResponse(&aosv1.ListChangesResponse{Changes: changes}), nil
}

// pending reports whether the file on disk differs from what it is compared
// with. With nothing to compare with — a folder, a file too big to have been
// kept — it is pending until the user keeps it.
func (t taskService) pending(ctx context.Context, b task.Base) bool {
	if !b.Known {
		return !b.Change.Reviewed
	}
	content, exists, err := t.readNow(ctx, b.Change.Path, int64(len(b.Content)))
	if err != nil {
		return true
	}
	return exists != b.Exists || (exists && !bytes.Equal(content, b.Content))
}

// readNow reads a file as the Realm's user. A file of another size than want
// is not read: it differs.
func (t taskService) readNow(ctx context.Context, path string, want int64) (content []byte, exists bool, err error) {
	if t.s.UserFiles == nil {
		return nil, false, errors.New("files are unavailable")
	}
	var info files.Info
	err = t.s.UserFiles.Run(ctx, files.OpStat, files.PathArgs{Path: path}, &info, nil)
	switch {
	case errors.Is(err, files.ErrNotExist):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	case info.Dir || info.Symlink:
		return nil, true, nil
	case want >= 0 && info.Size != want:
		return nil, true, nil
	case info.Size > tool.MaxBeforeKeep:
		return nil, true, fmt.Errorf("%s is too big to keep", path)
	}
	var buf bytes.Buffer
	for {
		var raw files.Raw
		if err := t.s.UserFiles.Run(ctx, files.OpRead, files.ReadArgs{Path: path, Offset: int64(buf.Len()), Limit: files.MaxRead}, &raw, nil); err != nil {
			return nil, true, err
		}
		buf.Write(raw.Content)
		if raw.EOF || len(raw.Content) == 0 || int64(buf.Len()) > tool.MaxBeforeKeep {
			return buf.Bytes(), true, nil
		}
	}
}

// GetBefore returns what a changed file is compared with (M8.11).
func (t taskService) GetBefore(ctx context.Context, req *connect.Request[aosv1.GetBeforeRequest]) (*connect.Response[aosv1.GetBeforeResponse], error) {
	change, content, err := t.s.Tasks.Before(ctx, req.Msg.TaskId, req.Msg.Path)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.GetBeforeResponse{Change: change, Content: content}), nil
}

// ReviewChange records what the user kept (M8.12): the given lines, or with
// keep_all the file as it is now.
func (t taskService) ReviewChange(ctx context.Context, req *connect.Request[aosv1.ReviewChangeRequest]) (*connect.Response[aosv1.ReviewChangeResponse], error) {
	content, exists := req.Msg.Base, !req.Msg.Gone
	if req.Msg.KeepAll {
		var err error
		if content, exists, err = t.readNow(ctx, req.Msg.Path, -1); err != nil {
			return nil, connectError(err)
		}
	}
	if err := t.s.Tasks.SetBase(ctx, req.Msg.TaskId, req.Msg.Path, content, exists); err != nil {
		return nil, connectError(err)
	}
	return changeNow(ctx, t, req.Msg.TaskId, req.Msg.Path, func(c *aosv1.TaskFileChange) *connect.Response[aosv1.ReviewChangeResponse] {
		return connect.NewResponse(&aosv1.ReviewChangeResponse{Change: c})
	})
}

// DiscardChange puts a file back as it is compared with, as FileService does
// — as the Realm's user, audited, asking first for a Sensitive System File —
// or moves it to the Trash when that is "does not exist" (M8.12).
func (t taskService) DiscardChange(ctx context.Context, req *connect.Request[aosv1.DiscardChangeRequest]) (*connect.Response[aosv1.DiscardChangeResponse], error) {
	b, err := t.s.Tasks.Base(ctx, req.Msg.TaskId, req.Msg.Path)
	if err != nil {
		return nil, connectError(err)
	}
	p := b.Change.Path
	if !b.Known {
		what := "a folder"
		if !b.Change.Folder {
			what = "too big to have been kept"
		}
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("%s was %s, so it can't be put back from here; restore it from the Trash", p, what))
	}
	if t.s.UserFiles == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("files are unavailable"))
	}
	f := fileService(t)
	// Only whether it exists: asked for 0 bytes, a file is not read.
	_, exists, err := t.readNow(ctx, p, 0)
	if err != nil {
		return nil, connectError(err)
	}
	switch {
	case b.Exists:
		why, sensitive := f.sensitive(p, false)
		if sensitive && !req.Msg.ConfirmSensitive {
			return nil, refuseSensitive(p, why)
		}
		if sensitive && exists {
			if err := f.keep(p); err != nil {
				return nil, connectError(err)
			}
		}
		err = t.s.UserFiles.Run(ctx, files.OpWrite, files.WriteArgs{Path: p, Content: string(b.Content), Overwrite: true}, nil, nil)
		f.audit(ctx, auditTool(sensitive, "discard_change"), p, err)
	case exists:
		why, sensitive := f.sensitive(p, true)
		if sensitive && !req.Msg.ConfirmSensitive {
			return nil, refuseSensitive(p, why)
		}
		err = t.s.UserFiles.Run(ctx, files.OpDelete, files.PathArgs{Path: p}, &files.TrashItem{}, nil)
		f.audit(ctx, auditTool(sensitive, "discard_change"), p, err)
	}
	if err != nil {
		return nil, connectError(err)
	}
	return changeNow(ctx, t, req.Msg.TaskId, p, func(c *aosv1.TaskFileChange) *connect.Response[aosv1.DiscardChangeResponse] {
		return connect.NewResponse(&aosv1.DiscardChangeResponse{Change: c})
	})
}

// changeNow answers with the change as it stands now.
func changeNow[T any](ctx context.Context, t taskService, taskID, path string, answer func(*aosv1.TaskFileChange) *connect.Response[T]) (*connect.Response[T], error) {
	b, err := t.s.Tasks.Base(ctx, taskID, path)
	if err != nil {
		return nil, connectError(err)
	}
	b.Change.Pending = t.pending(ctx, b)
	return answer(b.Change), nil
}
