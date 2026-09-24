package api

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/code"
	"github.com/Aman123at/agentic-os/internal/files"
)

// ---------------------------------------------------------------- Code

type codeService struct{ s *Server }

// Workspaces are the Code windows open now (M8.2); the Realm switch asks it
// before restarting aosd (M8.13).
func (s *Server) Workspaces() *code.Registry { return &s.workspaces }

func (s *Server) rootMode() bool {
	return s.Info != nil && s.Info().RootMode
}

// subscribed makes an event subscription able to own Workspaces (M8.2): it
// returns the subscription's id, the channel its Workspaces' changes arrive
// on, and the function that closes them all when the subscription ends.
func (s *Server) subscribed() (id string, changes <-chan *aosv1.Event, end func()) {
	id = code.NewID("sub_")
	ch := make(chan *aosv1.Event, 16)
	s.subscriptions.Store(id, ch)
	detach := s.workspaces.Attach(id)
	return id, ch, func() {
		// Detaching stops every watch first, so none is left sending to a
		// channel nobody reads.
		detach()
		s.subscriptions.Delete(id)
	}
}

// OpenWorkspace checks the folder as the Realm's user, records the Workspace
// for the tab's subscription, and watches it until it closes.
func (c codeService) OpenWorkspace(ctx context.Context, req *connect.Request[aosv1.OpenWorkspaceRequest]) (*connect.Response[aosv1.OpenWorkspaceResponse], error) {
	v, ok := c.s.subscriptions.Load(req.Msg.SubscriptionId)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, code.ErrNoOwner)
	}
	events := v.(chan *aosv1.Event)
	root, err := fileService(c).abs(req.Msg.Path)
	if err != nil {
		return nil, err
	}
	// Stat and List run as the Realm's user, like Finder's, so a folder that
	// user cannot read is refused before anything is recorded.
	var info files.Info
	if err := c.s.UserFiles.Run(ctx, files.OpStat, files.PathArgs{Path: root}, &info, nil); err != nil {
		return nil, connectError(err)
	}
	if !info.Dir {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(root+" is not a folder"))
	}
	if err := c.s.UserFiles.Run(ctx, files.OpList, files.PathArgs{Path: root}, nil, nil); err != nil {
		return nil, connectError(err)
	}
	watcher, ok := c.s.UserFiles.(files.Watcher)
	if !ok {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("this aosd cannot watch folders"))
	}

	// The watch outlives this request: it ends when the Workspace closes.
	watchCtx, stop := context.WithCancel(context.Background())
	ws, err := c.s.workspaces.Open(req.Msg.SubscriptionId, root, c.s.rootMode(), stop)
	switch {
	case errors.Is(err, code.ErrNoOwner):
		stop()
		return nil, connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, code.ErrTooMany):
		stop()
		return nil, connect.NewError(connect.CodeResourceExhausted, err)
	case err != nil:
		stop()
		return nil, connectError(err)
	}
	_, list, _ := c.s.workspaces.Get(ws.ID)
	go c.watch(watchCtx, watcher, ws, list, events)
	return connect.NewResponse(&aosv1.OpenWorkspaceResponse{Id: ws.ID, Root: ws.Root, RootMode: ws.RootMode}), nil
}

// watch relays a Workspace's watch to its subscription until ctx ends. A
// subscription that falls behind holds the watch back rather than losing
// changes: the kernel's queue fills, and the watch then asks for a rescan.
func (c codeService) watch(ctx context.Context, w files.Watcher, ws code.Workspace, list *code.FileList, events chan<- *aosv1.Event) {
	send := func(ev files.WatchEvent) error {
		// Quick Open's list follows the same changes (M8.7).
		if list != nil && len(ev.Changes) > 0 {
			list.Apply(ev.Changes)
		}
		select {
		case events <- workspaceEvent(ws.ID, ev):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	err := w.Watch(ctx, files.WatchArgs{Root: ws.Root, Interval: c.s.WatchInterval}, send)
	if err != nil && ctx.Err() == nil {
		// The folder checked out a moment ago; a watch that cannot start now
		// is reported, and the Workspace stays open regardless.
		_ = send(files.WatchEvent{Status: &files.WatchStatus{Mode: files.WatchStopped, Reason: err.Error()}})
	}
}

func workspaceEvent(id string, ev files.WatchEvent) *aosv1.Event {
	wc := &aosv1.WorkspaceChanged{WorkspaceId: id}
	for _, ch := range ev.Changes {
		kind := aosv1.FileChange_KIND_UNSPECIFIED
		switch ch.Kind {
		case files.Created:
			kind = aosv1.FileChange_KIND_CREATED
		case files.Changed:
			kind = aosv1.FileChange_KIND_CHANGED
		case files.Deleted:
			kind = aosv1.FileChange_KIND_DELETED
		case files.Rescan:
			kind = aosv1.FileChange_KIND_RESCAN
		}
		wc.Changes = append(wc.Changes, &aosv1.FileChange{Kind: kind, Path: ch.Path, Dir: ch.Dir})
	}
	if st := ev.Status; st != nil {
		state := aosv1.WorkspaceWatch_STATE_UNSPECIFIED
		switch st.Mode {
		case files.WatchLive:
			state = aosv1.WorkspaceWatch_STATE_LIVE
		case files.WatchPolling:
			state = aosv1.WorkspaceWatch_STATE_POLLING
		case files.WatchStopped:
			state = aosv1.WorkspaceWatch_STATE_STOPPED
		}
		wc.Watch = &aosv1.WorkspaceWatch{State: state, Reason: st.Reason}
	}
	return &aosv1.Event{Time: timestamppb.New(time.Now()), Kind: &aosv1.Event_Workspace{Workspace: wc}}
}

func (c codeService) CloseWorkspace(_ context.Context, req *connect.Request[aosv1.CloseWorkspaceRequest]) (*connect.Response[aosv1.CloseWorkspaceResponse], error) {
	if err := c.s.workspaces.Close(req.Msg.WorkspaceId); err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&aosv1.CloseWorkspaceResponse{}), nil
}

func (c codeService) ReportDirty(_ context.Context, req *connect.Request[aosv1.ReportDirtyRequest]) (*connect.Response[aosv1.ReportDirtyResponse], error) {
	err := c.s.workspaces.SetUnsaved(req.Msg.WorkspaceId, req.Msg.Paths)
	switch {
	case errors.Is(err, code.ErrNoWorkspace):
		return nil, connect.NewError(connect.CodeNotFound, err)
	case err != nil:
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&aosv1.ReportDirtyResponse{}), nil
}

// ---------------------------------------------------------------- Quick Open, Find in Files (M8.7)

// workspace is an open Workspace, or NotFound.
func (c codeService) workspace(id string) (code.Workspace, *code.FileList, error) {
	ws, list, err := c.s.workspaces.Get(id)
	if err != nil {
		return code.Workspace{}, nil, connect.NewError(connect.CodeNotFound, err)
	}
	return ws, list, nil
}

func (c codeService) FindFiles(ctx context.Context, req *connect.Request[aosv1.FindFilesRequest]) (*connect.Response[aosv1.FindFilesResponse], error) {
	ws, list, err := c.workspace(req.Msg.WorkspaceId)
	if err != nil {
		return nil, err
	}
	paths, truncated, err := list.Paths(ctx, func(ctx context.Context) (files.Tree, error) {
		var t files.Tree
		err := c.s.UserFiles.Run(ctx, files.OpListTree, files.TreeArgs{Root: ws.Root}, &t, nil)
		return t, err
	})
	if err != nil {
		return nil, connectError(err)
	}
	limit := int(req.Msg.Limit)
	switch {
	case limit <= 0:
		limit = 50
	case limit > 500:
		limit = 500
	}
	resp := &aosv1.FindFilesResponse{Total: uint32(len(paths)), Truncated: truncated}
	for _, f := range code.Rank(req.Msg.Query, paths, limit) {
		pos := make([]uint32, len(f.Positions))
		for i, p := range f.Positions {
			pos[i] = uint32(p)
		}
		resp.Files = append(resp.Files, &aosv1.FoundFile{Path: filepath.Join(ws.Root, filepath.FromSlash(f.Rel)), Positions: pos})
	}
	return connect.NewResponse(resp), nil
}

// searchFlush is how long found files wait to be sent together, so a search
// with many small results is a few messages and the first ones come at once.
const searchFlush = 100 * time.Millisecond

func (c codeService) SearchText(ctx context.Context, req *connect.Request[aosv1.SearchTextRequest], stream *connect.ServerStream[aosv1.SearchTextResponse]) error {
	ws, _, err := c.workspace(req.Msg.WorkspaceId)
	if err != nil {
		return err
	}
	searcher, ok := c.s.UserFiles.(files.TextSearcher)
	if !ok {
		return connect.NewError(connect.CodeUnimplemented, errors.New("this aosd cannot search text"))
	}
	var batch []*aosv1.TextFileMatches
	last := time.Now()
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := stream.Send(&aosv1.SearchTextResponse{Files: batch})
		batch, last = nil, time.Now()
		return err
	}
	res, err := searcher.SearchText(ctx, files.TextSearchArgs{
		Root: ws.Root, Query: req.Msg.Query, Regex: req.Msg.Regex, Case: req.Msg.CaseSensitive,
		Include: req.Msg.Include, Exclude: req.Msg.Exclude,
	}, func(f files.TextFile) error {
		m := &aosv1.TextFileMatches{Path: f.Path}
		for _, x := range f.Matches {
			tm := &aosv1.TextMatch{Line: uint32(x.Line), Text: x.Text}
			for _, r := range x.Ranges {
				tm.Ranges = append(tm.Ranges, &aosv1.TextRange{Start: uint32(r[0]), End: uint32(r[1])})
			}
			m.Matches = append(m.Matches, tm)
		}
		batch = append(batch, m)
		if len(batch) >= 32 || time.Since(last) >= searchFlush {
			return flush()
		}
		return nil
	})
	if errors.Is(err, files.ErrBadQuery) {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err != nil {
		return connectError(err)
	}
	if err := flush(); err != nil {
		return err
	}
	return stream.Send(&aosv1.SearchTextResponse{Summary: &aosv1.SearchTextSummary{
		Files: uint32(res.Files), Matches: uint32(res.Matches), Truncated: res.Truncated, Engine: res.Engine,
	}})
}
