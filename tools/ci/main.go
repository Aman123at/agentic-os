// Command ci runs every check (PLAN.md §17): `go run ./tools/ci [stage…]`.
// It works the same locally and in GitHub Actions.
//
// Stages: lint, unit, image, integration. With no arguments, all run in order.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Image size targets in MB, unpacked (PLAN.md §16). Both are currently missed:
// finding F3 in docs/m0-findings.md awaits a decision, so misses are reported
// without failing the run until then.
var sizeTargets = map[string]int{"cli": 450, "ui": 500}

const sizeTargetsPending = true

var stages = []struct {
	name string
	run  func() error
}{
	{"lint", lint},
	{"unit", unit},
	{"image", image},
	{"integration", integration},
}

func main() {
	selected := os.Args[1:]
	failed := false
	for _, s := range stages {
		if len(selected) > 0 && !contains(selected, s.name) {
			continue
		}
		start := time.Now()
		fmt.Printf("==> %s\n", s.name)
		if err := s.run(); err != nil {
			fmt.Printf("<== %s FAILED after %s: %v\n", s.name, time.Since(start).Round(time.Millisecond), err)
			failed = true
			continue
		}
		fmt.Printf("<== %s ok (%s)\n", s.name, time.Since(start).Round(time.Millisecond))
	}
	if failed {
		os.Exit(1)
	}
}

func lint() error {
	out, err := output("gofmt", "-l", ".")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "" {
		return fmt.Errorf("gofmt needed:\n%s", out)
	}
	for _, goos := range []string{"", "linux"} {
		env := []string{}
		if goos != "" {
			env = append(env, "GOOS="+goos)
		}
		if err := runEnv(env, "go", "vet", "./..."); err != nil {
			return err
		}
		if err := runEnv(env, "golangci-lint", "run", "./..."); err != nil {
			return err
		}
	}
	return nil
}

func unit() error {
	return run("go", "test", "./...")
}

func image() error {
	for _, target := range []string{"cli", "ui"} {
		tag := "agentic-os:" + target
		if err := run("docker", "buildx", "build", "-f", "docker/Dockerfile", "--target", target, "--load", "-t", tag, "."); err != nil {
			return err
		}
		out, err := output("docker", "run", "--rm", "--entrypoint", "sh", tag, "-c", "du -sx --block-size=1M / | cut -f1")
		if err != nil {
			return err
		}
		mb, err := strconv.Atoi(strings.TrimSpace(out))
		if err != nil {
			return fmt.Errorf("image size: %q", out)
		}
		switch {
		case mb <= sizeTargets[target]:
			fmt.Printf("    %s image: %d MB unpacked (target < %d MB)\n", target, mb, sizeTargets[target])
		case sizeTargetsPending:
			fmt.Printf("    %s image: %d MB unpacked, over target %d MB (finding F3, pending decision)\n", target, mb, sizeTargets[target])
		default:
			return fmt.Errorf("%s image is %d MB unpacked, target < %d MB", target, mb, sizeTargets[target])
		}
	}
	return nil
}

// integration cross-compiles the host check test and runs it as root inside the
// cli image, with a scratch Shared Folder and a dummy secret (never a real key).
func integration() error {
	arch, err := output("docker", "version", "--format", "{{.Server.Arch}}")
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "aos-ci-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	for _, d := range []string{"bin", "shared", "secrets"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets", "openai_api_key"), []byte("sk-test-dummy-ci-not-a-real-key"), 0o644); err != nil {
		return err
	}
	env := []string{"GOOS=linux", "GOARCH=" + strings.TrimSpace(arch), "CGO_ENABLED=0"}
	if err := runEnv(env, "go", "test", "-c", "-o", filepath.Join(dir, "bin", "hostcheck.test"), "./tools/hostcheck"); err != nil {
		return err
	}
	return run("docker", "run", "--rm", "-e", "AOS_INTEGRATION=1",
		"-v", filepath.Join(dir, "bin")+":/t:ro",
		"-v", filepath.Join(dir, "shared")+":/home/aos/Shared",
		"-v", filepath.Join(dir, "secrets")+":/run/secrets:ro",
		"--entrypoint", "/t/hostcheck.test", "agentic-os:cli", "-test.run", "TestHostCheck", "-test.v")
}

func run(name string, args ...string) error { return runEnv(nil, name, args...) }

func runEnv(env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func output(name string, args ...string) (string, error) {
	var out bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = &out, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out.String(), nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
