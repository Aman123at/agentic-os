//go:build linux

// Command aosd is the Agentic OS daemon (PLAN.md §4.1).
package main

import (
	"context"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/amantiwari/agentic-os/internal/cli"
	"github.com/amantiwari/agentic-os/internal/config"
	"github.com/amantiwari/agentic-os/internal/daemon"
	"github.com/amantiwari/agentic-os/internal/files"
	"github.com/amantiwari/agentic-os/internal/sandbox"
	"github.com/amantiwari/agentic-os/internal/service"
	"github.com/amantiwari/agentic-os/internal/webui"
)

func main() {
	// aosd re-executes itself as the sandbox helper and as the file worker; both
	// must return before anything else happens.
	sandbox.RunHelperIfRequested()
	// The image links aos, and the Agent's rm shim, to this binary.
	if name := filepath.Base(os.Args[0]); name == "aos" || name == "rm" {
		os.Exit(cli.Main())
	}
	if len(os.Args) > 1 && os.Args[1] == files.WorkerArg {
		os.Exit(files.WorkerMain())
	}
	if len(os.Args) > 1 && os.Args[1] == service.SocketsArg {
		os.Exit(service.SocketsMain())
	}
	log.SetFlags(0)
	log.SetPrefix("aosd: ")

	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	if cfg.Mode == "ui" && cfg.ImageMode == "cli" {
		log.Fatal("AOS_MODE=ui, but this image was built for cli Mode. Rebuild it: docker compose up --build")
	}
	if cfg.Bind != "127.0.0.1" && cfg.Bind != "localhost" {
		log.Printf("WARNING: AOS_BIND=%s publishes port %s beyond this computer", cfg.Bind, cfg.HostPort)
	}
	var assets fs.FS
	if cfg.Mode == "ui" {
		assets = webui.Assets()
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err := daemon.Run(ctx, cfg, assets); err != nil {
		log.Fatal(err)
	}
}
