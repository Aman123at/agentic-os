// Package browser runs the page behind the Desktop's Browser app (PLAN.md
// M5.2, ADR-0008). A Chromium headless shell renders it inside the Machine and
// streams its frames over the DevTools protocol; viewers get those frames and
// send input back. There is no X server, window manager or VNC.
//
// The browser starts when the first viewer attaches and stops once none has
// been attached for Manager.Idle. Every viewer sees the same page.
package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
)

// Binary is where the image installs the headless shell (docker/Dockerfile).
const Binary = "/opt/aos-browser/chrome"

// LibDir holds libraries the image keeps beside the browser rather than
// installing their packages (libgbm, whose package pulls in Mesa); it goes on
// the browser's LD_LIBRARY_PATH.
const LibDir = "/opt/aos-browser/lib"

// Flags are the headless shell's flags, besides --user-data-dir. The pipe
// keeps DevTools off the network, so nothing else in the Machine can drive the
// browser. --no-sandbox: the container is the sandbox, and aosd confines the
// browser with Landlock like an Agent (Chromium's own sandbox needs user
// namespaces, which Docker's default seccomp profile refuses).
var Flags = []string{
	"--remote-debugging-pipe",
	"--no-sandbox",
	"--no-first-run",
	"--no-default-browser-check",
	"--disable-dev-shm-usage",
	"--disable-background-networking",
	"--disable-component-update",
	"--disable-sync",
	"--disable-extensions",
	"--disable-breakpad",
	"--disable-features=Translate,MediaRouter,OptimizationHints",
	"--password-store=basic",
	"--mute-audio",
	"--force-color-profile=srgb",
	Blank,
}

// errorPage is where Chromium shows a page that failed to load.
const errorPage = "chrome-error://"

// ErrClosed is returned once the browser has stopped.
var ErrClosed = errors.New("the browser stopped")

// Process is a running browser: its DevTools pipe and a way to stop it.
type Process struct {
	In   io.WriteCloser // commands to the browser
	Out  io.ReadCloser  // replies and events from it
	Done <-chan struct{}
	// Stop kills the browser and everything it started, then waits for Done.
	Stop func()
}

// Manager owns the one browser page.
type Manager struct {
	// Start launches the browser.
	Start func() (*Process, error)
	// Policy decides which pages load.
	Policy Policy
	// Downloads is where downloads are saved.
	Downloads string
	// Idle is how long the browser outlives its last viewer; 0 means 5 minutes.
	Idle time.Duration
	// AgentIdle is how long an Agent keeps the page without using it; 0 means
	// 2 minutes.
	AgentIdle time.Duration
	// Logf logs; nil discards.
	Logf func(format string, args ...any)

	mu    sync.Mutex
	page  *Page
	timer *time.Timer
	lease *lease
}

// Attach connects a viewer, starting the browser if it isn't running.
func (m *Manager) Attach(ctx context.Context) (*Viewer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.attach(ctx, true)
}

// attach connects a viewer; m.mu is held.
func (m *Manager) attach(ctx context.Context, visible bool) (*Viewer, error) {
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	if m.page == nil || m.page.isClosed() {
		p, err := m.open(ctx)
		if err != nil {
			return nil, err
		}
		m.page = p
	}
	return m.page.subscribe(visible), nil
}

// Detach disconnects a viewer; the browser stops after Idle without viewers.
func (m *Manager) Detach(v *Viewer) {
	left := v.page.unsubscribe(v)
	m.mu.Lock()
	defer m.mu.Unlock()
	if left > 0 || m.page != v.page {
		return
	}
	idle := m.Idle
	if idle <= 0 {
		idle = 5 * time.Minute
	}
	page := m.page
	if m.timer != nil {
		m.timer.Stop()
	}
	m.timer = time.AfterFunc(idle, func() {
		m.mu.Lock()
		if m.page != page || page.count() > 0 {
			m.mu.Unlock()
			return
		}
		m.page, m.timer = nil, nil
		m.mu.Unlock()
		page.close()
	})
}

// Running reports whether the browser is running.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.page != nil && !m.page.isClosed()
}

// Close stops the browser.
func (m *Manager) Close() {
	m.mu.Lock()
	page := m.page
	m.page = nil
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	if m.lease != nil {
		m.lease.timer.Stop()
		m.lease = nil
	}
	m.mu.Unlock()
	if page != nil {
		page.close()
	}
}

func (m *Manager) logf(format string, args ...any) {
	if m.Logf != nil {
		m.Logf(format, args...)
	}
}

// open starts the browser and opens the page every viewer shares.
func (m *Manager) open(ctx context.Context) (*Page, error) {
	proc, err := m.Start()
	if err != nil {
		return nil, err
	}
	p := &Page{proc: proc, policy: m.Policy, logf: m.logf, subs: map[*Viewer]bool{}, handled: map[string]bool{},
		events: make(chan event, 256), closed: make(chan struct{}), loadCh: make(chan struct{}), width: 1024, height: 700, scale: 1,
		state: State{URL: Blank}}
	p.c = newConn(proc.In, proc.Out, p.enqueue)
	go func() {
		select {
		case <-proc.Done:
		case <-p.c.done:
		}
		p.shutdown()
	}()
	go p.loop()

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := p.init(ctx, m.Downloads); err != nil {
		p.close()
		return nil, errors.New("the browser didn't start: " + err.Error())
	}
	return p, nil
}

// State is what the Browser's toolbar shows.
type State struct {
	URL        string `json:"url"`
	Title      string `json:"title"`
	Loading    bool   `json:"loading"`
	CanBack    bool   `json:"canBack"`
	CanForward bool   `json:"canForward"`
	// Agent is the Task whose Agent is using the page, if any.
	Agent string `json:"agent,omitempty"`
}

type event struct {
	session, method string
	params          json.RawMessage
}

// Page is the browser's one page and the viewers watching it.
type Page struct {
	proc    *Process
	c       *conn
	policy  Policy
	logf    func(string, ...any)
	events  chan event
	closed  chan struct{}
	once    sync.Once
	session string
	target  string

	// castMu orders the screencast's start, stop and resize calls.
	castMu  sync.Mutex
	casting bool
	castW   int
	castH   int

	mu            sync.Mutex
	subs          map[*Viewer]bool
	state         State
	frame         []byte
	pendingAck    int
	acked         bool
	width, height int
	scale         float64
	handled       map[string]bool
	// starts counts main-frame loads; loadCh is closed and replaced whenever a
	// load starts or stops, waking whoever waits for one (Agent.settle).
	starts int
	loadCh chan struct{}
}

func (p *Page) init(ctx context.Context, downloads string) error {
	if downloads != "" {
		if err := p.c.call(ctx, "", "Browser.setDownloadBehavior", map[string]any{"behavior": "allow", "downloadPath": downloads}, nil); err != nil {
			p.logf("browser: downloads: %v", err)
		}
	}
	if err := p.c.call(ctx, "", "Target.setDiscoverTargets", map[string]any{"discover": true}, nil); err != nil {
		return err
	}
	var t struct {
		TargetID string `json:"targetId"`
	}
	if err := p.c.call(ctx, "", "Target.createTarget", map[string]any{"url": Blank}, &t); err != nil {
		return err
	}
	var s struct {
		SessionID string `json:"sessionId"`
	}
	if err := p.c.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": t.TargetID, "flatten": true}, &s); err != nil {
		return err
	}
	p.mu.Lock()
	p.target, p.session = t.TargetID, s.SessionID
	w, h, scale := p.width, p.height, p.scale
	p.mu.Unlock()
	if err := p.c.call(ctx, s.SessionID, "Page.enable", nil, nil); err != nil {
		return err
	}
	return p.metrics(ctx, w, h, scale)
}

func (p *Page) metrics(ctx context.Context, w, h int, scale float64) error {
	return p.c.call(ctx, p.session, "Emulation.setDeviceMetricsOverride",
		map[string]any{"width": w, "height": h, "deviceScaleFactor": scale, "mobile": false}, nil)
}

// enqueue runs on the pipe's reader: it only hands the event on.
func (p *Page) enqueue(session, method string, params json.RawMessage) {
	select {
	case p.events <- event{session, method, params}:
	case <-p.closed:
	}
}

func (p *Page) isClosed() bool {
	select {
	case <-p.closed:
		return true
	default:
		return false
	}
}

// shutdown marks the page closed and wakes every viewer.
func (p *Page) shutdown() {
	p.once.Do(func() {
		close(p.closed)
		p.mu.Lock()
		for v := range p.subs {
			v.wake()
		}
		p.mu.Unlock()
	})
}

// close stops the browser: politely first, then by force.
func (p *Page) close() {
	if !p.isClosed() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = p.c.call(ctx, "", "Browser.close", nil, nil)
		cancel()
	}
	p.shutdown()
	p.proc.Stop()
}

// async runs a DevTools call without waiting for it, logging a failure.
func (p *Page) async(method string, params any) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := p.c.call(ctx, p.session, method, params, nil); err != nil && !p.isClosed() {
			p.logf("browser: %v", err)
		}
	}()
}

func (p *Page) loop() {
	for {
		select {
		case <-p.closed:
			return
		case ev := <-p.events:
			p.handle(ev)
		}
	}
}

func (p *Page) handle(ev event) {
	p.mu.Lock()
	session, target := p.session, p.target
	mine := ev.session != "" && ev.session == session
	p.mu.Unlock()
	switch ev.method {
	case "Page.screencastFrame":
		if !mine {
			return
		}
		var f struct {
			Data      string `json:"data"`
			SessionID int    `json:"sessionId"`
		}
		if json.Unmarshal(ev.params, &f) != nil {
			return
		}
		img, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			return
		}
		p.mu.Lock()
		p.frame, p.pendingAck, p.acked = img, f.SessionID, false
		watched := false
		for v := range p.subs {
			if v.isVisible() {
				v.setFrame(img)
				watched = true
			}
		}
		p.mu.Unlock()
		if !watched {
			p.ack()
		}
	case "Page.frameNavigated":
		var n struct {
			Frame struct {
				ID       string `json:"id"`
				ParentID string `json:"parentId"`
				URL      string `json:"url"`
			} `json:"frame"`
		}
		if !mine || json.Unmarshal(ev.params, &n) != nil || n.Frame.ParentID != "" {
			return
		}
		p.navigated(n.Frame.URL)
	case "Page.navigatedWithinDocument":
		var n struct {
			FrameID string `json:"frameId"`
			URL     string `json:"url"`
		}
		if !mine || json.Unmarshal(ev.params, &n) != nil || n.FrameID != target {
			return
		}
		p.navigated(n.URL)
	case "Page.frameStartedLoading", "Page.frameStoppedLoading":
		var n struct {
			FrameID string `json:"frameId"`
		}
		if !mine || json.Unmarshal(ev.params, &n) != nil || n.FrameID != target {
			return
		}
		loading := ev.method == "Page.frameStartedLoading"
		p.update(func(s *State) { s.Loading = loading })
		p.loaded(loading)
		if !loading {
			go p.refreshHistory()
		}
	case "Page.javascriptDialogOpening":
		// A dialog nobody answers would freeze the page; the Desktop shows the
		// message instead. Only alerts and leave-page prompts are accepted.
		var d struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if !mine || json.Unmarshal(ev.params, &d) != nil {
			return
		}
		p.async("Page.handleJavaScriptDialog", map[string]any{"accept": d.Type == "alert" || d.Type == "beforeunload"})
		if d.Type != "beforeunload" && d.Message != "" {
			p.notice("The page says: " + limit(d.Message, 300))
		}
	case "Target.targetCreated", "Target.targetInfoChanged":
		var t struct {
			TargetInfo struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
				Title    string `json:"title"`
				URL      string `json:"url"`
			} `json:"targetInfo"`
		}
		if json.Unmarshal(ev.params, &t) != nil {
			return
		}
		ti := t.TargetInfo
		if ti.TargetID == target {
			title := limit(ti.Title, 300)
			p.update(func(s *State) { s.Title = title })
			return
		}
		if ti.Type == "page" && ti.URL != "" && ti.URL != Blank {
			p.popup(ti.TargetID, ti.URL)
		}
	case "Target.targetCrashed":
		var t struct {
			TargetID string `json:"targetId"`
		}
		if json.Unmarshal(ev.params, &t) == nil && t.TargetID == target {
			p.notice("The page crashed. Reload to try again.")
		}
	case "Target.detachedFromTarget":
		var t struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(ev.params, &t) == nil && t.SessionID != "" && t.SessionID == session {
			p.shutdown()
		}
	}
}

// navigated records the page's new address, or turns away one the policy
// refuses (a link or redirect the address bar never saw).
func (p *Page) navigated(url string) {
	// A page that failed to load shows Chromium's own error page, which says
	// why; the address stays the one that failed.
	if strings.HasPrefix(url, errorPage) {
		return
	}
	if err := p.policy.Check(url); err != nil {
		p.notice(err.Error())
		p.async("Page.navigate", map[string]any{"url": Blank})
		return
	}
	p.update(func(s *State) { s.URL = url })
	go p.refreshHistory()
}

// popup handles a page that opened a new window: the Browser has one page, so
// the popup closes and its address opens here instead.
func (p *Page) popup(targetID, url string) {
	p.mu.Lock()
	seen := p.handled[targetID]
	p.handled[targetID] = true
	p.mu.Unlock()
	if seen {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = p.c.call(ctx, "", "Target.closeTarget", map[string]any{"targetId": targetID}, nil)
	}()
	if err := p.policy.Check(url); err != nil {
		p.notice(err.Error())
		return
	}
	p.async("Page.navigate", map[string]any{"url": url})
}

func (p *Page) refreshHistory() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var h struct {
		CurrentIndex int `json:"currentIndex"`
		Entries      []struct {
			ID    int    `json:"id"`
			Title string `json:"title"`
		} `json:"entries"`
	}
	if p.c.call(ctx, p.session, "Page.getNavigationHistory", nil, &h) != nil {
		return
	}
	// The entry's title is the document's once it has loaded; Target events
	// don't always report a title change.
	var title string
	if h.CurrentIndex >= 0 && h.CurrentIndex < len(h.Entries) {
		title = limit(h.Entries[h.CurrentIndex].Title, 300)
	}
	p.update(func(s *State) {
		s.CanBack = h.CurrentIndex > 0
		s.CanForward = h.CurrentIndex < len(h.Entries)-1
		if title != "" {
			s.Title = title
		}
	})
}

// update changes the toolbar state and tells every viewer.
func (p *Page) update(f func(*State)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	old := p.state
	f(&p.state)
	if p.state == old {
		return
	}
	for v := range p.subs {
		v.setState(p.state)
	}
}

// notice tells every viewer something the user should read.
func (p *Page) notice(text string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for v := range p.subs {
		v.setNotice(text)
	}
}

// ack asks for the next frame, once per frame.
func (p *Page) ack() {
	p.mu.Lock()
	if p.acked {
		p.mu.Unlock()
		return
	}
	p.acked = true
	id := p.pendingAck
	p.mu.Unlock()
	p.async("Page.screencastFrameAck", map[string]any{"sessionId": id})
}

// loaded records that a load started or stopped.
func (p *Page) loaded(started bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if started {
		p.starts++
	}
	close(p.loadCh)
	p.loadCh = make(chan struct{})
}

// subscribe adds a viewer; a hidden one (an Agent's) gets no frames.
func (p *Page) subscribe(visible bool) *Viewer {
	v := &Viewer{page: p, notify: make(chan struct{}, 1), visible: visible}
	p.mu.Lock()
	p.subs[v] = true
	v.setState(p.state)
	if p.frame != nil {
		v.setFrame(p.frame)
	}
	p.mu.Unlock()
	go p.cast()
	return v
}

func (p *Page) unsubscribe(v *Viewer) int {
	p.mu.Lock()
	delete(p.subs, v)
	n := len(p.subs)
	p.mu.Unlock()
	go p.cast()
	return n
}

func (p *Page) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.subs)
}

// cast streams while some viewer can see the page, at the current size.
func (p *Page) cast() {
	p.castMu.Lock()
	defer p.castMu.Unlock()
	if p.isClosed() {
		return
	}
	p.mu.Lock()
	want := false
	for v := range p.subs {
		want = want || v.isVisible()
	}
	w, h := int(float64(p.width)*p.scale), int(float64(p.height)*p.scale)
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if p.casting && (!want || w != p.castW || h != p.castH) {
		_ = p.c.call(ctx, p.session, "Page.stopScreencast", nil, nil)
		p.casting = false
	}
	if want && !p.casting {
		err := p.c.call(ctx, p.session, "Page.startScreencast",
			map[string]any{"format": "jpeg", "quality": 75, "maxWidth": w, "maxHeight": h, "everyNthFrame": 1}, nil)
		if err != nil {
			p.logf("browser: screencast: %v", err)
			return
		}
		p.casting, p.castW, p.castH = true, w, h
	}
}

// Viewer is one Browser window watching the page.
type Viewer struct {
	page   *Page
	notify chan struct{}

	mu      sync.Mutex
	visible bool
	frame   []byte
	state   *State
	notices []string
}

// Update is what a viewer has to show: any of a new frame, new toolbar state
// and notices.
type Update struct {
	Frame   []byte
	State   *State
	Notices []string
}

// Next waits for the next Update.
func (v *Viewer) Next(ctx context.Context) (Update, error) {
	for {
		v.mu.Lock()
		u := Update{Frame: v.frame, State: v.state, Notices: v.notices}
		v.frame, v.state, v.notices = nil, nil, nil
		v.mu.Unlock()
		if u.Frame != nil || u.State != nil || len(u.Notices) > 0 {
			return u, nil
		}
		select {
		case <-v.notify:
		case <-v.page.closed:
			return Update{}, ErrClosed
		case <-ctx.Done():
			return Update{}, ctx.Err()
		}
	}
}

// FrameSent says this viewer has shown the last frame, so the next may come.
func (v *Viewer) FrameSent() { v.page.ack() }

// Do carries out one Command from this viewer.
func (v *Viewer) Do(ctx context.Context, c Command) error {
	p := v.page
	if p.isClosed() {
		return ErrClosed
	}
	switch c.Type {
	case "navigate":
		_, err := p.navigate(ctx, c.URL)
		return err
	case "back", "forward":
		_, err := p.history(ctx, c.Type == "forward")
		return err
	case "reload":
		return p.c.call(ctx, p.session, "Page.reload", nil, nil)
	case "stop":
		return p.c.call(ctx, p.session, "Page.stopLoading", nil, nil)
	case "resize":
		w, h, scale, err := viewport(c)
		if err != nil {
			return err
		}
		p.mu.Lock()
		same := w == p.width && h == p.height && scale == p.scale
		p.width, p.height, p.scale = w, h, scale
		p.mu.Unlock()
		if same {
			return nil
		}
		if err := p.metrics(ctx, w, h, scale); err != nil {
			return err
		}
		p.cast()
		return nil
	case "visible":
		v.mu.Lock()
		changed := v.visible != c.Visible
		v.visible = c.Visible
		v.mu.Unlock()
		if changed {
			if c.Visible {
				p.mu.Lock()
				if p.frame != nil {
					v.setFrame(p.frame)
				}
				p.mu.Unlock()
			}
			p.cast()
		}
		return nil
	}
	method, params, err := inputCall(c)
	if err != nil {
		return err
	}
	return p.c.call(ctx, p.session, method, params, nil)
}

// navigate opens what the user (or an Agent) typed, as the address bar does.
// It reports whether a new document is loading.
func (p *Page) navigate(ctx context.Context, input string) (loading bool, err error) {
	u, err := p.policy.Normalize(input)
	if err != nil {
		return false, err
	}
	var r struct {
		LoaderID  string `json:"loaderId"`
		ErrorText string `json:"errorText"`
	}
	p.update(func(s *State) { s.URL = u })
	if err := p.c.call(ctx, p.session, "Page.navigate", map[string]any{"url": u}, &r); err != nil {
		return false, err
	}
	if r.ErrorText != "" && r.ErrorText != "net::ERR_ABORTED" {
		return false, errors.New("couldn't open " + u + ": " + r.ErrorText)
	}
	return r.LoaderID != "", nil
}

// history goes one entry back or forward; moved is false at either end.
func (p *Page) history(ctx context.Context, forward bool) (moved bool, err error) {
	var h struct {
		CurrentIndex int `json:"currentIndex"`
		Entries      []struct {
			ID int `json:"id"`
		} `json:"entries"`
	}
	if err := p.c.call(ctx, p.session, "Page.getNavigationHistory", nil, &h); err != nil {
		return false, err
	}
	i := h.CurrentIndex - 1
	if forward {
		i = h.CurrentIndex + 1
	}
	if i < 0 || i >= len(h.Entries) {
		return false, nil
	}
	return true, p.c.call(ctx, p.session, "Page.navigateToHistoryEntry", map[string]any{"entryId": h.Entries[i].ID}, nil)
}

func (v *Viewer) isVisible() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.visible
}

func (v *Viewer) setFrame(b []byte) {
	v.mu.Lock()
	v.frame = b
	v.mu.Unlock()
	v.wake()
}

func (v *Viewer) setState(s State) {
	v.mu.Lock()
	v.state = &s
	v.mu.Unlock()
	v.wake()
}

func (v *Viewer) setNotice(text string) {
	v.mu.Lock()
	if len(v.notices) < 8 {
		v.notices = append(v.notices, text)
	}
	v.mu.Unlock()
	v.wake()
}

// Notice shows text to this viewer only.
func (v *Viewer) Notice(text string) { v.setNotice(text) }

func (v *Viewer) wake() {
	select {
	case v.notify <- struct{}{}:
	default:
	}
}
