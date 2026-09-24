package api

import (
	"context"
	"errors"
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
	go c.watch(watchCtx, watcher, ws, events)
	return connect.NewResponse(&aosv1.OpenWorkspaceResponse{Id: ws.ID, Root: ws.Root, RootMode: ws.RootMode}), nil
}

// watch relays a Workspace's watch to its subscription until ctx ends. A
// subscription that falls behind holds the watch back rather than losing
// changes: the kernel's queue fills, and the watch then asks for a rescan.
func (c codeService) watch(ctx context.Context, w files.Watcher, ws code.Workspace, events chan<- *aosv1.Event) {
	send := func(ev files.WatchEvent) error {
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
