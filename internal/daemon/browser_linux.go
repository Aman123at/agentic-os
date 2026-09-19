package daemon

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"syscall"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/browser"
	"github.com/Aman123at/agentic-os/internal/sandbox"
	"github.com/Aman123at/agentic-os/internal/tool"
)

// browserStatus says whether the Browser app can run (PLAN.md M5.2) and, when
// INCLUDE_BROWSER=true but it can't, why.
func (d *Daemon) browserStatus() (ok bool, why string) {
	if !d.cfg.IncludeBrowser || d.cfg.Mode != "ui" {
		return false, ""
	}
	if _, err := os.Stat(browser.Binary); err != nil {
		return false, "the Browser is turned on, but its headless shell isn't installed. Install it: sudo aos browser install"
	}
	return true, ""
}

// newBrowser is the Browser app's page, or nil when it isn't included.
func (d *Daemon) newBrowser() *browser.Manager {
	if d.cfg.IncludeBrowser && d.cfg.Mode != "ui" {
		log.Printf("WARNING: INCLUDE_BROWSER=true is ignored in %s Mode; the Browser is a Desktop app", d.cfg.Mode)
	}
	ok, why := d.browserStatus()
	if why != "" {
		log.Printf("WARNING: %s", why)
	}
	if !ok {
		return nil
	}
	return &browser.Manager{
		Start:     d.startBrowser,
		Policy:    browser.Policy{AosdPort: Port},
		Downloads: d.browserDownloads(),
		Logf:      log.Printf,
	}
}

// browserProfile is the one writable place for everything Chromium keeps: the
// user-data-dir plus the XDG folders redirected into it, since ~/.config is a
// Protected dotfile.
func (d *Daemon) browserProfile() string {
	return filepath.Join(d.layout.Home, ".local", "share", "aos-browser")
}

// browserDownloads is where the Browser saves downloads.
func (d *Daemon) browserDownloads() string {
	return filepath.Join(d.layout.Home, "Downloads")
}

// browserPolicy confines the streamed Browser more tightly than an Agent
// (ADR-0008, M6.7). It reads the Machine like an Agent, but may write only its
// own profile and ~/Downloads (plus the runtime scratch every process needs), so
// M6.8's widening of the Agent policy to all of `/` never hands an unsandboxed
// Chromium write access to the server.
func (d *Daemon) browserPolicy() sandbox.Policy {
	p := d.agentPolicy()
	p.Writable = []string{d.browserProfile(), d.browserDownloads(), "/tmp", "/var/tmp", "/dev"}
	return p
}

// ensureBrowserDirs creates the Browser's profile and Downloads folders, owned by
// aos, before planning: browserPolicy grants write only to paths that already
// exist (Plan skips missing Writable roots), and a confined Chromium cannot
// create them itself.
func (d *Daemon) ensureBrowserDirs() error {
	for _, dir := range []string{d.browserProfile(), d.browserDownloads()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.Chown(dir, int(d.uid), int(d.gid)); err != nil {
			return err
		}
	}
	return nil
}

// agentBrowser gives a Task's Agent the Browser's page (PLAN.md M5.3); the
// Task's release ends its lease.
func (d *Daemon) agentBrowser(taskID string) *tool.Browser {
	return &tool.Browser{
		Page: func(ctx context.Context) (tool.BrowserPage, error) {
			a, err := d.browser.Agent(ctx, taskID)
			if err != nil {
				return nil, err
			}
			return a, nil
		},
		Show: func(context.Context) {
			d.bus.Publish(&aosv1.Event{Kind: &aosv1.Event_OpenInDesktop{OpenInDesktop: &aosv1.OpenInDesktop{TaskId: taskID, App: "browser"}}})
		},
	}
}

// startBrowser launches the headless shell as aos, confined by its own narrow
// ruleset (browserPolicy) so it may write only its profile and ~/Downloads.
// DevTools runs over fds 3 and 4.
func (d *Daemon) startBrowser() (*browser.Process, error) {
	if err := d.ensureBrowserDirs(); err != nil {
		return nil, err
	}
	rs, err := d.plan(d.browserPolicy())
	if err != nil {
		return nil, err
	}
	// ~/.config is a Protected dotfile, so the profile and everything Chromium
	// would put under XDG folders lives in one writable place.
	profile := d.browserProfile()
	env := append(d.userEnv(), "XDG_CONFIG_HOME="+profile+"/config", "XDG_CACHE_HOME="+profile+"/cache",
		"LD_LIBRARY_PATH="+browser.LibDir)
	argv := append([]string{browser.Binary, "--user-data-dir=" + profile}, browser.Flags...)
	cmd, err := sandbox.Command(rs, d.uid, d.gid, env, argv...)
	if err != nil {
		return nil, err
	}
	cmd.Dir = d.layout.Home
	// Its own process group, so Stop takes the renderers and helpers too.
	cmd.SysProcAttr.Setpgid = true

	toBrowser, commands, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	replies, fromBrowser, err := os.Pipe()
	if err != nil {
		_ = toBrowser.Close()
		_ = commands.Close()
		return nil, err
	}
	cmd.ExtraFiles = []*os.File{toBrowser, fromBrowser}
	err = cmd.Start()
	_ = toBrowser.Close()
	_ = fromBrowser.Close()
	if err != nil {
		_ = commands.Close()
		_ = replies.Close()
		return nil, errors.New("couldn't run the browser: " + err.Error())
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		_ = commands.Close()
		_ = replies.Close()
		close(done)
	}()
	pgid := cmd.Process.Pid
	return &browser.Process{In: commands, Out: replies, Done: done, Stop: func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-done
	}}, nil
}
