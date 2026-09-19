// Package daemon assembles aosd: storage, the Task manager, sandboxed Tools,
// the API and Service forwarding.
package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/agent"
	"github.com/Aman123at/agentic-os/internal/api"
	"github.com/Aman123at/agentic-os/internal/audit"
	"github.com/Aman123at/agentic-os/internal/auth"
	"github.com/Aman123at/agentic-os/internal/browser"
	"github.com/Aman123at/agentic-os/internal/catalogue"
	"github.com/Aman123at/agentic-os/internal/config"
	"github.com/Aman123at/agentic-os/internal/desktop"
	"github.com/Aman123at/agentic-os/internal/events"
	"github.com/Aman123at/agentic-os/internal/files"
	"github.com/Aman123at/agentic-os/internal/llm"
	"github.com/Aman123at/agentic-os/internal/llm/fake"
	"github.com/Aman123at/agentic-os/internal/llm/openai"
	"github.com/Aman123at/agentic-os/internal/notify"
	"github.com/Aman123at/agentic-os/internal/policy"
	"github.com/Aman123at/agentic-os/internal/profile"
	"github.com/Aman123at/agentic-os/internal/proxy"
	"github.com/Aman123at/agentic-os/internal/sandbox"
	"github.com/Aman123at/agentic-os/internal/service"
	"github.com/Aman123at/agentic-os/internal/session"
	"github.com/Aman123at/agentic-os/internal/settings"
	"github.com/Aman123at/agentic-os/internal/software"
	"github.com/Aman123at/agentic-os/internal/store"
	"github.com/Aman123at/agentic-os/internal/sysinfo"
	"github.com/Aman123at/agentic-os/internal/task"
	"github.com/Aman123at/agentic-os/internal/tool"
	"github.com/Aman123at/agentic-os/internal/usage"
)

// Paths inside the Machine.
const (
	Port        = 7700
	StateDir    = "/var/lib/aos"
	RunDir      = "/run/aos"
	SocketPath  = "/run/aos/aosd.sock"
	SessionsDir = "/run/aos/sessions"
	SecretKey   = "/run/secrets/openai_api_key"
	AgentBinDir = "/usr/local/lib/aos/agent-bin"
	// unitPath is where systemd keeps aosd's unit; Protected when the filesystem
	// widens so a widened Agent cannot rewrite aosd's own service (M6.8).
	unitPath = "/etc/systemd/system/aos.service"
	// Version is what the Desktop shows in Settings ▸ Status and About This
	// Machine. Bump it with the milestone; version_test.go keeps it honest.
	Version        = "0.1.0-m5"
	keyFile        = StateDir + "/keys/openai"
	sessionKeyFile = StateDir + "/keys/session"
	outputsDir     = StateDir + "/outputs"
	databaseFile   = StateDir + "/aos.db"
	pricesFile     = StateDir + "/prices.yaml"
	modelsFile     = StateDir + "/models.yaml"
	blobsDir       = StateDir + "/blobs"
	servicesDir    = StateDir + "/services"
	aptArchives    = "/var/cache/aos/apt/archives"
	aptLists       = "/var/cache/aos/apt/lists"
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
	usage    *usage.Tracker
	models   *catalogue.File
	memories *profile.Memories
	osName   string
	software *software.Manager
	services *service.Supervisor
	settings *settings.Store
	sampler  *sysinfo.Sampler
	notify   *notify.Center
	browser  *browser.Manager

	gitMu sync.Mutex
	git   map[string]map[string]bool // Task id → repo root → dirty when first seen

	// homes lists the other users' home folders to keep Protected when the
	// filesystem is widened to the host (M6.8); nil reads /home. A seam for tests.
	homes func() []string
	// replay re-applies the Install Ledger at boot (M6.9); nil uses
	// software.Replay. A seam so the Compose-vs-native gate is testable off a VPS.
	replay func(context.Context) error
}

// Run starts aosd and serves until ctx ends.
func Run(ctx context.Context, cfg config.Config, assets fs.FS) error {
	d := &Daemon{cfg: cfg, layout: sandbox.DefaultLayout(), abi: sandbox.ABI(), git: map[string]map[string]bool{}}
	d.sampler = &sysinfo.Sampler{Disks: []string{d.layout.Home, StateDir}}
	if err := d.init(); err != nil {
		return err
	}
	// The CPU columns are rates, so they need a reading to measure against
	// before the Desktop asks for the first one.
	d.sampler.Prime()
	defer d.db.Close()

	var err error
	model := d.cfg.Model
	if model == "" {
		model = openai.DefaultModel
	}
	// config.yml is the single source of truth (ADR-0010). It overlays the
	// startup-only keys onto d.cfg and holds the runtime settings, which win over
	// the environment; a malformed file or an unknown key refuses the start. Done
	// before the Landlock check and provider() so both see the file's values.
	d.settings, err = settings.Open(d.cfg.ConfigPath, settings.Values{Model: model, ReasoningEffort: d.cfg.ReasoningEffort,
		Autonomy: d.cfg.Autonomy, MaxTasks: d.cfg.MaxTasks, MaxRetries: d.cfg.MaxRetries, TaskCostLimit: d.cfg.TaskCostLimit,
		DailyCostLimit: d.cfg.DailyCostLimit, TrashRetentionDays: int(d.cfg.TrashRetention / (24 * time.Hour)),
		TrashMaxGB: int(d.cfg.TrashMaxBytes >> 30)}, &d.cfg)
	if err != nil {
		return fmt.Errorf("loading the configuration: %w", err)
	}
	// A reasoning effort the chosen model does not accept is an HTTP 400, so
	// settings validates the pair against the catalogue (M6.16).
	d.settings.Efforts = d.models.Efforts

	if d.cfg.RequireLandlock && d.abi < 1 {
		return errors.New("require_landlock is set, but this Host's kernel has no Landlock")
	}
	if d.abi < 1 {
		log.Print("WARNING: Landlock is unavailable on this Host: Agents are guarded by policy checks only, and auto Autonomy acts as confirm-risky")
	}

	// Mode is resolved from config.yml now that settings.Open has overlaid it
	// (M6.10). The Desktop is served only in ui Mode; nil assets in ui Mode mean
	// this binary carries no Desktop, so say so rather than serve one that 404s.
	if d.cfg.Mode == "ui" {
		if assets == nil {
			log.Print("WARNING: ui Mode, but the Desktop was not built into this binary. Build it (npm --prefix desktop run build) before go build, or run the release image")
		}
	} else {
		assets = nil
	}

	provider, model, err := d.provider()
	if err != nil {
		return err
	}
	if v := d.settings.Values(); (v.TaskCostLimit > 0 || v.DailyCostLimit > 0) && !d.pricesKnown(v.Model) {
		log.Printf("WARNING: %s has no model %s, so its cost is unknown and the Cost Limits cannot apply", pricesFile, v.Model)
	}
	d.osName = osRelease()
	browserOK, _ := d.browserStatus()
	instructions := agent.Instructions(agent.Machine{OS: d.osName, Arch: runtime.GOARCH, Mode: d.cfg.Mode, Landlock: d.abi >= 1, Browser: browserOK})
	ledger := &software.Ledger{DB: d.db}
	d.services = &service.Supervisor{DB: d.db, Ledger: ledger, Bus: d.bus, Launch: d.launchService, LogDir: servicesDir, Logf: log.Printf,
		Owners: d.socketOwners}
	if err := d.services.Load(ctx); err != nil {
		return fmt.Errorf("loading the Services: %w", err)
	}
	d.software = &software.Manager{Ledger: ledger, Etc: &software.Tree{Root: "/etc", Blobs: blobsDir, Skip: software.EtcSkip},
		Archives: aptArchives, Lists: aptLists, AsUser: d.asUser, Services: d.services, Bus: d.bus, Notify: d.post, Logf: log.Printf}
	tools := append(tool.SessionTools(), tool.FilesTools()...)
	tools = append(append(append(tools, tool.InternetTools()...), tool.SoftwareTools()...), tool.ServiceTools()...)
	tools = append(tools, tool.CoordinationTools()...)
	if d.cfg.Mode == "ui" {
		// In cli Mode there is no Desktop to show anything (PLAN.md §9).
		tools = append(tools, tool.DesktopTools()...)
	}
	if browserOK {
		// Agents use the Desktop's Browser only where it exists (PLAN.md M5.3).
		tools = append(tools, tool.BrowserTools()...)
	}
	registry := tool.NewRegistry(tools...)
	d.tasks, err = task.New(task.Config{
		DB: d.db, Bus: d.bus, Audit: d.audit, Provider: provider, Tools: registry,
		Model: model, ReasoningEffort: cfg.ReasoningEffort, Instructions: instructions,
		Autonomy: cfg.Autonomy, MaxTasks: cfg.MaxTasks, MaxRetries: cfg.MaxRetries, Landlock: d.abi >= 1, Home: d.layout.Home,
		Usage: d.usage, TaskCostLimit: cfg.TaskCostLimit, DailyCostLimit: cfg.DailyCostLimit, Context: d.agentContext,
		NewEnv: d.newEnv, Protection: d.protection, Settings: d.settings.Values,
	})
	if err != nil {
		return err
	}
	defer d.tasks.Close()
	d.settings.OnChange(func(settings.Values) { d.tasks.SettingsChanged() })
	defer d.services.Close()

	d.browser = d.newBrowser()
	if d.browser != nil {
		defer d.browser.Close()
	}

	userOps := files.Ops{Home: d.layout.Home, UID: int(d.uid)}
	userFiles := files.AsUser{Ops: userOps, UID: d.uid, GID: d.gid, Exe: d.exe, Env: d.workerEnv()}
	srv := &api.Server{
		Auth: d.auth, Tasks: d.tasks, Bus: d.bus, Audit: d.audit, Home: d.layout.Home,
		UserFiles: userFiles, FileOps: userOps, Protected: d.locks,
		Sessions: &userSessions{d: d}, Memories: d.memories, Software: d.software, Supervisor: d.services,
		Desktop: &desktop.State{DB: d.db}, Settings: d.settings, Catalogue: d.models, APIKey: keys, Usage: d.usage, Info: d.info, Assets: assets,
		Sampler:       d.sampler,
		Notifications: d.notify,
		Browser:       d.browser,
	}
	handler := srv.Handler()

	// The forwarder runs inside the authenticator (M6.4): auth.TCP gates every
	// request, including /port/<n>/ and <n>.localhost Service loads, before
	// proxy.New forwards them, so a public bind exposes nothing unauthenticated.
	tcp := &http.Server{Handler: d.auth.TCP(proxy.New(handler, Port)), ReadHeaderTimeout: 10 * time.Second}
	// Bind before signalling readiness so that, under Type=notify, `systemctl start
	// aos` (and install.sh above it) cannot return before the socket is accepting
	// (M6.2). cli Mode has no Desktop, so it binds only the control socket (M6.4).
	tcpLn, sock, err := bindListeners(d.cfg.Mode, fmt.Sprintf(":%d", Port), SocketPath)
	if err != nil {
		return err
	}
	unixSrv := &http.Server{Handler: d.auth.Socket(handler, d.refused), ConnContext: api.SocketConnContext, ReadHeaderTimeout: 10 * time.Second}

	errc := make(chan error, 2)
	go func() { errc <- unixSrv.Serve(sock) }()
	if tcpLn != nil {
		go func() { errc <- tcp.Serve(tcpLn) }()
	}
	sdNotify("READY=1")
	go d.expireTrash(ctx, userFiles)
	go d.services.Watch(ctx)
	// Replay first (Compose only), then the Services, which may need its software
	// (PLAN.md §11–12). On a native install Replay is off (M6.9).
	go func() {
		d.replayAtBoot(ctx)
		if ctx.Err() == nil {
			d.services.StartAll()
		}
	}()

	if tcpLn != nil {
		log.Printf("%s Mode, model %s, Landlock ABI %d; listening on :%d", d.cfg.Mode, model, d.abi, Port)
		log.Printf("Open the Desktop at http://<this-host>:%d/ and sign in", Port)
	} else {
		log.Printf("%s Mode, model %s, Landlock ABI %d; control socket only, no TCP port", d.cfg.Mode, model, d.abi)
	}

	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	// Drain in order: stop the public front door, let running Tasks settle, then
	// close the control socket last (M6.2). sdNotify tells systemd the stop is
	// under way so it holds off on SIGKILL for the full TimeoutStopSec.
	sdNotify("STOPPING=1")
	gracefulShutdown(
		func(sctx context.Context) {
			if tcpLn != nil {
				_ = tcp.Shutdown(sctx)
			}
		},
		func(context.Context) { d.tasks.Close() },
		func(sctx context.Context) { _ = unixSrv.Shutdown(sctx) },
	)
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
	if err := keys.copy(); err != nil {
		log.Printf("API key: %v", err)
	}
	if d.db, err = store.Open(databaseFile); err != nil {
		return err
	}
	key, err := sessionKey()
	if err != nil {
		return err
	}
	// The API is always authenticated (ADR-0007): over TCP the auth Model checks
	// access tokens and tickets; over the control socket the uid check accepts
	// only the user aosd runs as — root, under systemd (M6.2) — which its 0600
	// mode already enforces, the uid check being the belt to that braces.
	d.auth = &api.Auth{Model: &auth.Model{DB: d.db, Key: key}, SocketUID: os.Getuid(), SelfPort: Port}
	// The user edits prices.yaml; it is only written when missing.
	if f, err := os.OpenFile(pricesFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err == nil {
		_, _ = f.WriteString(usage.DefaultPrices)
		f.Close()
	}
	d.usage = &usage.Tracker{DB: d.db, Prices: &usage.File{Path: pricesFile}}
	// The user edits models.yaml too; seeded once, then re-read on change (M6.16).
	if f, err := os.OpenFile(modelsFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err == nil {
		_, _ = f.WriteString(catalogue.DefaultModels)
		f.Close()
	}
	d.models = &catalogue.File{Path: modelsFile}
	d.bus = events.New()
	d.notify = &notify.Center{DB: d.db, Bus: d.bus}
	d.memories = &profile.Memories{DB: d.db, Notify: d.post}
	d.audit = &audit.Log{DB: d.db}
	d.registry = session.NewRegistry()
	d.outputs = &tool.Outputs{Dir: outputsDir}
	d.locks = &locks{db: d.db, home: d.layout.Home}
	return d.locks.load()
}

// sessionKey loads the key that signs access tokens, generating it on first
// start. It lives in /var/lib/aos, never in config.yml (ADR-0007); deleting the
// file or replacing the key signs every Desktop out.
func sessionKey() ([]byte, error) {
	if b, err := os.ReadFile(sessionKeyFile); err == nil {
		if key, err := hex.DecodeString(strings.TrimSpace(string(b))); err == nil && len(key) >= 32 {
			return key, nil
		}
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, os.WriteFile(sessionKeyFile, []byte(hex.EncodeToString(key)+"\n"), 0o600)
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
	return &openai.Provider{Key: keys.read, BaseURL: d.cfg.BaseURL, MaxRetries: d.cfg.MaxRetries}, model, nil
}

// replayAtBoot re-applies the Install Ledger at startup on a Compose install,
// but not on a native one (ADR-0003, M6.9). A container discards everything
// outside its volumes across a restart, so Replay rebuilds it; a native VPS is a
// persistent server where nothing is discarded, so re-applying the Ledger would
// be needless and destructive (re-pinning packages, rewriting /etc the user may
// have changed). The Ledger is still recorded and Restore stays user-initiated.
func (d *Daemon) replayAtBoot(ctx context.Context) {
	if d.cfg.Native() {
		log.Printf("native install: Replay is off (ADR-0003); the Install Ledger is still recorded and Restore is on demand")
		return
	}
	replay := d.replay
	if replay == nil {
		replay = d.software.Replay
	}
	if err := replay(ctx); err != nil {
		log.Printf("Replay: %v", err)
	}
}

// agentPolicy is the sandbox policy for Agents: the layout, other Sessions'
// directories hidden, and the paths the user locked.
func (d *Daemon) agentPolicy() sandbox.Policy {
	p := d.layout.Policy()
	p.Hidden = append(p.Hidden, SessionsDir)
	if d.cfg.Native() {
		d.widenToHost(&p)
	}
	for _, l := range d.locks.paths() {
		p.Protected = append(p.Protected, resolvePath(l))
	}
	return p
}

// widenToHost opens the Writable set from home to the whole VPS minus an explicit
// Protected list (ADR-0004, M6.8), for a native install. Agents already read all
// of `/`, so only writing widens. AOS's own files, the boot and pseudo
// filesystems, root's home and other users' homes are excluded; the Browser keeps
// its own narrow ruleset (browserPolicy), so this never reaches it.
func (d *Daemon) widenToHost(p *sandbox.Policy) {
	p.Writable = []string{"/"}
	// The config file joins the key and secret stores as Hidden.
	p.Hidden = append(p.Hidden, "/etc/aos")
	// The boot loader and the pseudo filesystems, root's home, AOS's binaries and
	// its systemd unit. Excluding /proc and /sys as whole directories (never a path
	// beneath them) keeps carve from ever descending into a pseudo-filesystem — the
	// "no exclusion under /proc or /sys" rule that bounds the walk.
	p.Protected = append(p.Protected,
		"/boot", "/proc", "/sys", "/snap", "/root",
		d.exe, "/usr/local/bin/aos", "/usr/local/lib/aos", unitPath)
	p.Protected = append(p.Protected, d.otherHomes()...)
}

// otherHomes are the home folders under /home that are not AOS's own, kept
// Protected so a widened Agent cannot write another user's files.
func (d *Daemon) otherHomes() []string {
	if d.homes != nil {
		return d.homes()
	}
	entries, err := os.ReadDir("/home")
	if err != nil {
		return nil
	}
	self := filepath.Base(d.layout.Home)
	var out []string
	for _, e := range entries {
		if e.Name() != self {
			out = append(out, filepath.Join("/home", e.Name()))
		}
	}
	return out
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
		OnStart: func(pid int) { d.sampler.Started(pid, env.TaskID) },
		// npm's global prefix is in the home folder (PLAN.md §11).
		Env: []string{"NPM_CONFIG_PREFIX=" + d.layout.Home + "/.local"},
	})
	ops := files.Ops{Home: d.layout.Home, UID: int(d.uid)}
	env.Sessions = agentSession
	env.Cwd = agentSession.Cwd
	env.FileOps = ops
	env.Outputs = d.outputs
	env.HTTP = &http.Client{Timeout: 2 * time.Minute}
	env.Stat = statPath
	env.Remember = func(ctx context.Context, text string, direct bool) error {
		var err error
		if direct {
			_, err = d.memories.Add(ctx, env.TaskID, text)
		} else {
			_, err = d.memories.Propose(ctx, env.TaskID, text)
		}
		return err
	}
	env.Software = software.Tools{M: d.software}
	env.Services = service.Tools{S: d.services, Checkpoint: d.taskCheckpoint}
	if d.cfg.Mode == "ui" {
		env.Desktop = d.desktop(env.TaskID)
	}
	if d.browser != nil {
		env.Browser = d.agentBrowser(env.TaskID)
	}
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
		if d.browser != nil {
			d.browser.Release(env.TaskID)
		}
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

// agentContext is the message that starts every Agent's conversation: the
// user's Memory and the Machine Profile (PLAN.md §8.5).
func (d *Daemon) agentContext(ctx context.Context) string {
	memory, _ := d.memories.Accepted(ctx)
	return profile.Context(memory, d.machine())
}

// machine describes the Machine for its Profile.
func (d *Daemon) machine() profile.Machine {
	m := profile.Machine{OS: d.osName, Arch: runtime.GOARCH, Mode: d.cfg.Mode, Landlock: d.abi >= 1, Replaying: d.software.Replaying()}
	if pkgs, err := d.software.Installed(context.Background()); err == nil {
		for _, p := range pkgs {
			name, _, _ := strings.Cut(p.Name, ":") // nginx:arm64 → nginx
			m.Software = append(m.Software, profile.Software{Manager: p.Manager, Name: name, Version: p.Version})
		}
	}
	for _, s := range d.services.List() {
		ps := profile.Service{Name: s.Name, State: service.StateName(s.State)}
		for _, p := range s.Ports {
			ps.Ports = append(ps.Ports, int(p))
		}
		m.Services = append(m.Services, ps)
	}
	for _, l := range d.services.Listeners() {
		if l.Internal() {
			continue
		}
		m.Listeners = append(m.Listeners, profile.Listener{Port: l.Port, Process: l.Process, Service: l.Service})
	}
	return m
}

// taskCheckpoint takes a Task's Checkpoint before its first change (for Services).
func (d *Daemon) taskCheckpoint(ctx context.Context, taskID, title string) (string, string, bool, error) {
	cp, created, err := d.software.TaskCheckpoint(ctx, software.Call{TaskID: taskID, TaskTitle: title, Actor: "agent"})
	if err != nil {
		return "", "", false, err
	}
	return cp.Id, cp.Name, created, nil
}

// userEnv is the environment of what AOS runs as aos outside a Session: pipx,
// npm and Services.
func (d *Daemon) userEnv() []string {
	home := d.layout.Home
	return []string{"HOME=" + home, "USER=aos", "LOGNAME=aos", "SHELL=/bin/bash", "LANG=C.UTF-8",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:" + home + "/.local/bin",
		"NPM_CONFIG_PREFIX=" + home + "/.local", "npm_config_yes=true", "PIP_NO_INPUT=1"}
}

// socketOwners asks `aosd __sockets`, running as aos, which of aos's processes
// hold which sockets: root in the container can't read their /proc/<pid>/fd.
func (d *Daemon) socketOwners() map[string]int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, d.exe, service.SocketsArg)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: d.uid, Gid: d.gid, Groups: []uint32{}}}
	out, err := cmd.Output()
	var owners map[string]int
	if err != nil || json.Unmarshal(out, &owners) != nil {
		return nil
	}
	return owners
}

// asUser runs argv as aos, confined like an Agent (pipx and npm installs).
func (d *Daemon) asUser(argv ...string) (*exec.Cmd, error) {
	rs, err := d.plan(d.agentPolicy())
	if err != nil {
		return nil, err
	}
	cmd, err := sandbox.Command(rs, d.uid, d.gid, d.userEnv(), argv...)
	if err != nil {
		return nil, err
	}
	cmd.Dir = d.layout.Home
	return cmd, nil
}

// launchService runs a Service's command: as aos, confined like the Agent
// that created it, or as root when it was created as a Privileged Tool call.
func (d *Daemon) launchService(def service.Definition) (*exec.Cmd, error) {
	env := d.userEnv()
	dir := d.layout.Home
	if def.Root {
		env, dir = []string{"HOME=/root", "USER=root", "LANG=C.UTF-8", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}, "/"
	}
	keys := make([]string, 0, len(def.Env))
	for k := range def.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+def.Env[k])
	}
	if def.Dir != "" {
		dir = def.Dir
	}
	var cmd *exec.Cmd
	if def.Root {
		cmd = exec.Command("/bin/bash", "-c", def.Command)
		cmd.Env = env
	} else {
		rs, err := d.plan(d.agentPolicy())
		if err != nil {
			return nil, err
		}
		if cmd, err = sandbox.Command(rs, d.uid, d.gid, env, "bash", "-c", def.Command); err != nil {
			return nil, err
		}
	}
	cmd.Dir = dir
	return cmd, nil
}

// refused records a request the Unix socket turned away (PLAN.md §7.5).
func (d *Daemon) refused(r *http.Request, pid int, reason string) {
	_ = d.audit.Record(context.Background(), audit.Entry{Tool: "api", Arguments: `{"path":"` + strings.ReplaceAll(r.URL.Path, `"`, "") + `"}`,
		Decision: "deny", DecidedBy: "policy", Result: fmt.Sprintf("refused pid %d: %s", pid, reason), Actor: "unix-socket"})
}

func (d *Daemon) info() *aosv1.InfoResponse {
	v := d.settings.Values()
	key, keySource, hint := keys.status()
	autonomy := map[policy.Autonomy]aosv1.Autonomy{policy.Auto: aosv1.Autonomy_AUTONOMY_AUTO, policy.ConfirmRisky: aosv1.Autonomy_AUTONOMY_CONFIRM_RISKY, policy.ConfirmAll: aosv1.Autonomy_AUTONOMY_CONFIRM_ALL}[v.Autonomy]
	today, _ := d.usage.Today(context.Background())
	browserOK, browserWhy := d.browserStatus()
	return &aosv1.InfoResponse{Browser: browserOK, BrowserUnavailable: browserWhy, Mode: d.cfg.Mode, Version: Version, LandlockAbi: int32(d.abi), ApiKey: key, ApiKeyHint: hint, ApiKeySource: keySource, Model: v.Model, Autonomy: autonomy,
		MaxTasks: int32(v.MaxTasks), MaxRetries: int32(v.MaxRetries), Today: today, PricesKnown: d.pricesKnown(v.Model), Replay: d.software.ReplayStatus(),
		TaskCostLimitUsd: v.TaskCostLimit, DailyCostLimitUsd: v.DailyCostLimit}
}

// pricesKnown reports whether prices.yaml prices model.
func (d *Daemon) pricesKnown(model string) bool {
	prices, err := d.usage.Prices.Prices()
	_, ok := prices.Lookup(model)
	return err == nil && ok
}

func (d *Daemon) expireTrash(ctx context.Context, runner files.Runner) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		var n int
		v := d.settings.Values() // retention and the size cap can change in System Settings
		args := files.ExpireArgs{Retention: time.Duration(v.TrashRetentionDays) * 24 * time.Hour, MaxBytes: int64(v.TrashMaxGB) << 30}
		if err := runner.Run(ctx, files.OpExpireTrash, args, &n, nil); err == nil && n > 0 {
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

// post saves and publishes a notification; failing to save it is only logged.
func (d *Daemon) post(ctx context.Context, n *aosv1.Notification) {
	if _, err := d.notify.Post(context.WithoutCancel(ctx), n); err != nil {
		log.Printf("notification %q: %v", n.Title, err)
	}
}

// desktop gives a Task's Agent the Desktop Tools' way to the Desktop.
func (d *Daemon) desktop(taskID string) *tool.Desktop {
	return &tool.Desktop{
		Notify: func(ctx context.Context, n tool.Notice) error {
			_, err := d.notify.Post(context.WithoutCancel(ctx), &aosv1.Notification{Title: n.Title, Body: n.Body, TaskId: taskID, Port: int32(n.Port)})
			return err
		},
		Open: func(_ context.Context, path string, dir bool) error {
			d.bus.Publish(&aosv1.Event{Kind: &aosv1.Event_OpenInDesktop{OpenInDesktop: &aosv1.OpenInDesktop{TaskId: taskID, Path: path, Dir: dir}}})
			return nil
		},
	}
}
