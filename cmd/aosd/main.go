//go:build linux

// Command aosd is the Agentic OS daemon (PLAN.md §4.1).
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Aman123at/agentic-os/internal/cli"
	"github.com/Aman123at/agentic-os/internal/config"
	"github.com/Aman123at/agentic-os/internal/daemon"
	"github.com/Aman123at/agentic-os/internal/files"
	"github.com/Aman123at/agentic-os/internal/sandbox"
	"github.com/Aman123at/agentic-os/internal/service"
	"github.com/Aman123at/agentic-os/internal/webui"
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
	if cfg.Bind != "127.0.0.1" && cfg.Bind != "localhost" {
		log.Printf("WARNING: AOS_BIND=%s publishes port %s beyond this computer", cfg.Bind, cfg.HostPort)
	}
	// One image always compiles the embed (M6.10); the Mode in force is decided
	// by config.yml inside daemon.Run, which serves these only in ui Mode. nil
	// here means the Desktop was never built into this binary.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err := daemon.Run(ctx, cfg, webui.Assets()); err != nil {
		log.Fatal(err)
	}
}
