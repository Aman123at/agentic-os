// Package daemon assembles aosd: storage, the Task manager, sandboxed Tools,
// the API and Service forwarding.
package daemon

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
	"github.com/amantiwari/agentic-os/internal/agent"
	"github.com/amantiwari/agentic-os/internal/api"
	"github.com/amantiwari/agentic-os/internal/audit"
	"github.com/amantiwari/agentic-os/internal/config"
	"github.com/amantiwari/agentic-os/internal/events"
	"github.com/amantiwari/agentic-os/internal/files"
	"github.com/amantiwari/agentic-os/internal/llm"
	"github.com/amantiwari/agentic-os/internal/llm/fake"
	"github.com/amantiwari/agentic-os/internal/llm/openai"
	"github.com/amantiwari/agentic-os/internal/policy"
	"github.com/amantiwari/agentic-os/internal/proxy"
	"github.com/amantiwari/agentic-os/internal/sandbox"
	"github.com/amantiwari/agentic-os/internal/session"
	"github.com/amantiwari/agentic-os/internal/store"
	"github.com/amantiwari/agentic-os/internal/task"
	"github.com/amantiwari/agentic-os/internal/tool"
)

// Paths inside the Machine.
const (
	Port         = 7700
	StateDir     = "/var/lib/aos"
	RunDir       = "/run/aos"
	SocketPath   = "/run/aos/aosd.sock"
	SessionsDir  = "/run/aos/sessions"
	SecretKey    = "/run/secrets/openai_api_key"
	AgentBinDir  = "/usr/local/lib/aos/agent-bin"
	Version      = "0.1.0-m1"
	keyFile      = StateDir + "/keys/openai"
	tokenFile    = StateDir + "/token"
	outputsDir   = StateDir + "/outputs"
	databaseFile = StateDir + "/aos.db"
)

// Daemon is a running aosd.
type Daemon struct {
	cfg      config.Config
	layout   sandbox.Layout
	uid, gid uint32
	abi      int
	exe      string

	db       *store.DB
	bus      *events.Bus
	audit    *audit.Log
	tasks    *task.Manager
	registry *session.Registry
	locks    *locks
	outputs  *tool.Outputs
	auth     *api.Auth

	gitMu sync.Mutex
	git   map[string]map[string]bool // Task id → repo root → dirty when first seen
}

// Run starts aosd and serves until ctx ends.
func Run(ctx context.Context, cfg config.Config, assets fs.FS) error {
	d := &Daemon{cfg: cfg, layout: sandbox.DefaultLayout(), abi: sandbox.ABI(), git: map[string]map[string]bool{}}
	if err := d.init(); err != nil {
		return err
	}
	defer d.db.Close()

	if cfg.RequireLandlock && d.abi < 1 {
		return errors.New("AOS_REQUIRE_LANDLOCK=true, but this Host's kernel has no Landlock")
	}
	if d.abi < 1 {
		log.Print("WARNING: Landlock is unavailable on this Host: Agents are guarded by policy checks only, and auto Autonomy acts as confirm-risky")
	}

	provider, model, err := d.provider()
	if err != nil {
		return err
	}
	osName := osRelease()
	instructions := agent.Instructions(agent.Machine{OS: osName, Arch: runtime.GOARCH, Mode: cfg.Mode, Landlock: d.abi >= 1})
	registry := tool.NewRegistry(append(append(append(tool.SessionTools(), tool.FilesTools()...), tool.InternetTools()...), tool.CoordinationTools()...)...)
	d.tasks, err = task.New(task.Config{
		DB: d.db, Bus: d.bus, Audit: d.audit, Provider: provider, Tools: registry,
		Model: model, ReasoningEffort: cfg.ReasoningEffort, Instructions: instructions,
		Autonomy: cfg.Autonomy, MaxTasks: cfg.MaxTasks, MaxRetries: cfg.MaxRetries, Landlock: d.abi >= 1, Home: d.layout.Home,
		NewEnv: d.newEnv, Protection: d.protection,
	})
	if err != nil {
		return err
	}
	defer d.tasks.Close()

	userOps := files.Ops{Home: d.layout.Home, Shared: d.layout.Shared, UID: int(d.uid)}
	userFiles := files.AsUser{Ops: userOps, UID: d.uid, GID: d.gid, Exe: d.exe, Env: d.workerEnv()}
	srv := &api.Server{
		Auth: d.auth, Tasks: d.tasks, Bus: d.bus, Audit: d.audit, Home: d.layout.Home,
		UserFiles: userFiles, FileOps: userOps, Protected: d.locks,
		Sessions: &userSessions{d: d}, Info: func() *aosv1.InfoResponse { return d.info(model) }, Assets: assets,
	}
	handler := srv.Handler()

	tcp := &http.Server{Addr: fmt.Sprintf(":%d", Port), Handler: proxy.New(d.auth.TCP(handler), Port), ReadHeaderTimeout: 10 * time.Second}
	_ = os.Remove(SocketPath)
	sock, err := net.Listen("unix", SocketPath)
	if err != nil {
		return err
	}
	if err := os.Chmod(SocketPath, 0o666); err != nil {
		return err
	}
	unixSrv := &http.Server{Handler: d.auth.Socket(handler, d.refused), ConnContext: api.SocketConnContext, ReadHeaderTimeout: 10 * time.Second}

	errc := make(chan error, 2)
	go func() { errc <- unixSrv.Serve(sock) }()
	go func() { errc <- tcp.ListenAndServe() }()
	go d.expireTrash(ctx, userFiles)

	log.Printf("%s Mode, model %s, Landlock ABI %d; listening on :%d (on the Host: http://localhost:%s)", cfg.Mode, model, d.abi, Port, cfg.HostPort)
	if cfg.Mode == "ui" {
		code, _ := d.auth.NewLoginCode()
		log.Printf("Open the Desktop: http://localhost:%s/#code=%s (valid 5 minutes; later: docker compose exec aos aos desktop-url)", cfg.HostPort, code)
	}

	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = tcp.Shutdown(shutdown)
	_ = unixSrv.Shutdown(shutdown)
	return nil
}

func (d *Daemon) init() error {
	u, err := user.Lookup("aos")
	if err != nil {
		return err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	d.uid, d.gid = uint32(uid), uint32(gid)
	if d.exe, err = os.Executable(); err != nil {
		return err
	}
	notes, err := sandbox.PrepareHome(d.layout, uid, gid)
	for _, n := range notes {
		log.Print(n)
	}
	if err != nil {
		return fmt.Errorf("preparing the home folder: %w", err)
	}
	for dir, mode := range map[string]fs.FileMode{StateDir: 0o700, StateDir + "/keys": 0o700, outputsDir: 0o700, RunDir: 0o755, SessionsDir: 0o755} {
		if err := os.MkdirAll(dir, mode); err != nil {
			return err
		}
		if err := os.Chmod(dir, mode); err != nil {
			return err
		}
	}
	// Session directories never outlive aosd.
	if entries, err := os.ReadDir(SessionsDir); err == nil {
		for _, e := range entries {
			_ = os.RemoveAll(filepath.Join(SessionsDir, e.Name()))
		}
	}
	if err := copyKey(); err != nil {
		log.Printf("API key: %v", err)
	}
	token, err := accessToken(d.cfg.AccessToken)
	if err != nil {
		return err
	}
	d.auth = &api.Auth{Token: token}
	if d.db, err = store.Open(databaseFile); err != nil {
		return err
	}
	d.bus = events.New()
	d.audit = &audit.Log{DB: d.db}
	d.registry = session.NewRegistry()
	d.outputs = &tool.Outputs{Dir: outputsDir}
	d.locks = &locks{db: d.db, home: d.layout.Home}
	return d.locks.load()
}

// copyKey copies the Compose secret to a root-only file (PLAN.md §7.7).
func copyKey() error {
	key, err := os.ReadFile(SecretKey)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	key = []byte(strings.TrimSpace(string(key)))
	if len(key) == 0 {
		return nil
	}
	tmp := keyFile + ".tmp"
	if err := os.WriteFile(tmp, key, 0o400); err != nil {
		return err
	}
	return os.Rename(tmp, keyFile)
}

func readKey() string {
	b, err := os.ReadFile(keyFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// accessToken returns AOS_ACCESS_TOKEN, or the stored token, generating it on first start.
func accessToken(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if b, err := os.ReadFile(tokenFile); err == nil && len(strings.TrimSpace(string(b))) >= 32 {
		return strings.TrimSpace(string(b)), nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	return token, os.WriteFile(tokenFile, []byte(token+"\n"), 0o600)
}

func (d *Daemon) provider() (llm.Provider, string, error) {
	model := d.cfg.Model
	if model == "" {
		model = openai.DefaultModel
	}
	if d.cfg.FakeModel != "" {
		log.Printf("replaying recorded model conversations from %s instead of calling OpenAI", d.cfg.FakeModel)
		return &fake.Library{Dir: d.cfg.FakeModel}, model, nil
	}
	return &openai.Provider{Key: readKey, BaseURL: d.cfg.BaseURL, MaxRetries: d.cfg.MaxRetries}, model, nil
}

// agentPolicy is the sandbox policy for Agents: the layout, other Sessions'
// directories hidden, and the paths the user locked.
func (d *Daemon) agentPolicy() sandbox.Policy {
	p := d.layout.Policy()
	p.Hidden = append(p.Hidden, SessionsDir)
	for _, l := range d.locks.paths() {
		p.Protected = append(p.Protected, resolvePath(l))
	}
	return p
}

func (d *Daemon) plan(p sandbox.Policy) (sandbox.Ruleset, error) {
	if d.abi < 1 {
		return sandbox.Ruleset{}, nil
	}
	return sandbox.Plan(p, sandbox.RootFS())
}

func (d *Daemon) workerEnv() []string {
	return []string{"HOME=" + d.layout.Home, "USER=aos", "LANG=C.UTF-8", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
}

// newEnv gives a Task its Agent Session and sandboxed Tools.
func (d *Daemon) newEnv(env *tool.Env) (func(), error) {
	agentSession := session.NewAgent(session.AgentConfig{
		TaskID: env.TaskID, Dir: filepath.Join(SessionsDir, env.TaskID), UID: d.uid, GID: d.gid, Home: d.layout.Home,
		Policy: d.agentPolicy, Confine: d.abi >= 1, PathPrefix: AgentBinDir + ":", Outputs: d.outputs, Registry: d.registry,
	})
	ops := files.Ops{Home: d.layout.Home, Shared: d.layout.Shared, UID: int(d.uid)}
	env.Sessions = agentSession
	env.Cwd = agentSession.Cwd
	env.FileOps = ops
	env.Outputs = d.outputs
	env.HTTP = &http.Client{Timeout: 2 * time.Minute}
	env.Stat = statPath
	env.Remember = func(ctx context.Context, text string) error { return d.remember(ctx, env.TaskID, text) }
	env.Files = func(widen []string) files.Runner {
		return files.Confined{Ops: ops, UID: d.uid, GID: d.gid, Exe: d.exe, Env: d.workerEnv(), Ruleset: func() (sandbox.Ruleset, error) {
			p := d.agentPolicy()
			if len(widen) > 0 {
				resolved := make([]string, len(widen))
				for i, w := range widen {
					resolved[i] = resolvePath(w)
				}
				p = p.Widen(resolved)
			}
			return d.plan(p)
		}}
	}
	return func() {
		agentSession.Close()
		d.gitMu.Lock()
		delete(d.git, env.TaskID)
		d.gitMu.Unlock()
	}, nil
}

// protection returns a Task's Protected Paths, with symlinks resolved and git
// working trees judged by whether they were dirty when the Task first saw them.
func (d *Daemon) protection(taskID string) *policy.Protection {
	p := policy.NewProtection(d.layout.Home, d.locks.paths())
	p.Resolve = resolvePath
	p.DirtyRepo = func(path string) (string, bool) {
		root := gitRoot(path)
		if root == "" {
			return "", false
		}
		d.gitMu.Lock()
		defer d.gitMu.Unlock()
		seen := d.git[taskID]
		if seen == nil {
			seen = map[string]bool{}
			d.git[taskID] = seen
		}
		dirty, ok := seen[root]
		if !ok {
			dirty = gitDirty(root, d.uid, d.gid)
			seen[root] = dirty
		}
		return root, dirty
	}
	return p
}

func (d *Daemon) remember(ctx context.Context, taskID, text string) error {
	id := "m_" + randomHex(8)
	err := d.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO memories (id, text, status, task_id, created_at) VALUES (?, ?, 'proposed', ?, ?)`,
			id, text, taskID, store.Millis(time.Now()))
		return err
	})
	if err != nil {
		return err
	}
	d.bus.Publish(&aosv1.Event{Kind: &aosv1.Event_Notification{Notification: &aosv1.Notification{
		Title: "Remember this?", Body: text, TaskId: taskID}}})
	return nil
}

// refused records a request the Unix socket turned away (PLAN.md §7.5).
func (d *Daemon) refused(r *http.Request, pid int, reason string) {
	_ = d.audit.Record(context.Background(), audit.Entry{Tool: "api", Arguments: `{"path":"` + strings.ReplaceAll(r.URL.Path, `"`, "") + `"}`,
		Decision: "deny", DecidedBy: "policy", Result: fmt.Sprintf("refused pid %d: %s", pid, reason), Actor: "unix-socket"})
}

func (d *Daemon) info(model string) *aosv1.InfoResponse {
	key := "missing"
	if fi, err := os.Stat(SecretKey); err == nil {
		key = "empty"
		if fi.Size() > 0 || readKey() != "" {
			key = "present"
		}
	} else if readKey() != "" {
		key = "present"
	}
	autonomy := map[policy.Autonomy]aosv1.Autonomy{policy.Auto: aosv1.Autonomy_AUTONOMY_AUTO, policy.ConfirmRisky: aosv1.Autonomy_AUTONOMY_CONFIRM_RISKY, policy.ConfirmAll: aosv1.Autonomy_AUTONOMY_CONFIRM_ALL}[d.cfg.Autonomy]
	return &aosv1.InfoResponse{Mode: d.cfg.Mode, Version: Version, LandlockAbi: int32(d.abi), ApiKey: key, Model: model, Autonomy: autonomy, MaxTasks: int32(d.cfg.MaxTasks)}
}

func (d *Daemon) expireTrash(ctx context.Context, runner files.Runner) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		var n int
		if err := runner.Run(ctx, files.OpExpireTrash, files.ExpireArgs{Retention: d.cfg.TrashRetention, MaxBytes: d.cfg.TrashMaxBytes}, &n, nil); err == nil && n > 0 {
			log.Printf("Trash: removed %d expired item(s)", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func statPath(path string) (exists, dir bool) {
	fi, err := os.Stat(path)
	return err == nil, err == nil && fi.IsDir()
}

// resolvePath resolves symlinks in path, or in its longest existing prefix.
func resolvePath(path string) string {
	rest := ""
	for p := filepath.Clean(path); ; {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Clean(path)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

func osRelease() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
