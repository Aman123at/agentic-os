package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/gen/go/aos/v1/aosv1connect"
	"github.com/Aman123at/agentic-os/internal/audit"
	"github.com/Aman123at/agentic-os/internal/auth"
	"github.com/Aman123at/agentic-os/internal/browser"
	"github.com/Aman123at/agentic-os/internal/catalogue"
	"github.com/Aman123at/agentic-os/internal/desktop"
	"github.com/Aman123at/agentic-os/internal/events"
	"github.com/Aman123at/agentic-os/internal/files"
	"github.com/Aman123at/agentic-os/internal/notify"
	"github.com/Aman123at/agentic-os/internal/profile"
	"github.com/Aman123at/agentic-os/internal/service"
	"github.com/Aman123at/agentic-os/internal/settings"
	"github.com/Aman123at/agentic-os/internal/software"
	"github.com/Aman123at/agentic-os/internal/sysinfo"
	"github.com/Aman123at/agentic-os/internal/task"
	"github.com/Aman123at/agentic-os/internal/usage"
)

// Protected stores the paths the user locked.
type Protected interface {
	List(ctx context.Context) ([]*aosv1.ListProtectedResponse_Entry, error)
	Protect(ctx context.Context, path string) error
	Unprotect(ctx context.Context, path string) error
	// IsProtected says why a path is protected: "user", "default", "inherited",
	// or "" when it is not.
	IsProtected(path string) string
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

// APIKey replaces the OpenAI API key (PLAN.md §7.7). Only hints (sk-…abcd)
// come back; the key itself never leaves the implementation.
type APIKey interface {
	// Set saves a key from System Settings and returns its hint.
	Set(key string) (hint string, err error)
	// Clear goes back to the key from .env and returns its hint, or "".
	Clear() (hint string, err error)
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
	Memories  *profile.Memories
	Desktop   *desktop.State
	// Settings are the settings that can change while aosd runs.
	Settings *settings.Store
	// Catalogue is the model catalogue the Settings dropdowns offer (M6.16).
	Catalogue *catalogue.File
	// APIKey replaces the OpenAI API key.
	APIKey APIKey
	// WatchInterval is how often FileService.Watch lists a folder again; 0 means 2 s.
	WatchInterval time.Duration
	// Usage is model usage per day.
	Usage *usage.Tracker
	// Sampler samples the Machine for Activity Monitor.
	Sampler *sysinfo.Sampler
	// Notifications are kept until dismissed.
	Notifications *notify.Center
	// Software and Supervisor serve the Install Ledger and the Services.
	Software   *software.Manager
	Supervisor *service.Supervisor
	Info       func() *aosv1.InfoResponse
	// Restart restarts aosd out of band after the RPC replies (M7.3); nil means
	// restart is not available (e.g. a foreground run with no supervisor).
	Restart func() error
	// ClearRootHistory deletes the Root Realm's database and its read_output blobs
	// (M7.12), so the next Root start begins empty; nil means it is not available.
	// The RPC only calls it while in Root Mode, after its Task gate, and restarts.
	ClearRootHistory func() error
	// Browser is the Browser app's page; nil when it isn't included.
	Browser *browser.Manager
	// Assets is the Desktop (ui Mode); nil serves a short note.
	Assets fs.FS
	// Now is the clock for the Root Mode switch's lockout (M7.7); nil means time.Now.
	Now func() time.Time

	rootGateOnce sync.Once
	rootGate     *rootModeGate
	// realmSwitch overrides Tasks as the Root Mode switch's queue gate; tests set
	// it, production uses Tasks (M7.7).
	realmSwitch realmSwitcher
}

// Handler returns every route; wrap it with Auth.TCP or Auth.Socket.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// No response compression: the API serves this computer (PLAN.md §7.6),
	// where gzip saves nothing, and each response's compressor holds about 1 MB.
	// A heap profile after the Desktop suite had Connect's gzip pool at two
	// thirds of aosd's live heap, pushing idle memory over its §16 target.
	opts := []connect.HandlerOption{connect.WithCompression("gzip", nil, nil)}
	mux.Handle(aosv1connect.NewAuthServiceHandler(authService{s}, opts...))
	mux.Handle(aosv1connect.NewTaskServiceHandler(taskService{s}, opts...))
	mux.Handle(aosv1connect.NewApprovalServiceHandler(approvalService{s}, opts...))
	mux.Handle(aosv1connect.NewEventServiceHandler(eventService{s}, opts...))
	mux.Handle(aosv1connect.NewFileServiceHandler(fileService{s}, opts...))
	mux.Handle(aosv1connect.NewTrashServiceHandler(trashService{s}, opts...))
	mux.Handle(aosv1connect.NewSessionServiceHandler(sessionService{s}, opts...))
	mux.Handle(aosv1connect.NewSystemServiceHandler(systemService{s}, opts...))
	mux.Handle(aosv1connect.NewSettingsServiceHandler(settingsService{s}, opts...))
	mux.Handle(aosv1connect.NewSoftwareServiceHandler(softwareService{s}, opts...))
	mux.Handle(aosv1connect.NewSupervisorServiceHandler(supervisorService{s}, opts...))
	mux.HandleFunc("GET /ws/session/{id}", s.sessionSocket)
	mux.HandleFunc("GET /ws/browser", s.browserSocket)
	mux.HandleFunc("GET /files/raw", s.rawFile)
	mux.HandleFunc("POST /upload", s.upload)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	// The Realm is public so the login card can name it before anyone signs in
	// (M7.10). Only the one boolean is exposed; everything else about the Machine
	// stays behind authentication.
	mux.HandleFunc("GET /realm", func(w http.ResponseWriter, _ *http.Request) {
		root := false
		if s.Info != nil {
			root = s.Info().RootMode
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, "{\"rootMode\":%t}\n", root)
	})
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
	case errors.Is(err, task.ErrNotFound), errors.Is(err, files.ErrNotExist), errors.Is(err, profile.ErrNoMemory),
		errors.Is(err, software.ErrNoCheckpoint), errors.Is(err, service.ErrNoService), errors.Is(err, notify.ErrNoNotification):
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

// model is the authentication model, or an error when this Server has none.
func (a authService) model() (*auth.Model, error) {
	if a.s.Auth == nil || a.s.Auth.Model == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("authentication is not available"))
	}
	return a.s.Auth.Model, nil
}

func (a authService) SignIn(ctx context.Context, req *connect.Request[aosv1.SignInRequest]) (*connect.Response[aosv1.SignInResponse], error) {
	m, err := a.model()
	if err != nil {
		return nil, err
	}
	access, refresh, mustChange, err := m.SignIn(ctx, req.Msg.Username, req.Msg.Password)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	return connect.NewResponse(&aosv1.SignInResponse{AccessToken: access, RefreshToken: refresh, MustChangePassword: mustChange}), nil
}

func (a authService) Refresh(ctx context.Context, req *connect.Request[aosv1.RefreshRequest]) (*connect.Response[aosv1.RefreshResponse], error) {
	m, err := a.model()
	if err != nil {
		return nil, err
	}
	access, refresh, err := m.Refresh(ctx, req.Msg.RefreshToken)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	// A reload during the forced first change refreshes before it shows a screen;
	// carry the flag so it returns to the change, not the shell.
	mustChange, err := m.MustChange(ctx)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.RefreshResponse{AccessToken: access, RefreshToken: refresh, MustChangePassword: mustChange}), nil
}

// ChangePassword replaces the signed-in user's password. The middleware has
// already verified the caller's access token, so a forced first change works
// even though the account is still on its generated password.
func (a authService) ChangePassword(ctx context.Context, req *connect.Request[aosv1.ChangePasswordRequest]) (*connect.Response[aosv1.ChangePasswordResponse], error) {
	m, err := a.model()
	if err != nil {
		return nil, err
	}
	access, refresh, err := m.ChangePassword(ctx, req.Msg.NewPassword)
	if err != nil {
		if errors.Is(err, auth.ErrWeakPassword) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.ChangePasswordResponse{AccessToken: access, RefreshToken: refresh}), nil
}

// SignOut ends the caller's refresh family. It takes the refresh token in the
// body rather than the family from context because the access token names only
// the user, and one user may have several sessions to end independently.
func (a authService) SignOut(ctx context.Context, req *connect.Request[aosv1.SignOutRequest]) (*connect.Response[aosv1.SignOutResponse], error) {
	m, err := a.model()
	if err != nil {
		return nil, err
	}
	if err := m.Revoke(ctx, req.Msg.RefreshToken); err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.SignOutResponse{}), nil
}

// CreateInitialUser creates the one account. It is the account-creation moment
// `aos mode ui` runs, so it is served only over the local control socket: on the
// public bind a stranger reaching a Machine that has no account yet must not be
// able to seize it. It refuses once an account exists.
func (a authService) CreateInitialUser(ctx context.Context, req *connect.Request[aosv1.CreateInitialUserRequest]) (*connect.Response[aosv1.CreateInitialUserResponse], error) {
	if ActorFrom(ctx) != "user:cli" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("the account is created locally, by `aos mode ui`"))
	}
	m, err := a.model()
	if err != nil {
		return nil, err
	}
	switch err := m.CreateInitialUser(ctx, req.Msg.Username, req.Msg.Password); {
	case errors.Is(err, auth.ErrUserExists):
		return nil, connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, auth.ErrWeakPassword):
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	case err != nil:
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.CreateInitialUserResponse{}), nil
}

// CreateTicket mints a single-use ticket for a browser load. The middleware has
// already checked the caller's access token, so no further guard is needed.
func (a authService) CreateTicket(_ context.Context, _ *connect.Request[aosv1.CreateTicketRequest]) (*connect.Response[aosv1.CreateTicketResponse], error) {
	m, err := a.model()
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&aosv1.CreateTicketResponse{Ticket: m.Ticket()}), nil
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

func (t taskService) DeleteTask(ctx context.Context, req *connect.Request[aosv1.DeleteTaskRequest]) (*connect.Response[aosv1.DeleteTaskResponse], error) {
	if err := t.s.Tasks.Delete(ctx, req.Msg.Id); err != nil {
		return nil, stateError(err)
	}
	_ = t.s.Audit.Record(ctx, audit.Entry{TaskID: req.Msg.Id, Tool: "delete_task", Actor: ActorFrom(ctx), Decision: "allow", DecidedBy: ActorFrom(ctx)})
	return connect.NewResponse(&aosv1.DeleteTaskResponse{}), nil
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
	src := f.s.Protected.IsProtected(i.Path)
	return &aosv1.FileInfo{Path: i.Path, Name: i.Name, Dir: i.Dir, Symlink: i.Symlink, LinkTarget: i.LinkTarget, Size: i.Size,
		Mode: uint32(i.Mode), ModifiedAt: timestamppb.New(i.ModTime), Protected: src != "", ProtectSource: src}
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
	p, err := f.abs(req.Msg.Path)
	if err != nil {
		return nil, err
	}
	var raw files.Raw
	if err := f.s.UserFiles.Run(ctx, files.OpRead, files.ReadArgs{Path: p, Offset: req.Msg.Offset, Limit: req.Msg.Limit}, &raw, nil); err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.ReadResponse{Content: raw.Content, Eof: raw.EOF}), nil
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
	_ = f.s.Audit.Record(ctx, audit.Entry{Tool: op, Arguments: `{"path":` + quote(target) + `}`, Decision: decisionFor(err), DecidedBy: ActorFrom(ctx), Result: result, Actor: ActorFrom(ctx)})
}

// decisionFor is what the Audit Log's Decision column says about a call the user
// was allowed to make. "allow" means it ran; a call that failed is recorded as
// "error", so a reader is not told a call that did nothing was allowed
// (PLAN.md M4.8 item 8.16). "deny" stays reserved for a policy refusal.
func decisionFor(err error) string {
	if err != nil {
		return "error"
	}
	return "allow"
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
	restoreResult := restored
	if err != nil {
		restoreResult = err.Error()
	}
	_ = t.s.Audit.Record(ctx, audit.Entry{Tool: "restore", Arguments: `{"id":` + quote(req.Msg.Id) + `}`, Result: restoreResult, Decision: decisionFor(err), DecidedBy: ActorFrom(ctx), Actor: ActorFrom(ctx)})
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.RestoreResponse{Path: restored}), nil
}

func (t trashService) Empty(ctx context.Context, _ *connect.Request[aosv1.EmptyRequest]) (*connect.Response[aosv1.EmptyResponse], error) {
	var n int
	err := t.s.UserFiles.Run(ctx, files.OpEmptyTrash, struct{}{}, &n, nil)
	emptyResult := fmt.Sprintf("%d removed", n)
	if err != nil {
		emptyResult = err.Error()
	}
	_ = t.s.Audit.Record(ctx, audit.Entry{Tool: "empty_trash", Result: emptyResult, Decision: decisionFor(err), DecidedBy: ActorFrom(ctx), Actor: ActorFrom(ctx)})
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

// ---------------------------------------------------------------- Settings

type settingsService struct{ s *Server }

func (st settingsService) ListMemory(ctx context.Context, _ *connect.Request[aosv1.ListMemoryRequest]) (*connect.Response[aosv1.ListMemoryResponse], error) {
	memories, err := st.s.Memories.List(ctx)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.ListMemoryResponse{Memories: memories}), nil
}

func (st settingsService) AddMemory(ctx context.Context, req *connect.Request[aosv1.AddMemoryRequest]) (*connect.Response[aosv1.AddMemoryResponse], error) {
	m, err := st.s.Memories.Add(ctx, "", req.Msg.Text)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	st.audit(ctx, "memory_add", m.Id, m.Text)
	return connect.NewResponse(&aosv1.AddMemoryResponse{Memory: m}), nil
}

func (st settingsService) AcceptMemory(ctx context.Context, req *connect.Request[aosv1.AcceptMemoryRequest]) (*connect.Response[aosv1.AcceptMemoryResponse], error) {
	m, err := st.s.Memories.Accept(ctx, req.Msg.Id)
	if err != nil {
		return nil, connectError(err)
	}
	st.audit(ctx, "memory_accept", m.Id, m.Text)
	return connect.NewResponse(&aosv1.AcceptMemoryResponse{Memory: m}), nil
}

func (st settingsService) ForgetMemory(ctx context.Context, req *connect.Request[aosv1.ForgetMemoryRequest]) (*connect.Response[aosv1.ForgetMemoryResponse], error) {
	if err := st.s.Memories.Forget(ctx, req.Msg.Id); err != nil {
		return nil, connectError(err)
	}
	st.audit(ctx, "memory_forget", req.Msg.Id, "")
	return connect.NewResponse(&aosv1.ForgetMemoryResponse{}), nil
}

func (st settingsService) GetDesktopState(ctx context.Context, _ *connect.Request[aosv1.GetDesktopStateRequest]) (*connect.Response[aosv1.GetDesktopStateResponse], error) {
	if st.s.Desktop == nil {
		return connect.NewResponse(&aosv1.GetDesktopStateResponse{}), nil
	}
	state, err := st.s.Desktop.Get(ctx)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.GetDesktopStateResponse{State: state}), nil
}

func (st settingsService) SaveDesktopState(ctx context.Context, req *connect.Request[aosv1.SaveDesktopStateRequest]) (*connect.Response[aosv1.SaveDesktopStateResponse], error) {
	if st.s.Desktop == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("desktop state is not available"))
	}
	if err := st.s.Desktop.Save(ctx, req.Msg.State); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// Tell the other tabs: they adopt the preferences in it (theme, wallpaper,
	// Liquid Glass, shortcuts) and keep their own window layout (PLAN.md §4.3).
	st.s.Bus.Publish(&aosv1.Event{Kind: &aosv1.Event_DesktopState{
		DesktopState: &aosv1.DesktopStateChanged{State: req.Msg.State, Origin: req.Msg.Origin},
	}})
	return connect.NewResponse(&aosv1.SaveDesktopStateResponse{}), nil
}

func setting(s settings.Setting) *aosv1.Setting {
	return &aosv1.Setting{Key: s.Key, Value: s.Value, Source: string(s.Source), Env: s.Env, Fallback: s.Fallback, PendingRestart: s.PendingRestart}
}

func (st settingsService) Get(context.Context, *connect.Request[aosv1.GetSettingsRequest]) (*connect.Response[aosv1.GetSettingsResponse], error) {
	if st.s.Settings == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("settings are not available"))
	}
	resp := &aosv1.GetSettingsResponse{}
	for _, s := range st.s.Settings.List() {
		resp.Settings = append(resp.Settings, setting(s))
	}
	if cat, err := st.s.Catalogue.Catalogue(); err == nil && cat != nil {
		for _, m := range cat.Models {
			resp.Models = append(resp.Models, &aosv1.ModelChoice{
				Id: m.ID, Label: m.Label, Efforts: m.Efforts, DefaultEffort: m.DefaultEffort, IsDefault: m.Default,
			})
		}
	}
	return connect.NewResponse(resp), nil
}

// Update saves one setting. Every change, refused ones too, goes to the Audit
// Log; Agents never get here (the socket refuses them, PLAN.md §7.5).
func (st settingsService) Update(ctx context.Context, req *connect.Request[aosv1.UpdateSettingRequest]) (*connect.Response[aosv1.UpdateSettingResponse], error) {
	if st.s.Settings == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("settings are not available"))
	}
	s, err := st.s.Settings.Set(req.Msg.Key, req.Msg.Value)
	result := s.Value
	if err != nil {
		result = err.Error()
	}
	_ = st.s.Audit.Record(ctx, audit.Entry{Tool: "settings_update", Arguments: `{"key":` + quote(req.Msg.Key) + `,"value":` + quote(req.Msg.Value) + `}`,
		Result: result, Decision: decisionFor(err), DecidedBy: ActorFrom(ctx), Actor: ActorFrom(ctx)})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&aosv1.UpdateSettingResponse{Setting: setting(s)}), nil
}

// SetApiKey replaces the API key. The Audit Log records the change with the
// new key's hint, or why it was refused, and never the key.
func (st settingsService) SetApiKey(ctx context.Context, req *connect.Request[aosv1.SetApiKeyRequest]) (*connect.Response[aosv1.SetApiKeyResponse], error) {
	if st.s.APIKey == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("the API key cannot be changed here"))
	}
	hint, err := st.s.APIKey.Set(req.Msg.Key)
	st.keyAudit(ctx, "set_api_key", hint, err)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&aosv1.SetApiKeyResponse{Hint: hint}), nil
}

// ClearApiKey goes back to the key from .env.
func (st settingsService) ClearApiKey(ctx context.Context, _ *connect.Request[aosv1.ClearApiKeyRequest]) (*connect.Response[aosv1.ClearApiKeyResponse], error) {
	if st.s.APIKey == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("the API key cannot be changed here"))
	}
	hint, err := st.s.APIKey.Clear()
	st.keyAudit(ctx, "clear_api_key", hint, err)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.ClearApiKeyResponse{Hint: hint}), nil
}

func (st settingsService) keyAudit(ctx context.Context, tool, hint string, err error) {
	result := hint
	if err != nil {
		result = err.Error()
	}
	_ = st.s.Audit.Record(ctx, audit.Entry{Tool: tool, Result: result, Decision: decisionFor(err), DecidedBy: ActorFrom(ctx), Actor: ActorFrom(ctx)})
}

func (st settingsService) audit(ctx context.Context, tool, id, result string) {
	_ = st.s.Audit.Record(ctx, audit.Entry{Tool: tool, Arguments: `{"id":` + quote(id) + `}`, Result: result, Decision: "allow", DecidedBy: ActorFrom(ctx), Actor: ActorFrom(ctx)})
}

// ---------------------------------------------------------------- System

type systemService struct{ s *Server }

func (sys systemService) Info(context.Context, *connect.Request[aosv1.InfoRequest]) (*connect.Response[aosv1.InfoResponse], error) {
	return connect.NewResponse(sys.s.Info()), nil
}

func (sys systemService) Usage(ctx context.Context, req *connect.Request[aosv1.UsageRequest]) (*connect.Response[aosv1.UsageResponse], error) {
	if sys.s.Usage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("usage is not available"))
	}
	n := int(req.Msg.Days)
	switch {
	case n == 0:
		n = 30
	case n < 0 || n > 366:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("usage covers 1 to 366 days"))
	}
	days, err := sys.s.Usage.Days(ctx, n)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.UsageResponse{Days: days}), nil
}

func (sys systemService) Metrics(context.Context, *connect.Request[aosv1.MetricsRequest]) (*connect.Response[aosv1.MetricsResponse], error) {
	if sys.s.Sampler == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("metrics are not available"))
	}
	return connect.NewResponse(sys.s.Sampler.Metrics()), nil
}

func (sys systemService) Processes(context.Context, *connect.Request[aosv1.ProcessesRequest]) (*connect.Response[aosv1.ProcessesResponse], error) {
	if sys.s.Sampler == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("processes are not available"))
	}
	return connect.NewResponse(sys.s.Sampler.Processes()), nil
}

func (sys systemService) ListNotifications(ctx context.Context, _ *connect.Request[aosv1.ListNotificationsRequest]) (*connect.Response[aosv1.ListNotificationsResponse], error) {
	if sys.s.Notifications == nil {
		return connect.NewResponse(&aosv1.ListNotificationsResponse{}), nil
	}
	list, err := sys.s.Notifications.List(ctx)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.ListNotificationsResponse{Notifications: list}), nil
}

func (sys systemService) DismissNotification(ctx context.Context, req *connect.Request[aosv1.DismissNotificationRequest]) (*connect.Response[aosv1.DismissNotificationResponse], error) {
	if sys.s.Notifications == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("notifications are not available"))
	}
	var err error
	if req.Msg.All {
		err = sys.s.Notifications.DismissAll(ctx)
	} else {
		err = sys.s.Notifications.Dismiss(ctx, req.Msg.Id)
	}
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.DismissNotificationResponse{}), nil
}

func (sys systemService) Restart(ctx context.Context, _ *connect.Request[aosv1.RestartRequest]) (*connect.Response[aosv1.RestartResponse], error) {
	if sys.s.Restart == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("restart is not available"))
	}
	// Trigger the restart, then audit the outcome and reply. The restart is out
	// of band (systemd's SIGTERM, or a Compose drain), so the response flushes
	// before aosd goes down; the guard on the control socket (§7.5) has already
	// refused any Agent-confined caller, so this only ever runs for the user.
	err := sys.s.Restart()
	result := "restarting"
	if err != nil {
		result = err.Error()
	}
	_ = sys.s.Audit.Record(ctx, audit.Entry{Tool: "restart", Result: result, Decision: "allow", DecidedBy: ActorFrom(ctx), Actor: ActorFrom(ctx)})
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.RestartResponse{}), nil
}

func (sys systemService) Audit(ctx context.Context, req *connect.Request[aosv1.AuditRequest]) (*connect.Response[aosv1.AuditResponse], error) {
	entries, err := sys.s.Audit.List(ctx, req.Msg.TaskId, int(req.Msg.Limit), req.Msg.BeforeId)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.AuditResponse{Entries: entries}), nil
}
