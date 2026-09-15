package software

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
	"github.com/amantiwari/agentic-os/internal/events"
	"github.com/amantiwari/agentic-os/internal/tool"
)

// Services lets a Restore return Services to earlier definitions.
type Services interface {
	// Definition returns a Service's definition as JSON, "" if there is none.
	Definition(name string) string
	// Restore gives a Service a definition; "" removes it.
	Restore(ctx context.Context, name, definition string) error
}

// Manager runs the operations with root authority (PLAN.md §11), one at a
// time across all Tasks, and records what each changed in the Ledger.
type Manager struct {
	Ledger *Ledger
	// Etc tracks /etc.
	Etc *Tree
	// Archives and Lists are apt's cache folders in the aos-pkgcache volume.
	Archives, Lists string
	// AsUser returns a command running argv as aos, confined like an Agent
	// (pipx and npm install into the home folder).
	AsUser func(argv ...string) (*exec.Cmd, error)
	// Services, if set, takes part in Restores.
	Services Services
	Bus      *events.Bus
	// Notify, if set, tells the user a Replay failed or left notes.
	Notify func(context.Context, *aosv1.Notification)
	Logf   func(format string, args ...any)

	once sync.Once
	sem  chan struct{}
	arch string

	mu     sync.Mutex
	replay *aosv1.ReplayStatus
}

// Call says who asks for an operation.
type Call struct {
	TaskID, TaskTitle, Actor string
}

// Outcome is what an operation did.
type Outcome struct {
	// Op is the operation as recorded; nil when nothing changed.
	Op *Op
	// Output is the package manager's or command's output.
	Output string
	// Checkpoint is set when this operation took its Task's Checkpoint.
	Checkpoint *aosv1.Checkpoint
	// Waited is true when it waited for Replay or another operation.
	Waited bool
	Notes  []string
}

// packageTimeout bounds a package operation, which finishes even when its
// Task is cancelled (PLAN.md §8.1).
const packageTimeout = 30 * time.Minute

var rootEnv = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LANG=C.UTF-8",
	"DEBIAN_FRONTEND=noninteractive", "APT_LISTCHANGES_FRONTEND=none", "TERM=dumb"}

var aptOptions = []string{"-o", "DPkg::Lock::Timeout=120", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"}

func (m *Manager) init() {
	m.once.Do(func() { m.sem = make(chan struct{}, 1) })
}

// lock waits for the one operation slot; Replay holds it while it runs.
func (m *Manager) lock(ctx context.Context) (waited bool, err error) {
	m.init()
	select {
	case m.sem <- struct{}{}:
		return false, nil
	default:
	}
	select {
	case m.sem <- struct{}{}:
		return true, nil
	case <-ctx.Done():
		return true, ctx.Err()
	}
}

func (m *Manager) unlock() { <-m.sem }

func (m *Manager) logf(format string, args ...any) {
	if m.Logf != nil {
		m.Logf(format, args...)
	}
}

// root runs a program as root and returns its combined output.
func (m *Manager) root(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = rootEnv
	cmd.Dir = "/"
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// user runs argv as aos, confined; stdout and the combined output are returned.
func (m *Manager) user(ctx context.Context, argv ...string) (stdout, combined string, err error) {
	if m.AsUser == nil {
		return "", "", errors.New("pipx and npm installs are not available")
	}
	cmd, err := m.AsUser(argv...)
	if err != nil {
		return "", "", err
	}
	var out, all bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.MultiWriter(&out, &all), &all
	if err := cmd.Start(); err != nil {
		return "", "", err
	}
	stop := context.AfterFunc(ctx, func() { _ = cmd.Process.Kill() })
	defer stop()
	err = cmd.Wait()
	return out.String(), all.String(), err
}

func (m *Manager) nativeArch(ctx context.Context) string {
	if m.arch == "" {
		out, _ := m.root(ctx, "dpkg", "--print-architecture")
		m.arch = strings.TrimSpace(out)
	}
	return m.arch
}

// packages returns what a package manager has installed.
func (m *Manager) packages(ctx context.Context, manager string) (Packages, error) {
	switch manager {
	case "apt":
		out, err := m.root(ctx, "dpkg-query", "-W", "-f", dpkgFormat)
		if err != nil {
			return nil, fmt.Errorf("dpkg-query: %v: %s", err, out)
		}
		p := parseDpkg(out)
		auto, _ := m.root(ctx, "apt-mark", "showauto")
		p.markAuto(auto, m.nativeArch(ctx))
		return p, nil
	case "pipx":
		out, all, err := m.user(ctx, "pipx", "list", "--json")
		if err != nil && strings.TrimSpace(out) == "" {
			return nil, fmt.Errorf("pipx list: %v: %s", err, all)
		}
		return parsePipx(out)
	case "npm":
		// npm ls exits 1 for problems it still reports as JSON.
		out, all, err := m.user(ctx, "npm", "ls", "-g", "--json", "--depth=0")
		if err != nil && strings.TrimSpace(out) == "" {
			return nil, fmt.Errorf("npm ls: %v: %s", err, all)
		}
		return parseNpm(out)
	}
	return nil, fmt.Errorf("unknown package manager %q", manager)
}

// state is what an operation may change.
type state struct {
	pkgs map[string]Packages
	etc  Snapshot
}

func (m *Manager) snapshot(ctx context.Context, managers []string) (state, error) {
	s := state{pkgs: map[string]Packages{}}
	for _, mgr := range managers {
		p, err := m.packages(ctx, mgr)
		if err != nil {
			return s, err
		}
		s.pkgs[mgr] = p
	}
	etc, err := m.Etc.Scan()
	s.etc = etc
	return s, err
}

func diffStates(before, after state) []Change {
	var changes []Change
	mgrs := make([]string, 0, len(before.pkgs))
	for mgr := range before.pkgs {
		mgrs = append(mgrs, mgr)
	}
	sort.Strings(mgrs)
	for _, mgr := range mgrs {
		changes = append(changes, diffPackages(mgr, before.pkgs[mgr], after.pkgs[mgr])...)
	}
	return append(changes, diffFiles(before.etc, after.etc)...)
}

// operate runs fn as one operation: it waits for the slot, takes the Task's
// Checkpoint before its first software change, and records what fn changed,
// even when fn failed halfway.
func (m *Manager) operate(ctx context.Context, c Call, action, summary string, managers []string, fn func(ctx context.Context) (string, error)) (Outcome, error) {
	var out Outcome
	waited, err := m.lock(ctx)
	out.Waited = waited
	if err != nil {
		return out, err
	}
	defer m.unlock()
	if c.TaskID != "" {
		cp, created, err := m.taskCheckpoint(ctx, c)
		if err != nil {
			return out, err
		}
		if created {
			out.Checkpoint = cp
		}
	}
	before, err := m.snapshot(ctx, managers)
	if err != nil {
		return out, fmt.Errorf("recording the state before: %w", err)
	}
	output, runErr := fn(ctx)
	out.Output = output
	after, err := m.snapshot(context.WithoutCancel(ctx), managers)
	if err != nil {
		return out, errors.Join(runErr, fmt.Errorf("recording the state after: %w", err))
	}
	if changes := diffStates(before, after); len(changes) > 0 {
		op := &Op{TaskID: c.TaskID, Action: action, Summary: summary, Actor: c.Actor, Changes: changes}
		if err := m.Ledger.Append(context.WithoutCancel(ctx), op); err != nil {
			return out, errors.Join(runErr, err)
		}
		out.Op = op
	}
	return out, runErr
}

// TaskCheckpoint returns the Checkpoint taken before the Task's first change,
// taking it now if there is none; created reports that. Creating a Service is
// such a change too.
func (m *Manager) TaskCheckpoint(ctx context.Context, c Call) (cp *aosv1.Checkpoint, created bool, err error) {
	return m.taskCheckpoint(ctx, c)
}

// taskCheckpoint returns the Checkpoint taken before the Task's first software
// change, taking it now if there is none.
func (m *Manager) taskCheckpoint(ctx context.Context, c Call) (*aosv1.Checkpoint, bool, error) {
	cps, err := m.Ledger.checkpoints(ctx, `WHERE task_id = ? AND automatic ORDER BY created_at LIMIT 1`, c.TaskID)
	if err != nil {
		return nil, false, err
	}
	if len(cps) > 0 {
		return cps[0], false, nil
	}
	title := c.TaskTitle
	if r := []rune(title); len(r) > 60 {
		title = string(r[:59]) + "…"
	}
	cp, err := m.Ledger.CreateCheckpoint(ctx, "Before: "+title, c.TaskID, true)
	return cp, err == nil, err
}

var packagePatterns = map[string]*regexp.Regexp{
	"apt":  regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]*(:[a-z0-9]+)?(=[A-Za-z0-9.+~:-]+)?$`),
	"pipx": regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(\[[A-Za-z0-9,._-]+\])?((==|>=|<=|~=|!=)[A-Za-z0-9.*+!_-]+)?$`),
	"npm":  regexp.MustCompile(`^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*(@[A-Za-z0-9.^~<>=*_-]+)?$`),
}

// validPackages checks names before they reach a package manager's command line.
func validPackages(manager string, packages []string) error {
	re, ok := packagePatterns[manager]
	if !ok {
		return fmt.Errorf("unknown package manager %q: use apt, pipx or npm", manager)
	}
	if len(packages) == 0 {
		return errors.New("no packages given")
	}
	for _, p := range packages {
		if !re.MatchString(p) {
			return fmt.Errorf("%q is not a valid %s package name", p, manager)
		}
	}
	return nil
}

// Install installs packages with apt (as root), pipx or npm (as aos).
func (m *Manager) Install(ctx context.Context, c Call, manager string, packages []string) (Outcome, error) {
	if err := validPackages(manager, packages); err != nil {
		return Outcome{}, err
	}
	summary := fmt.Sprintf("Install %s (%s)", strings.Join(packages, ", "), manager)
	return m.operate(ctx, c, "install", summary, []string{manager}, func(ctx context.Context) (string, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), packageTimeout)
		defer cancel()
		switch manager {
		case "apt":
			args := append(append([]string{"install", "-y", "--no-install-recommends"}, aptOptions...), packages...)
			return m.apt(ctx, args...)
		case "pipx":
			return m.eachUser(ctx, packages, func(p string) []string { return []string{"pipx", "install", p} })
		default:
			_, out, err := m.user(ctx, append([]string{"npm", "install", "-g", "--no-fund", "--no-audit"}, packages...)...)
			return out, err
		}
	})
}

// Remove removes packages, and with apt the dependencies nothing else needs.
func (m *Manager) Remove(ctx context.Context, c Call, manager string, packages []string) (Outcome, error) {
	if err := validPackages(manager, packages); err != nil {
		return Outcome{}, err
	}
	summary := fmt.Sprintf("Remove %s (%s)", strings.Join(packages, ", "), manager)
	return m.operate(ctx, c, "remove", summary, []string{manager}, func(ctx context.Context) (string, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), packageTimeout)
		defer cancel()
		switch manager {
		case "apt":
			out, err := m.root(ctx, "apt-get", append(append([]string{"remove", "-y", "--autoremove"}, aptOptions...), packages...)...)
			return out, err
		case "pipx":
			return m.eachUser(ctx, packages, func(p string) []string { return []string{"pipx", "uninstall", p} })
		default:
			_, out, err := m.user(ctx, append([]string{"npm", "uninstall", "-g"}, packages...)...)
			return out, err
		}
	})
}

func (m *Manager) eachUser(ctx context.Context, packages []string, argv func(string) []string) (string, error) {
	var log strings.Builder
	var errs []error
	for _, p := range packages {
		_, out, err := m.user(ctx, argv(p)...)
		log.WriteString(out)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
		}
	}
	return log.String(), errors.Join(errs...)
}

// apt runs apt-get, updating the package lists first when they are old, and
// again when apt can't find a package.
func (m *Manager) apt(ctx context.Context, args ...string) (string, error) {
	var log strings.Builder
	updated := false
	if !m.listsFresh() {
		out, err := m.root(ctx, "apt-get", "update")
		log.WriteString(out)
		if err != nil {
			return log.String(), fmt.Errorf("apt-get update: %w", err)
		}
		updated = true
	}
	out, err := m.root(ctx, "apt-get", args...)
	log.WriteString(out)
	if err != nil && !updated && staleLists(out) {
		up, uerr := m.root(ctx, "apt-get", "update")
		log.WriteString(up)
		if uerr == nil {
			out, err = m.root(ctx, "apt-get", args...)
			log.WriteString(out)
		}
	}
	if err != nil {
		return log.String(), fmt.Errorf("apt-get %s: %w", args[0], err)
	}
	return log.String(), nil
}

// listsFresh reports whether apt's package lists were updated in the last day.
func (m *Manager) listsFresh() bool {
	entries, err := os.ReadDir(m.Lists)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "_Packages") {
			if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) < 24*time.Hour {
				return true
			}
		}
	}
	return false
}

func staleLists(out string) bool {
	for _, s := range []string{"Unable to locate package", "has no installation candidate", "404  Not Found", "Failed to fetch"} {
		if strings.Contains(out, s) {
			return true
		}
	}
	return false
}

// RunAsRoot runs a bash command as root, outside the sandbox, in dir.
func (m *Manager) RunAsRoot(ctx context.Context, c Call, command, dir string, timeout time.Duration) (tool.CommandResult, Outcome, error) {
	var res tool.CommandResult
	summary := "Run as root: " + oneLine(command)
	out, err := m.operate(ctx, c, "command", summary, []string{"apt"}, func(ctx context.Context) (string, error) {
		cmd := exec.Command("bash", "-c", command)
		cmd.Env = rootEnv
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			dir = "/"
		}
		cmd.Dir = dir
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		start := time.Now()
		if err := cmd.Start(); err != nil {
			return "", err
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		var err error
		select {
		case err = <-done:
		case <-timer.C:
			res.TimedOut = true
			err = killGroup(cmd, done)
		case <-ctx.Done():
			_ = killGroup(cmd, done)
			return buf.String(), ctx.Err()
		}
		res.Output, res.Duration, res.Cwd = buf.Bytes(), time.Since(start), dir
		var exit *exec.ExitError
		switch {
		case errors.As(err, &exit):
			res.ExitCode = exit.ExitCode()
		case err != nil && !res.TimedOut:
			return buf.String(), err
		}
		return buf.String(), nil
	})
	return res, out, err
}

// killGroup stops a command's process group: SIGTERM, then SIGKILL.
func killGroup(cmd *exec.Cmd, done <-chan error) error {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return <-done
	}
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 120 {
		return string(r[:119]) + "…"
	}
	return s
}

// Checkpoint names the Ledger's current position.
func (m *Manager) Checkpoint(ctx context.Context, c Call, name string) (*aosv1.Checkpoint, error) {
	return m.Ledger.CreateCheckpoint(ctx, name, c.TaskID, false)
}

// ---------------------------------------------------------------- Restore

// aptPlan is what apt must do to give packages their target states.
type aptPlan struct {
	remove  []string          // name:arch
	install map[string]string // name:arch → version
	// auto are the dependency marks of every package that stays installed.
	auto  map[string]bool
	notes []string
}

// planApt compares targets (name:arch → JSON state, "" absent) with what is
// installed. With newer set (Replay), a newer installed version is kept.
func planApt(targets map[string]string, installed Packages, newer func(a, b string) bool) aptPlan {
	p := aptPlan{install: map[string]string{}, auto: map[string]bool{}}
	keys := make([]string, 0, len(targets))
	for k := range targets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cur, isInstalled := installed[key]
		if targets[key] == "" {
			if isInstalled {
				p.remove = append(p.remove, key)
			}
			continue
		}
		var want Pkg
		if json.Unmarshal([]byte(targets[key]), &want) != nil {
			continue
		}
		p.auto[key] = want.Auto
		switch {
		case isInstalled && cur.Version == want.Version:
		case isInstalled && newer != nil && newer(cur.Version, want.Version):
			p.notes = append(p.notes, fmt.Sprintf("kept %s %s: newer than %s in the Ledger", key, cur.Version, want.Version))
		default:
			p.install[key] = want.Version
		}
	}
	return p
}

// applyApt carries out a plan. Cached .deb files are installed first without
// the network; what is missing comes through the package lists, and a version
// no longer available is replaced by the newest one (PLAN.md §11).
func (m *Manager) applyApt(ctx context.Context, p aptPlan, purge bool) (notes []string, fromCache int, err error) {
	notes = append(notes, p.notes...)
	if len(p.remove) > 0 {
		verb := "remove"
		if purge {
			verb = "purge"
		}
		if out, err := m.root(ctx, "apt-get", append(append([]string{verb, "-y"}, aptOptions...), p.remove...)...); err != nil {
			return notes, 0, fmt.Errorf("apt-get %s: %v\n%s", verb, err, lastLines(out, 15))
		}
	}
	if len(p.install) > 0 {
		keys := make([]string, 0, len(p.install))
		for k := range p.install {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var debs, specs []string
		for _, k := range keys {
			if f := debFile(m.Archives, k, p.install[k]); fileExists(f) {
				debs = append(debs, f)
			} else {
				specs = append(specs, k+"="+p.install[k])
			}
		}
		installed := false
		if len(specs) == 0 {
			empty, err := os.MkdirTemp("", "aos-nolists-")
			if err == nil {
				_ = os.Mkdir(filepath.Join(empty, "partial"), 0o755)
				args := append([]string{"install", "-y", "--no-download", "--allow-downgrades", "--no-install-recommends",
					"-o", "Dir::State::lists=" + empty}, aptOptions...)
				_, err = m.root(ctx, "apt-get", append(args, debs...)...)
				os.RemoveAll(empty)
				installed, fromCache = err == nil, len(debs)
			}
		}
		if !installed {
			fromCache = 0
			args := append([]string{"install", "-y", "--allow-downgrades", "--no-install-recommends"}, aptOptions...)
			if _, err := m.apt(ctx, append(append(args, debs...), specs...)...); err != nil {
				// Versions that are gone: the newest instead, with a note.
				var names []string
				for _, s := range specs {
					name, version, _ := strings.Cut(s, "=")
					names = append(names, name)
					notes = append(notes, fmt.Sprintf("installed the newest %s: %s is no longer available", name, version))
				}
				if out, err := m.apt(ctx, append(append(args, debs...), names...)...); err != nil {
					return notes, 0, fmt.Errorf("%v\n%s", err, lastLines(out, 15))
				}
			}
		}
	}
	var auto, manual []string
	for k, a := range p.auto {
		if a {
			auto = append(auto, k)
		} else {
			manual = append(manual, k)
		}
	}
	sort.Strings(auto)
	sort.Strings(manual)
	if len(auto) > 0 {
		_, _ = m.root(ctx, "apt-mark", append([]string{"auto"}, auto...)...)
	}
	if len(manual) > 0 {
		_, _ = m.root(ctx, "apt-mark", append([]string{"manual"}, manual...)...)
	}
	return notes, fromCache, nil
}

// applyUser gives pipx or npm packages their target states.
func (m *Manager) applyUser(ctx context.Context, manager string, targets map[string]string) error {
	var errs []error
	for name, target := range targets {
		var argv []string
		var want Pkg
		_ = json.Unmarshal([]byte(target), &want)
		switch {
		case manager == "pipx" && target == "":
			argv = []string{"pipx", "uninstall", name}
		case manager == "pipx":
			argv = []string{"pipx", "install", "--force", name + "==" + want.Version}
		case target == "":
			argv = []string{"npm", "uninstall", "-g", name}
		default:
			argv = []string{"npm", "install", "-g", "--no-fund", "--no-audit", name + "@" + want.Version}
		}
		if _, out, err := m.user(ctx, argv...); err != nil {
			errs = append(errs, fmt.Errorf("%s: %v: %s", strings.Join(argv, " "), err, lastLines(out, 5)))
		}
	}
	return errors.Join(errs...)
}

// Restore returns software, /etc and Services to a Checkpoint: everything the
// Ledger records after it is undone. A "Before restoring" Checkpoint is taken
// first, so the Restore can itself be undone (PLAN.md §11).
func (m *Manager) Restore(ctx context.Context, c Call, id string) (Outcome, *aosv1.Checkpoint, error) {
	var out Outcome
	cp, err := m.Ledger.Checkpoint(ctx, id)
	if err != nil {
		return out, nil, err
	}
	if out.Waited, err = m.lock(ctx); err != nil {
		return out, nil, err
	}
	defer m.unlock()
	before, err := m.Ledger.CreateCheckpoint(ctx, "Before restoring "+cp.Name, c.TaskID, true)
	if err != nil {
		return out, nil, err
	}
	ops, err := m.Ledger.After(ctx, cp.LedgerId)
	if err != nil {
		return out, before, err
	}
	// It finishes once started: a half-done Restore helps nobody.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), packageTimeout)
	defer cancel()

	targets := map[string]map[string]string{}
	files, services := map[string]string{}, map[string]string{}
	for k, v := range Undo(ops) {
		switch k.Kind {
		case KindPackage:
			if targets[k.Manager] == nil {
				targets[k.Manager] = map[string]string{}
			}
			targets[k.Manager][k.Name] = v
		case KindFile:
			files[k.Name] = v
		case KindService:
			services[k.Name] = v
		}
	}
	managers := []string{"apt"}
	for _, mgr := range []string{"pipx", "npm"} {
		if len(targets[mgr]) > 0 {
			managers = append(managers, mgr)
		}
	}
	was, err := m.snapshot(ctx, managers)
	if err != nil {
		return out, before, err
	}
	serviceWas := map[string]string{}
	if m.Services != nil {
		for name := range services {
			serviceWas[name] = m.Services.Definition(name)
		}
		// Stop what depends on the software first.
		for name, def := range services {
			if def == "" {
				_ = m.Services.Restore(ctx, name, "")
			}
		}
	}

	var errs []error
	notes, _, err := m.applyApt(ctx, planApt(targets["apt"], was.pkgs["apt"], nil), true)
	out.Notes = append(out.Notes, notes...)
	errs = append(errs, err)
	for _, mgr := range []string{"pipx", "npm"} {
		if len(targets[mgr]) > 0 {
			errs = append(errs, m.applyUser(ctx, mgr, targets[mgr]))
		}
	}
	fileNotes, err := m.Etc.Apply(files)
	out.Notes = append(out.Notes, fileNotes...)
	errs = append(errs, err)
	var serviceChanges []Change
	if m.Services != nil {
		for name, def := range services {
			if def != "" {
				errs = append(errs, m.Services.Restore(ctx, name, def))
			}
			if now := m.Services.Definition(name); now != serviceWas[name] {
				serviceChanges = append(serviceChanges, Change{Kind: KindService, Name: name, Before: serviceWas[name], After: now})
			}
		}
	}

	now, err := m.snapshot(ctx, managers)
	if err != nil {
		return out, before, errors.Join(append(errs, err)...)
	}
	if changes := append(diffStates(was, now), serviceChanges...); len(changes) > 0 {
		op := &Op{TaskID: c.TaskID, Action: "restore", Summary: fmt.Sprintf("Restore to %q (%s)", cp.Name, cp.Id), Actor: c.Actor, Changes: changes}
		if err := m.Ledger.Append(ctx, op); err != nil {
			errs = append(errs, err)
		}
		out.Op = op
	}
	return out, before, errors.Join(errs...)
}

// ---------------------------------------------------------------- Replay

// replayFiles returns the file states Replay writes: only paths the image
// still has as the Ledger first found them, so a newer image's own changes win.
func replayFiles(final, first map[string]string, now Snapshot) (write map[string]string, notes []string) {
	write = map[string]string{}
	paths := make([]string, 0, len(final))
	for p := range final {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		switch {
		case now.Same(p, final[p]):
		case !now.Same(p, first[p]):
			notes = append(notes, fmt.Sprintf("kept this image's %s: it differs from the one AOS first recorded", p))
		default:
			write[p] = final[p]
		}
	}
	return write, notes
}

// ReplayStatus returns the progress of the last Replay.
func (m *Manager) ReplayStatus() *aosv1.ReplayStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.replay == nil {
		return &aosv1.ReplayStatus{}
	}
	return proto.Clone(m.replay).(*aosv1.ReplayStatus)
}

// Replaying reports whether Replay is running.
func (m *Manager) Replaying() bool {
	return m.ReplayStatus().State == aosv1.ReplayState_REPLAY_STATE_RUNNING
}

func (m *Manager) setReplay(change func(s *aosv1.ReplayStatus)) {
	m.mu.Lock()
	if m.replay == nil {
		m.replay = &aosv1.ReplayStatus{}
	}
	change(m.replay)
	s := proto.Clone(m.replay).(*aosv1.ReplayStatus)
	m.mu.Unlock()
	if m.Bus != nil {
		m.Bus.Publish(&aosv1.Event{Kind: &aosv1.Event_ReplayProgress{ReplayProgress: &aosv1.ReplayProgress{Status: s}}})
	}
}

// Replay re-applies the Ledger when a container starts (PLAN.md §11):
// packages at their recorded versions, from the cache when possible, and the
// /etc files Privileged Tools changed. Installs wait until it finishes.
func (m *Manager) Replay(ctx context.Context) error {
	m.setReplay(func(s *aosv1.ReplayStatus) {
		*s = aosv1.ReplayStatus{State: aosv1.ReplayState_REPLAY_STATE_RUNNING, StartedAt: timestamppb.Now(), Message: "Checking the Install Ledger"}
	})
	if _, err := m.lock(ctx); err != nil {
		return err
	}
	defer m.unlock()
	notes, err := m.replayLedger(ctx)
	m.setReplay(func(s *aosv1.ReplayStatus) {
		s.FinishedAt, s.Done = timestamppb.Now(), s.Total
		s.State = aosv1.ReplayState_REPLAY_STATE_DONE
		if err != nil {
			s.State, s.Message = aosv1.ReplayState_REPLAY_STATE_FAILED, "Replay failed: "+err.Error()
		}
		if len(notes) > 0 {
			s.Message += "\n" + strings.Join(notes, "\n")
		}
	})
	if (err != nil || len(notes) > 0) && m.Notify != nil {
		m.Notify(ctx, &aosv1.Notification{Title: "Replay", Body: m.ReplayStatus().Message})
	}
	return err
}

func (m *Manager) replayLedger(ctx context.Context) ([]string, error) {
	ops, err := m.Ledger.After(ctx, 0)
	if err != nil {
		return nil, err
	}
	apt, finalFiles, firstFiles := map[string]string{}, map[string]string{}, map[string]string{}
	first := First(ops)
	for k, v := range Final(ops) {
		switch {
		case k.Kind == KindPackage && k.Manager == "apt":
			apt[k.Name] = v
		case k.Kind == KindFile:
			finalFiles[k.Name], firstFiles[k.Name] = v, first[k]
		}
	}
	// Judge /etc against the image before reinstalling changes it.
	etcNow, err := m.Etc.Scan()
	if err != nil {
		return nil, err
	}
	writes, notes := replayFiles(finalFiles, firstFiles, etcNow)
	installed, err := m.packages(ctx, "apt")
	if err != nil {
		return notes, err
	}
	plan := planApt(apt, installed, func(a, b string) bool {
		_, err := m.root(ctx, "dpkg", "--compare-versions", a, "gt", b)
		return err == nil
	})
	m.setReplay(func(s *aosv1.ReplayStatus) {
		s.Total = int32(len(plan.remove) + len(plan.install) + len(writes))
		s.Message = fmt.Sprintf("Reinstalling %d packages", len(plan.install))
	})
	aptNotes, fromCache, err := m.applyApt(ctx, plan, false)
	notes = append(notes, aptNotes...)
	if err != nil {
		return notes, err
	}
	m.setReplay(func(s *aosv1.ReplayStatus) {
		s.Done = int32(len(plan.remove) + len(plan.install))
		s.Message = fmt.Sprintf("Restoring %d files under /etc", len(writes))
	})
	fileNotes, err := m.Etc.Apply(writes)
	notes = append(notes, fileNotes...)
	if err != nil {
		return notes, err
	}
	m.cleanCache(ops)
	m.setReplay(func(s *aosv1.ReplayStatus) {
		s.Message = fmt.Sprintf("Reinstalled %d packages (%d from the cache), removed %d, restored %d files under /etc",
			len(plan.install), fromCache, len(plan.remove), len(writes))
	})
	if len(plan.install)+len(plan.remove)+len(writes) > 0 {
		m.logf("Replay: reinstalled %d packages (%d from the cache), removed %d, restored %d files under /etc", len(plan.install), fromCache, len(plan.remove), len(writes))
	}
	return notes, nil
}

// cleanCache deletes cached .deb files the Ledger doesn't refer to; every
// version it records, before or after, is kept for Replay and Restore.
func (m *Manager) cleanCache(ops []Op) {
	keep := map[string]bool{}
	for _, op := range ops {
		for _, c := range op.Changes {
			if c.Kind != KindPackage || c.Manager != "apt" {
				continue
			}
			for _, s := range []string{c.Before, c.After} {
				var p Pkg
				if s != "" && json.Unmarshal([]byte(s), &p) == nil {
					keep[debFile(m.Archives, c.Name, p.Version)] = true
				}
			}
		}
	}
	debs, _ := filepath.Glob(filepath.Join(m.Archives, "*.deb"))
	for _, f := range debs {
		if !keep[f] {
			_ = os.Remove(f)
		}
	}
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------- Packages installed

// Installed returns the packages installed through AOS, without dependencies,
// for the Machine Profile.
func (m *Manager) Installed(ctx context.Context) ([]*aosv1.Package, error) {
	all, err := m.Ledger.Packages(ctx)
	var out []*aosv1.Package
	for _, p := range all {
		if !p.Auto {
			out = append(out, p)
		}
	}
	return out, err
}
