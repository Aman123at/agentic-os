package api

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
	"github.com/amantiwari/agentic-os/gen/go/aos/v1/aosv1connect"
	"github.com/amantiwari/agentic-os/internal/audit"
	"github.com/amantiwari/agentic-os/internal/events"
	"github.com/amantiwari/agentic-os/internal/files"
	"github.com/amantiwari/agentic-os/internal/task"
)

// Protected stores the paths the user locked.
type Protected interface {
	List(ctx context.Context) ([]*aosv1.ListProtectedResponse_Entry, error)
	Protect(ctx context.Context, path string) error
	Unprotect(ctx context.Context, path string) error
	IsProtected(path string) bool
}

// Terminal is a running Session a viewer attaches to.
type Terminal interface {
	Watch() (recent []byte, output <-chan []byte, stop func())
	// Write types into the Session.
	Write(b []byte) error
	Resize(cols, rows uint16) error
}

// Sessions manages User Sessions and lists Agent Sessions.
type Sessions interface {
	Create(ctx context.Context, cols, rows uint16) (*aosv1.SessionInfo, error)
	List() []*aosv1.SessionInfo
	Close(id string) error
	Attach(id string) (Terminal, error)
}

// Server implements the API services.
type Server struct {
	Auth      *Auth
	Tasks     *task.Manager
	Bus       *events.Bus
	Audit     *audit.Log
	Home      string
	UserFiles files.Runner // file operations as the aos user, unconfined
	FileOps   files.Ops
	Protected Protected
	Sessions  Sessions
	Info      func() *aosv1.InfoResponse
	// Assets is the Desktop (ui Mode); nil serves a short note.
	Assets fs.FS
}

// Handler returns every route; wrap it with Auth.TCP or Auth.Socket.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	opts := []connect.HandlerOption{}
	mux.Handle(aosv1connect.NewAuthServiceHandler(authService{s}, opts...))
	mux.Handle(aosv1connect.NewTaskServiceHandler(taskService{s}, opts...))
	mux.Handle(aosv1connect.NewApprovalServiceHandler(approvalService{s}, opts...))
	mux.Handle(aosv1connect.NewEventServiceHandler(eventService{s}, opts...))
	mux.Handle(aosv1connect.NewFileServiceHandler(fileService{s}, opts...))
	mux.Handle(aosv1connect.NewTrashServiceHandler(trashService{s}, opts...))
	mux.Handle(aosv1connect.NewSessionServiceHandler(sessionService{s}, opts...))
	mux.Handle(aosv1connect.NewSystemServiceHandler(systemService{s}, opts...))
	mux.HandleFunc("GET /ws/session/{id}", s.sessionSocket)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	page := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("Agentic OS is running in cli Mode: docker compose exec aos aos\n"))
	}))
	if s.Assets != nil {
		page = http.FileServerFS(s.Assets)
	}
	// "/" without a method: a method pattern would conflict with the service prefixes.
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		page.ServeHTTP(w, r)
	}))
	return mux
}

// connectError maps domain errors to Connect codes.
func connectError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, task.ErrNotFound), errors.Is(err, files.ErrNotExist):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, files.ErrExists):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, files.ErrPermission):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, err)
	}
	var ce *connect.Error
	if errors.As(err, &ce) {
		return err
	}
	return connect.NewError(connect.CodeUnknown, err)
}

// ---------------------------------------------------------------- Auth

type authService struct{ s *Server }

func (a authService) ExchangeLoginCode(ctx context.Context, req *connect.Request[aosv1.ExchangeLoginCodeRequest]) (*connect.Response[aosv1.ExchangeLoginCodeResponse], error) {
	cookie, err := a.s.Auth.Exchange(req.Msg.Code)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	resp := connect.NewResponse(&aosv1.ExchangeLoginCodeResponse{})
	resp.Header().Add("Set-Cookie", cookie.String())
	return resp, nil
}

func (a authService) CreateLoginCode(ctx context.Context, _ *connect.Request[aosv1.CreateLoginCodeRequest]) (*connect.Response[aosv1.CreateLoginCodeResponse], error) {
	if ActorFrom(ctx) != "user:cli" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only `aos desktop-url` inside the Machine can create login codes"))
	}
	code, expires := a.s.Auth.NewLoginCode()
	return connect.NewResponse(&aosv1.CreateLoginCodeResponse{Code: code, ExpiresAt: timestamppb.New(expires)}), nil
}

// ---------------------------------------------------------------- Tasks

type taskService struct{ s *Server }

func (t taskService) CreateTask(ctx context.Context, req *connect.Request[aosv1.CreateTaskRequest]) (*connect.Response[aosv1.CreateTaskResponse], error) {
	created, err := t.s.Tasks.Create(ctx, req.Msg.Prompt, req.Msg.Autonomy, req.Msg.Interactive)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	_ = t.s.Audit.Record(ctx, audit.Entry{TaskID: created.Id, Tool: "create_task", Result: created.Title, Actor: ActorFrom(ctx), Decision: "allow", DecidedBy: ActorFrom(ctx)})
	return connect.NewResponse(&aosv1.CreateTaskResponse{Task: created}), nil
}

func (t taskService) ListTasks(ctx context.Context, req *connect.Request[aosv1.ListTasksRequest]) (*connect.Response[aosv1.ListTasksResponse], error) {
	tasks, err := t.s.Tasks.List(ctx, int(req.Msg.Limit))
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.ListTasksResponse{Tasks: tasks}), nil
}

func (t taskService) GetTask(ctx context.Context, req *connect.Request[aosv1.GetTaskRequest]) (*connect.Response[aosv1.GetTaskResponse], error) {
	task, steps, approvals, err := t.s.Tasks.Get(ctx, req.Msg.Id)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.GetTaskResponse{Task: task, Steps: steps, Approvals: approvals}), nil
}

func (t taskService) SendFollowUp(ctx context.Context, req *connect.Request[aosv1.SendFollowUpRequest]) (*connect.Response[aosv1.SendFollowUpResponse], error) {
	continued, err := t.s.Tasks.FollowUp(ctx, req.Msg.Id, req.Msg.Text, req.Msg.Interactive)
	if err != nil {
		return nil, stateError(err)
	}
	_ = t.s.Audit.Record(ctx, audit.Entry{TaskID: continued.Id, Tool: "follow_up", Result: req.Msg.Text, Actor: ActorFrom(ctx), Decision: "allow", DecidedBy: ActorFrom(ctx)})
	return connect.NewResponse(&aosv1.SendFollowUpResponse{Task: continued}), nil
}

// stateError maps an error from changing a Task: unknown, or not in a state that allows it.
func stateError(err error) error {
	if errors.Is(err, task.ErrNotFound) {
		return connectError(err)
	}
	return connect.NewError(connect.CodeFailedPrecondition, err)
}

func (t taskService) AnswerQuestion(ctx context.Context, req *connect.Request[aosv1.AnswerQuestionRequest]) (*connect.Response[aosv1.AnswerQuestionResponse], error) {
	if err := t.s.Tasks.Answer(ctx, req.Msg.Id, req.Msg.Text, ActorFrom(ctx)); err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.AnswerQuestionResponse{}), nil
}

func (t taskService) CancelTask(ctx context.Context, req *connect.Request[aosv1.CancelTaskRequest]) (*connect.Response[aosv1.CancelTaskResponse], error) {
	if err := t.s.Tasks.Cancel(ctx, req.Msg.Id); err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return nil, connectError(err)
		}
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	_ = t.s.Audit.Record(ctx, audit.Entry{TaskID: req.Msg.Id, Tool: "cancel_task", Actor: ActorFrom(ctx), Decision: "allow", DecidedBy: ActorFrom(ctx)})
	return connect.NewResponse(&aosv1.CancelTaskResponse{}), nil
}

func (t taskService) ResumeTask(ctx context.Context, req *connect.Request[aosv1.ResumeTaskRequest]) (*connect.Response[aosv1.ResumeTaskResponse], error) {
	resumed, err := t.s.Tasks.Resume(ctx, req.Msg.Id, req.Msg.Interactive)
	if err != nil {
		return nil, stateError(err)
	}
	_ = t.s.Audit.Record(ctx, audit.Entry{TaskID: resumed.Id, Tool: "resume_task", Actor: ActorFrom(ctx), Decision: "allow", DecidedBy: ActorFrom(ctx)})
	return connect.NewResponse(&aosv1.ResumeTaskResponse{Task: resumed}), nil
}

func (t taskService) StopAll(ctx context.Context, _ *connect.Request[aosv1.StopAllRequest]) (*connect.Response[aosv1.StopAllResponse], error) {
	n, err := t.s.Tasks.StopAll(ctx)
	if err != nil {
		return nil, connectError(err)
	}
	_ = t.s.Audit.Record(ctx, audit.Entry{Tool: "stop_all", Actor: ActorFrom(ctx), Decision: "allow", DecidedBy: ActorFrom(ctx)})
	return connect.NewResponse(&aosv1.StopAllResponse{Cancelled: int32(n)}), nil
}

// ---------------------------------------------------------------- Approvals

type approvalService struct{ s *Server }

func (a approvalService) ListPending(ctx context.Context, req *connect.Request[aosv1.ListPendingRequest]) (*connect.Response[aosv1.ListPendingResponse], error) {
	pending, err := a.s.Tasks.Pending(ctx, req.Msg.TaskId)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.ListPendingResponse{Approvals: pending}), nil
}

func (a approvalService) Decide(ctx context.Context, req *connect.Request[aosv1.DecideRequest]) (*connect.Response[aosv1.DecideResponse], error) {
	approval, err := a.s.Tasks.Decide(ctx, req.Msg.ApprovalId, req.Msg.Decision, ActorFrom(ctx))
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.DecideResponse{Approval: approval}), nil
}

// ---------------------------------------------------------------- Events

type eventService struct{ s *Server }

func (e eventService) Subscribe(ctx context.Context, req *connect.Request[aosv1.SubscribeRequest], stream *connect.ServerStream[aosv1.SubscribeResponse]) error {
	ch := e.s.Bus.Subscribe(ctx, req.Msg.TaskId)
	// An empty first message tells the client it is subscribed: state it reads
	// after this cannot miss an event.
	if err := stream.Send(&aosv1.SubscribeResponse{}); err != nil {
		return err
	}
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				return connect.NewError(connect.CodeResourceExhausted, errors.New("the subscriber fell behind; subscribe again"))
			}
			if err := stream.Send(&aosv1.SubscribeResponse{Event: ev}); err != nil {
				return err
			}
		case <-ctx.Done():
			return nil
		}
	}
}

// ---------------------------------------------------------------- Files

type fileService struct{ s *Server }

// abs resolves a user-supplied path: ~ is home, relative paths start at home.
func (f fileService) abs(p string) (string, error) {
	switch {
	case p == "":
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("path is empty"))
	case p == "~":
		p = f.s.Home
	case strings.HasPrefix(p, "~/"):
		p = filepath.Join(f.s.Home, p[2:])
	case !path.IsAbs(p):
		p = filepath.Join(f.s.Home, p)
	}
	return filepath.Clean(p), nil
}

func (f fileService) info(i files.Info) *aosv1.FileInfo {
	return &aosv1.FileInfo{Path: i.Path, Name: i.Name, Dir: i.Dir, Symlink: i.Symlink, LinkTarget: i.LinkTarget, Size: i.Size,
		Mode: uint32(i.Mode), ModifiedAt: timestamppb.New(i.ModTime), Protected: f.s.Protected.IsProtected(i.Path)}
}

func (f fileService) List(ctx context.Context, req *connect.Request[aosv1.ListRequest]) (*connect.Response[aosv1.ListResponse], error) {
	p, err := f.abs(req.Msg.Path)
	if err != nil {
		return nil, err
	}
	var entries []files.Info
	if err := f.s.UserFiles.Run(ctx, files.OpList, files.PathArgs{Path: p}, &entries, nil); err != nil {
		return nil, connectError(err)
	}
	resp := &aosv1.ListResponse{}
	for _, e := range entries {
		resp.Entries = append(resp.Entries, f.info(e))
	}
	return connect.NewResponse(resp), nil
}

func (f fileService) Stat(ctx context.Context, req *connect.Request[aosv1.StatRequest]) (*connect.Response[aosv1.StatResponse], error) {
	p, err := f.abs(req.Msg.Path)
	if err != nil {
		return nil, err
	}
	var info files.Info
	if err := f.s.UserFiles.Run(ctx, files.OpStat, files.PathArgs{Path: p}, &info, nil); err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.StatResponse{Info: f.info(info)}), nil
}

func (f fileService) Read(ctx context.Context, req *connect.Request[aosv1.ReadRequest]) (*connect.Response[aosv1.ReadResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("raw reads arrive with the Desktop's /files/raw route (M3)"))
}

func (f fileService) Write(ctx context.Context, req *connect.Request[aosv1.WriteRequest]) (*connect.Response[aosv1.WriteResponse], error) {
	p, err := f.abs(req.Msg.Path)
	if err != nil {
		return nil, err
	}
	err = f.s.UserFiles.Run(ctx, files.OpWrite, files.WriteArgs{Path: p, Content: string(req.Msg.Content), Overwrite: req.Msg.Overwrite}, nil, nil)
	f.audit(ctx, "write_file", p, err)
	return connect.NewResponse(&aosv1.WriteResponse{}), connectError(err)
}

func (f fileService) transfer(ctx context.Context, op, src, dst string, overwrite bool) error {
	s, err := f.abs(src)
	if err != nil {
		return err
	}
	d, err := f.abs(dst)
	if err != nil {
		return err
	}
	err = f.s.UserFiles.Run(ctx, op, files.TransferArgs{Source: s, Destination: d, Overwrite: overwrite}, nil, nil)
	f.audit(ctx, op, s+" -> "+d, err)
	return connectError(err)
}

func (f fileService) Move(ctx context.Context, req *connect.Request[aosv1.MoveRequest]) (*connect.Response[aosv1.MoveResponse], error) {
	return connect.NewResponse(&aosv1.MoveResponse{}), f.transfer(ctx, files.OpMove, req.Msg.Source, req.Msg.Destination, req.Msg.Overwrite)
}

func (f fileService) Copy(ctx context.Context, req *connect.Request[aosv1.CopyRequest]) (*connect.Response[aosv1.CopyResponse], error) {
	return connect.NewResponse(&aosv1.CopyResponse{}), f.transfer(ctx, files.OpCopy, req.Msg.Source, req.Msg.Destination, req.Msg.Overwrite)
}

func (f fileService) Delete(ctx context.Context, req *connect.Request[aosv1.DeleteRequest]) (*connect.Response[aosv1.DeleteResponse], error) {
	p, err := f.abs(req.Msg.Path)
	if err != nil {
		return nil, err
	}
	var item files.TrashItem
	err = f.s.UserFiles.Run(ctx, files.OpDelete, files.PathArgs{Path: p}, &item, nil)
	f.audit(ctx, "delete", p, err)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.DeleteResponse{Item: trashItem(item)}), nil
}

func (f fileService) Protect(ctx context.Context, req *connect.Request[aosv1.ProtectRequest]) (*connect.Response[aosv1.ProtectResponse], error) {
	p, err := f.abs(req.Msg.Path)
	if err != nil {
		return nil, err
	}
	err = f.s.Protected.Protect(ctx, p)
	f.audit(ctx, "protect", p, err)
	return connect.NewResponse(&aosv1.ProtectResponse{}), connectError(err)
}

func (f fileService) Unprotect(ctx context.Context, req *connect.Request[aosv1.UnprotectRequest]) (*connect.Response[aosv1.UnprotectResponse], error) {
	p, err := f.abs(req.Msg.Path)
	if err != nil {
		return nil, err
	}
	err = f.s.Protected.Unprotect(ctx, p)
	f.audit(ctx, "unprotect", p, err)
	return connect.NewResponse(&aosv1.UnprotectResponse{}), connectError(err)
}

func (f fileService) ListProtected(ctx context.Context, _ *connect.Request[aosv1.ListProtectedRequest]) (*connect.Response[aosv1.ListProtectedResponse], error) {
	entries, err := f.s.Protected.List(ctx)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.ListProtectedResponse{Entries: entries}), nil
}

func (f fileService) audit(ctx context.Context, op, target string, err error) {
	result := "ok"
	if err != nil {
		result = err.Error()
	}
	_ = f.s.Audit.Record(ctx, audit.Entry{Tool: op, Arguments: `{"path":` + quote(target) + `}`, Decision: "allow", DecidedBy: ActorFrom(ctx), Result: result, Actor: ActorFrom(ctx)})
}

func quote(s string) string {
	b := strings.Builder{}
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			if r < 0x20 {
				b.WriteString(" ")
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ---------------------------------------------------------------- Trash

type trashService struct{ s *Server }

func trashItem(i files.TrashItem) *aosv1.TrashItem {
	return &aosv1.TrashItem{Id: i.ID, OriginalPath: i.OriginalPath, DeletedAt: timestamppb.New(i.DeletedAt), Size: i.Size, Dir: i.Dir}
}

func (t trashService) ListTrash(ctx context.Context, _ *connect.Request[aosv1.ListTrashRequest]) (*connect.Response[aosv1.ListTrashResponse], error) {
	var items []files.TrashItem
	if err := t.s.UserFiles.Run(ctx, files.OpListTrash, struct{}{}, &items, nil); err != nil {
		return nil, connectError(err)
	}
	resp := &aosv1.ListTrashResponse{}
	for _, i := range items {
		resp.Items = append(resp.Items, trashItem(i))
	}
	return connect.NewResponse(resp), nil
}

func (t trashService) Restore(ctx context.Context, req *connect.Request[aosv1.RestoreRequest]) (*connect.Response[aosv1.RestoreResponse], error) {
	var restored string
	err := t.s.UserFiles.Run(ctx, files.OpRestore, files.RestoreArgs{ID: req.Msg.Id}, &restored, nil)
	_ = t.s.Audit.Record(ctx, audit.Entry{Tool: "restore", Arguments: `{"id":` + quote(req.Msg.Id) + `}`, Result: restored, Decision: "allow", DecidedBy: ActorFrom(ctx), Actor: ActorFrom(ctx)})
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.RestoreResponse{Path: restored}), nil
}

func (t trashService) Empty(ctx context.Context, _ *connect.Request[aosv1.EmptyRequest]) (*connect.Response[aosv1.EmptyResponse], error) {
	var n int
	err := t.s.UserFiles.Run(ctx, files.OpEmptyTrash, struct{}{}, &n, nil)
	_ = t.s.Audit.Record(ctx, audit.Entry{Tool: "empty_trash", Decision: "allow", DecidedBy: ActorFrom(ctx), Actor: ActorFrom(ctx)})
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.EmptyResponse{Removed: int32(n)}), nil
}

// ---------------------------------------------------------------- Sessions

type sessionService struct{ s *Server }

func (ss sessionService) CreateSession(ctx context.Context, req *connect.Request[aosv1.CreateSessionRequest]) (*connect.Response[aosv1.CreateSessionResponse], error) {
	cols, rows := uint16(req.Msg.Cols), uint16(req.Msg.Rows)
	if cols == 0 || rows == 0 {
		cols, rows = 120, 40
	}
	info, err := ss.s.Sessions.Create(ctx, cols, rows)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.CreateSessionResponse{Session: info}), nil
}

func (ss sessionService) ListSessions(context.Context, *connect.Request[aosv1.ListSessionsRequest]) (*connect.Response[aosv1.ListSessionsResponse], error) {
	return connect.NewResponse(&aosv1.ListSessionsResponse{Sessions: ss.s.Sessions.List()}), nil
}

func (ss sessionService) CloseSession(ctx context.Context, req *connect.Request[aosv1.CloseSessionRequest]) (*connect.Response[aosv1.CloseSessionResponse], error) {
	return connect.NewResponse(&aosv1.CloseSessionResponse{}), connectError(ss.s.Sessions.Close(req.Msg.Id))
}

// ---------------------------------------------------------------- System

type systemService struct{ s *Server }

func (sys systemService) Info(context.Context, *connect.Request[aosv1.InfoRequest]) (*connect.Response[aosv1.InfoResponse], error) {
	return connect.NewResponse(sys.s.Info()), nil
}

func (sys systemService) Audit(ctx context.Context, req *connect.Request[aosv1.AuditRequest]) (*connect.Response[aosv1.AuditResponse], error) {
	entries, err := sys.s.Audit.List(ctx, req.Msg.TaskId, int(req.Msg.Limit), req.Msg.BeforeId)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.AuditResponse{Entries: entries}), nil
}
