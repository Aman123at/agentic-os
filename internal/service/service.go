// Package service is aosd's service manager (ADR-0005, PLAN.md §12): it keeps
// the Services Agents create running, restarts them by their policy, keeps
// their logs and finds the ports they listen on. Creating and removing a
// Service is recorded in the Install Ledger, so a Restore undoes it too.
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
	"github.com/amantiwari/agentic-os/internal/events"
	"github.com/amantiwari/agentic-os/internal/software"
	"github.com/amantiwari/agentic-os/internal/store"
)

// Restart policies.
const (
	Always    = "always"
	OnFailure = "on-failure"
	Never     = "never"
)

// ErrNoService is returned for unknown Services.
var ErrNoService = errors.New("no such Service")

// Definition is a Service as stored, and as recorded in the Ledger.
type Definition struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Dir     string            `json:"dir,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	// Root runs it as root, unconfined; otherwise it runs as aos, confined
	// like the Agent that created it.
	Root      bool   `json:"root,omitempty"`
	Autostart bool   `json:"autostart"`
	Restart   string `json:"restart"`
	TaskID    string `json:"task_id,omitempty"`
}

// JSON is the definition as the Ledger records it.
func (d Definition) JSON() string {
	b, _ := json.Marshal(d)
	return string(b)
}

var (
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,62}$`)
	envPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func (d *Definition) validate() error {
	if !namePattern.MatchString(d.Name) {
		return fmt.Errorf("invalid Service name %q: use lower-case letters, digits, '-', '_' and '.'", d.Name)
	}
	if strings.TrimSpace(d.Command) == "" {
		return errors.New("the Service's command is empty")
	}
	switch d.Restart {
	case "":
		d.Restart = OnFailure
	case Always, OnFailure, Never:
	default:
		return fmt.Errorf("restart policy %q: use always, on-failure or never", d.Restart)
	}
	for k := range d.Env {
		if !envPattern.MatchString(k) {
			return fmt.Errorf("environment variable name %q is not valid", k)
		}
	}
	return nil
}

// Launch returns the command that runs a Service's command: bash -c, as aos
// confined like an Agent, or as root.
type Launch func(d Definition) (*exec.Cmd, error)

// Supervisor runs the Services.
type Supervisor struct {
	DB     *store.DB
	Ledger *software.Ledger
	Bus    *events.Bus
	Launch Launch
	// LogDir keeps each Service's log file.
	LogDir string
	// Procfs is /proc; tests use a fixture.
	Procfs string
	// Backoff is the first delay before a restart; it doubles up to a minute.
	Backoff time.Duration
	Logf    func(format string, args ...any)

	mu        sync.Mutex
	services  map[string]*svc
	listeners []Listener
}

// svc is a Service and how it runs now.
type svc struct {
	def      Definition
	state    aosv1.ServiceState
	pid      int
	restarts int
	lastExit string
	started  time.Time
	logs     *logBuffer
	stop     chan struct{} // closed to stop the run loop
	done     chan struct{} // closed when the run loop has ended
}

func (s *Supervisor) backoff() time.Duration {
	if s.Backoff > 0 {
		return s.Backoff
	}
	return time.Second
}

// Load reads the stored Services; none is started yet.
func (s *Supervisor) Load(ctx context.Context) error {
	rows, err := s.DB.Read().QueryContext(ctx, `SELECT definition FROM services ORDER BY name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.services == nil {
		s.services = map[string]*svc{}
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var d Definition
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			return err
		}
		s.services[d.Name] = &svc{def: d, state: aosv1.ServiceState_SERVICE_STATE_STOPPED, logs: newLogBuffer(s.LogDir, d.Name)}
	}
	return rows.Err()
}

// StartAll starts every Service that starts with the Machine.
func (s *Supervisor) StartAll() {
	s.mu.Lock()
	var start []*svc
	for _, v := range s.services {
		if v.def.Autostart {
			start = append(start, v)
		}
	}
	s.mu.Unlock()
	for _, v := range start {
		s.start(v)
	}
}

// Close stops every Service.
func (s *Supervisor) Close() {
	s.mu.Lock()
	all := make([]*svc, 0, len(s.services))
	for _, v := range s.services {
		all = append(all, v)
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, v := range all {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.halt(v)
			v.logs.Close()
		}()
	}
	wg.Wait()
}

// Create adds a Service, or replaces the one with its name, and starts it.
func (s *Supervisor) Create(ctx context.Context, d Definition, actor string) (*aosv1.ServiceInfo, error) {
	if err := d.validate(); err != nil {
		return nil, err
	}
	before := s.Definition(d.Name)
	if err := s.save(ctx, d); err != nil {
		return nil, err
	}
	verb := "Create"
	if before != "" {
		verb = "Replace"
	}
	if err := s.record(ctx, d.TaskID, actor, verb+" Service "+d.Name, d.Name, before, d.JSON()); err != nil {
		return nil, err
	}
	s.start(s.put(d))
	return s.Get(d.Name)
}

// Remove stops a Service and deletes it.
func (s *Supervisor) Remove(ctx context.Context, name, actor, taskID string) error {
	before := s.Definition(name)
	if before == "" {
		return fmt.Errorf("%s: %w", name, ErrNoService)
	}
	if err := s.delete(ctx, name); err != nil {
		return err
	}
	return s.record(ctx, taskID, actor, "Remove Service "+name, name, before, "")
}

// Definition implements software.Services.
func (s *Supervisor) Definition(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.services[name]; ok {
		return v.def.JSON()
	}
	return ""
}

// Restore implements software.Services: a Restore gives a Service an earlier
// definition, or removes it. The Restore itself is what the Ledger records.
func (s *Supervisor) Restore(ctx context.Context, name, definition string) error {
	if definition == "" {
		if s.Definition(name) == "" {
			return nil
		}
		return s.delete(ctx, name)
	}
	var d Definition
	if err := json.Unmarshal([]byte(definition), &d); err != nil {
		return err
	}
	if err := s.save(ctx, d); err != nil {
		return err
	}
	v := s.put(d)
	if d.Autostart {
		s.start(v)
	}
	return nil
}

func (s *Supervisor) save(ctx context.Context, d Definition) error {
	now := store.Millis(time.Now())
	return s.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO services (name, definition, created_at, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (name) DO UPDATE SET definition = excluded.definition, updated_at = excluded.updated_at`, d.Name, d.JSON(), now, now)
		return err
	})
}

func (s *Supervisor) delete(ctx context.Context, name string) error {
	s.mu.Lock()
	v := s.services[name]
	s.mu.Unlock()
	if v != nil {
		s.halt(v)
	}
	err := s.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM services WHERE name = ?`, name)
		return err
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.services, name)
	s.mu.Unlock()
	if v != nil {
		v.logs.Close()
		s.publish(&aosv1.ServiceChanged{Service: &aosv1.ServiceInfo{Name: name}, Removed: true})
	}
	return nil
}

func (s *Supervisor) record(ctx context.Context, taskID, actor, summary, name, before, after string) error {
	if s.Ledger == nil {
		return nil
	}
	return s.Ledger.Append(ctx, &software.Op{TaskID: taskID, Action: "service", Summary: summary, Actor: actor,
		Changes: []software.Change{{Kind: software.KindService, Name: name, Before: before, After: after}}})
}

// put stores a definition in memory, stopping the Service's old run first.
func (s *Supervisor) put(d Definition) *svc {
	s.mu.Lock()
	if s.services == nil {
		s.services = map[string]*svc{}
	}
	v, ok := s.services[d.Name]
	s.mu.Unlock()
	if ok {
		s.halt(v)
	} else {
		v = &svc{state: aosv1.ServiceState_SERVICE_STATE_STOPPED, logs: newLogBuffer(s.LogDir, d.Name)}
	}
	s.mu.Lock()
	v.def, v.restarts, v.lastExit = d, 0, ""
	s.services[d.Name] = v
	s.mu.Unlock()
	return v
}

func (s *Supervisor) find(name string) (*svc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.services[name]
	if !ok {
		return nil, fmt.Errorf("%s: %w", name, ErrNoService)
	}
	return v, nil
}

// Start starts a stopped Service.
func (s *Supervisor) Start(name string) (*aosv1.ServiceInfo, error) {
	v, err := s.find(name)
	if err != nil {
		return nil, err
	}
	s.start(v)
	return s.Get(name)
}

// Stop stops a Service until it is started again or the Machine restarts.
func (s *Supervisor) Stop(name string) (*aosv1.ServiceInfo, error) {
	v, err := s.find(name)
	if err != nil {
		return nil, err
	}
	s.halt(v)
	return s.Get(name)
}

// Restart stops and starts a Service.
func (s *Supervisor) Restart(name string) (*aosv1.ServiceInfo, error) {
	v, err := s.find(name)
	if err != nil {
		return nil, err
	}
	s.halt(v)
	s.mu.Lock()
	v.restarts = 0
	s.mu.Unlock()
	s.start(v)
	return s.Get(name)
}

// start begins v's run loop unless it is running.
func (s *Supervisor) start(v *svc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v.done != nil {
		select {
		case <-v.done:
		default:
			return // running
		}
	}
	v.stop, v.done = make(chan struct{}), make(chan struct{})
	go s.run(v, v.stop, v.done)
}

// halt ends v's run loop and waits for it.
func (s *Supervisor) halt(v *svc) {
	s.mu.Lock()
	stop, done := v.stop, v.done
	if stop != nil {
		select {
		case <-stop:
		default:
			close(stop)
		}
	}
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}

// run keeps v running according to its restart policy until stop is closed.
func (s *Supervisor) run(v *svc, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	backoff := s.backoff()
	for {
		s.mu.Lock()
		d := v.def
		s.mu.Unlock()
		cmd, err := s.Launch(d)
		if err == nil {
			if cmd.SysProcAttr == nil {
				cmd.SysProcAttr = &syscall.SysProcAttr{}
			}
			cmd.SysProcAttr.Setpgid = true
			cmd.Stdout, cmd.Stderr = v.logs, v.logs
			err = cmd.Start()
		}
		if err != nil {
			fmt.Fprintf(v.logs, "[aos] could not start: %v\n", err)
			s.update(v, func() {
				v.state, v.pid, v.lastExit = aosv1.ServiceState_SERVICE_STATE_FAILED, 0, "could not start: "+err.Error()
			})
			return
		}
		pid, started := cmd.Process.Pid, time.Now()
		fmt.Fprintf(v.logs, "[aos] started (pid %d)\n", pid)
		s.update(v, func() { v.state, v.pid, v.started = aosv1.ServiceState_SERVICE_STATE_RUNNING, pid, started })
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		select {
		case <-exited:
		case <-stop:
			terminate(pid, exited)
			fmt.Fprintf(v.logs, "[aos] stopped\n")
			s.update(v, func() {
				v.state, v.pid, v.lastExit = aosv1.ServiceState_SERVICE_STATE_STOPPED, 0, exitText(cmd.ProcessState)
			})
			return
		}
		// Children left in its process group, such as workers, go with it.
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		how, ok := exitText(cmd.ProcessState), cmd.ProcessState.Success()
		if d.Restart == Never || d.Restart == OnFailure && ok {
			state := aosv1.ServiceState_SERVICE_STATE_STOPPED
			if !ok {
				state = aosv1.ServiceState_SERVICE_STATE_FAILED
			}
			fmt.Fprintf(v.logs, "[aos] exited: %s\n", how)
			s.update(v, func() { v.state, v.pid, v.lastExit = state, 0, how })
			return
		}
		if time.Since(started) > time.Minute {
			backoff = s.backoff()
		}
		fmt.Fprintf(v.logs, "[aos] exited: %s; restarting in %s\n", how, backoff)
		s.update(v, func() {
			v.state, v.pid, v.lastExit = aosv1.ServiceState_SERVICE_STATE_RESTARTING, 0, how
			v.restarts++
		})
		select {
		case <-time.After(backoff):
		case <-stop:
			s.update(v, func() { v.state = aosv1.ServiceState_SERVICE_STATE_STOPPED })
			return
		}
		backoff = min(2*backoff, time.Minute)
	}
}

// terminate stops a process group: SIGTERM, then SIGKILL after 10 s.
func terminate(pid int, exited <-chan error) {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-exited
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

func exitText(ps *os.ProcessState) string {
	if ps == nil {
		return ""
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return fmt.Sprintf("signal %d (%s)", int(ws.Signal()), ws.Signal())
	}
	return fmt.Sprintf("exit %d", ps.ExitCode())
}

func (s *Supervisor) update(v *svc, change func()) {
	s.mu.Lock()
	change()
	info := s.info(v)
	s.mu.Unlock()
	s.publish(&aosv1.ServiceChanged{Service: info})
}

func (s *Supervisor) publish(c *aosv1.ServiceChanged) {
	if s.Bus != nil {
		s.Bus.Publish(&aosv1.Event{Kind: &aosv1.Event_ServiceChanged{ServiceChanged: c}})
	}
}

// info describes v; the caller holds s.mu.
func (s *Supervisor) info(v *svc) *aosv1.ServiceInfo {
	d := v.def
	i := &aosv1.ServiceInfo{Name: d.Name, Command: d.Command, WorkingDir: d.Dir, Env: d.Env, Root: d.Root, Confined: !d.Root,
		Autostart: d.Autostart, Restart: restartPolicy(d.Restart), TaskId: d.TaskID, State: v.state, Pid: int32(v.pid),
		Restarts: int32(v.restarts), LastExit: v.lastExit}
	if !v.started.IsZero() && v.state == aosv1.ServiceState_SERVICE_STATE_RUNNING {
		i.StartedAt = timestamppb.New(v.started)
	}
	for _, l := range s.listeners {
		if l.Service == d.Name && !contains(i.Ports, int32(l.Port)) {
			i.Ports = append(i.Ports, int32(l.Port))
		}
	}
	return i
}

func contains(list []int32, n int32) bool {
	for _, x := range list {
		if x == n {
			return true
		}
	}
	return false
}

func restartPolicy(p string) aosv1.RestartPolicy {
	switch p {
	case Always:
		return aosv1.RestartPolicy_RESTART_POLICY_ALWAYS
	case Never:
		return aosv1.RestartPolicy_RESTART_POLICY_NEVER
	}
	return aosv1.RestartPolicy_RESTART_POLICY_ON_FAILURE
}

// Get describes one Service.
func (s *Supervisor) Get(name string) (*aosv1.ServiceInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.services[name]
	if !ok {
		return nil, fmt.Errorf("%s: %w", name, ErrNoService)
	}
	return s.info(v), nil
}

// List describes every Service, by name.
func (s *Supervisor) List() []*aosv1.ServiceInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*aosv1.ServiceInfo, 0, len(s.services))
	for _, v := range s.services {
		out = append(out, s.info(v))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Logs returns a Service's log.
func (s *Supervisor) Logs(name string) (*logBuffer, error) {
	v, err := s.find(name)
	if err != nil {
		return nil, err
	}
	return v.logs, nil
}

// ---------------------------------------------------------------- ports

// Listeners returns the programs listening on TCP ports, from the last scan.
func (s *Supervisor) Listeners() []Listener {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Listener{}, s.listeners...)
}

// Watch scans the listening ports every 2 s (PLAN.md §12) until ctx ends,
// and tells subscribers when a Service's ports change.
func (s *Supervisor) Watch(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		s.Scan()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Scan reads the listening ports now.
func (s *Supervisor) Scan() {
	found, err := scanListeners(s.procfs())
	if err != nil {
		return
	}
	s.mu.Lock()
	byLeader := map[int]string{}
	for name, v := range s.services {
		if v.pid > 0 {
			byLeader[v.pid] = name
		}
	}
	for i, l := range found {
		if name, ok := byLeader[l.PGID]; ok {
			found[i].Service = name
		} else if name, ok := byLeader[l.PID]; ok {
			found[i].Service = name
		}
	}
	before := map[string][]int32{}
	for _, v := range s.services {
		before[v.def.Name] = s.info(v).Ports
	}
	s.listeners = found
	var changed []*aosv1.ServiceInfo
	for _, v := range s.services {
		if i := s.info(v); fmt.Sprint(i.Ports) != fmt.Sprint(before[v.def.Name]) {
			changed = append(changed, i)
		}
	}
	s.mu.Unlock()
	for _, i := range changed {
		s.publish(&aosv1.ServiceChanged{Service: i})
	}
}

func (s *Supervisor) procfs() string {
	if s.Procfs != "" {
		return s.Procfs
	}
	return "/proc"
}
