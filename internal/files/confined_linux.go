package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/amantiwari/agentic-os/internal/sandbox"
)

// WorkerArg is the argument that turns aosd into a file worker (`aosd __files`).
const WorkerArg = "__files"

// Confined runs each operation in a new worker process as the aos user, confined
// to a Ruleset (ADR-0004): a symlink an Agent plants can never turn a Files Tool
// into root file access.
type Confined struct {
	Ops      Ops
	UID, GID uint32
	// Ruleset returns the ruleset for the next operation.
	Ruleset func() (sandbox.Ruleset, error)
	// Exe is the aosd binary; Env is the worker's environment.
	Exe string
	Env []string
}

// Run implements Runner.
func (c Confined) Run(ctx context.Context, op string, args, result any, progress func(n, total int64)) error {
	rs, err := c.Ruleset()
	if err != nil {
		return err
	}
	cmd, err := sandbox.Command(rs, c.UID, c.GID, c.Env, c.Exe, WorkerArg)
	if err != nil {
		return err
	}
	return runOp(ctx, cmd, c.Ops, op, args, result, progress)
}

// AsUser runs each operation in a worker process as the aos user, unconfined:
// the user's own file operations from the Desktop and the CLI. It also streams
// file bytes for the Desktop's downloads, media and uploads (Streamer).
type AsUser struct {
	Ops      Ops
	UID, GID uint32
	Exe      string
	Env      []string
}

func (u AsUser) command() *exec.Cmd {
	cmd := exec.Command(u.Exe, WorkerArg)
	cmd.Env = u.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: u.UID, Gid: u.GID, Groups: []uint32{}}}
	return cmd
}

// Run implements Runner.
func (u AsUser) Run(ctx context.Context, op string, args, result any, progress func(n, total int64)) error {
	return runOp(ctx, u.command(), u.Ops, op, args, result, progress)
}

// ReadStream implements Streamer.
func (u AsUser) ReadStream(ctx context.Context, a StreamArgs, header func(StreamHeader) error, w io.Writer) error {
	in, err := StreamRequest(u.Ops, OpReadStream, a, nil)
	if err != nil {
		return err
	}
	return runWorker(ctx, u.command(), in, func(out io.Reader) error { return ReadStreamResponse(out, header, w) })
}

// WriteStream implements Streamer.
func (u AsUser) WriteStream(ctx context.Context, a WriteStreamArgs, r io.Reader) (int64, error) {
	in, err := StreamRequest(u.Ops, OpWriteStream, a, r)
	if err != nil {
		return 0, err
	}
	var n int64
	err = runWorker(ctx, u.command(), in, func(out io.Reader) error { return ReadResponse(out, &n, nil) })
	return n, err
}

// runOp runs one JSON operation in a worker.
func runOp(ctx context.Context, cmd *exec.Cmd, ops Ops, op string, args, result any, progress func(n, total int64)) error {
	var req bytes.Buffer
	if err := WriteRequest(&req, ops, op, args); err != nil {
		return err
	}
	return runWorker(ctx, cmd, &req, func(out io.Reader) error { return ReadResponse(out, result, progress) })
}

// runWorker runs a worker process: stdin is what it reads, and read consumes
// its output. When read stops early (a Desktop that went away mid-download),
// the worker is killed rather than left blocked writing to a full pipe.
func runWorker(ctx context.Context, cmd *exec.Cmd, stdin io.Reader, read func(io.Reader) error) error {
	cmd.Stdin = stdin
	cmd.Dir = "/"
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	stopKill := context.AfterFunc(ctx, func() { _ = cmd.Process.Kill() })
	defer stopKill()
	readErr := read(stdout)
	if readErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if readErr != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" && !errors.Is(readErr, ErrExists) && errorKind(readErr) == "" {
			return fmt.Errorf("%w: %s", readErr, msg)
		}
		return readErr
	}
	if waitErr != nil {
		return fmt.Errorf("file worker: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// WorkerMain is the body of `aosd __files`: one operation from stdin to stdout.
func WorkerMain() int {
	if err := Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "file worker:", err)
		return 1
	}
	return 0
}
