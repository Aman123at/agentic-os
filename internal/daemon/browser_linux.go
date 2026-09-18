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
		return false, "INCLUDE_BROWSER=true, but this image was built without the browser. Rebuild it: docker compose up --build"
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
		Downloads: filepath.Join(d.layout.Home, "Downloads"),
		Logf:      log.Printf,
	}
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

// startBrowser launches the headless shell as aos, confined like an Agent, so
// Protected Paths stay out of its reach. DevTools runs over fds 3 and 4.
func (d *Daemon) startBrowser() (*browser.Process, error) {
	rs, err := d.plan(d.agentPolicy())
	if err != nil {
		return nil, err
	}
	// ~/.config is a Protected dotfile, so the profile and everything Chromium
	// would put under XDG folders lives in one writable place.
	profile := filepath.Join(d.layout.Home, ".local", "share", "aos-browser")
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
