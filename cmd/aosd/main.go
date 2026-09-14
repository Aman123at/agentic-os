//go:build linux

// Command aosd is the Agentic OS daemon (PLAN.md §4.1). M0 serves only the
// Desktop placeholder and Service forwarding; the API arrives in M1.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"syscall"
	"time"

	"github.com/amantiwari/agentic-os/internal/proxy"
	"github.com/amantiwari/agentic-os/internal/sandbox"
	"github.com/amantiwari/agentic-os/internal/webui"
)

const port = 7700

func main() {
	sandbox.RunHelperIfRequested()
	log.SetFlags(0)
	log.SetPrefix("aosd: ")

	image, mode := os.Getenv("AOS_IMAGE_MODE"), os.Getenv("AOS_MODE")
	if mode == "" {
		mode = image
	}
	if mode == "ui" && image == "cli" {
		log.Fatal("AOS_MODE=ui, but this image was built for cli Mode. Rebuild it: docker compose up --build")
	}
	if bind := os.Getenv("AOS_BIND"); bind != "" && bind != "127.0.0.1" {
		log.Printf("WARNING: AOS_BIND=%s publishes port %d beyond this computer", bind, port)
	}

	if err := prepareHome(); err != nil {
		log.Fatalf("preparing the home folder: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok\n") })
	if assets := webui.Assets(); assets != nil && mode == "ui" {
		mux.Handle("GET /", http.FileServerFS(assets))
	} else {
		mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "Agentic OS is running in cli Mode: docker compose exec aos aos\n")
		})
	}

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           proxy.New(mux, port),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	hostPort := os.Getenv("AOS_PORT")
	if hostPort == "" {
		hostPort = "7700"
	}
	log.Printf("%s Mode, listening on :%d (on the Host: http://localhost:%s)", mode, port, hostPort)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// prepareHome arranges the Protected dotfiles and ~/Shared (PLAN.md §7.2).
func prepareHome() error {
	u, err := user.Lookup("aos")
	if err != nil {
		return err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	notes, err := sandbox.PrepareHome(sandbox.DefaultLayout(), uid, gid)
	for _, n := range notes {
		log.Print(n)
	}
	return err
}
