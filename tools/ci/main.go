// Command ci runs every check (PLAN.md §17): `go run ./tools/ci [stage…]`.
// It works the same locally and in GitHub Actions.
//
// Stages: lint, unit, ui, image, integration, e2e, playwright. With no arguments,
// all run in order.
package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Image size targets (PLAN.md §16): unpacked MB per target, compressed MB for any.
var sizeTargets = map[string]int{"cli": 520, "ui": 540}

const compressedTargetMB = 180

// bundleBudgetKB is the Desktop's initial bundle target, gzipped (PLAN.md §16).
const bundleBudgetKB = 150

const desktopDir = "desktop"

var stages = []struct {
	name string
	run  func() error
}{
	{"lint", lint},
	{"unit", unit},
	{"ui", ui},
	{"image", image},
	{"integration", integration},
	{"e2e", e2e},
	{"playwright", playwright},
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

// ui builds the Desktop and checks its initial bundle against the §16 budget.
func ui() error {
	if err := npmInstall(); err != nil {
		return err
	}
	if err := run("npm", "--prefix", desktopDir, "run", "build"); err != nil {
		return err
	}
	kb, err := bundleKB()
	if err != nil {
		return err
	}
	fmt.Printf("    initial bundle: %d KB gzipped (target < %d KB)\n", kb, bundleBudgetKB)
	if kb >= bundleBudgetKB {
		return fmt.Errorf("the Desktop's initial bundle is over its %d KB budget", bundleBudgetKB)
	}
	return nil
}

// playwright runs the Desktop's browser e2e and performance suite (§16, §17).
// The specs drive a real `ui` Machine under Docker Compose with the fake model
// provider (no spend); the suite's global setup builds and starts it. Chromium
// is installed once and cached in ~/.cache/ms-playwright.
func playwright() error {
	if _, err := os.Stat(filepath.Join(desktopDir, "e2e")); os.IsNotExist(err) {
		fmt.Println("    no Playwright specs")
		return nil
	}
	if err := npmInstall(); err != nil {
		return err
	}
	if err := run("npm", "--prefix", desktopDir, "exec", "--", "playwright", "install", "chromium"); err != nil {
		return err
	}
	return run("npm", "--prefix", desktopDir, "run", "e2e")
}

// npmInstall installs the Desktop's dependencies, skipping the reinstall when a
// developer already has node_modules in place; CI starts from a clean checkout.
func npmInstall() error {
	if _, err := os.Stat(filepath.Join(desktopDir, "node_modules")); err == nil {
		return nil
	}
	return run("npm", "ci", "--prefix", desktopDir)
}

// bundleKB is the gzipped size of the initial bundle: the assets index.html
// pulls in (entry script, its module preloads and stylesheets). Lazily loaded
// app chunks are not referenced there, so they do not count against the budget.
func bundleKB() (int, error) {
	index, err := os.ReadFile(filepath.Join(desktopDir, "dist", "index.html"))
	if err != nil {
		return 0, err
	}
	ref := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`)
	total := 0
	for _, m := range ref.FindAllStringSubmatch(string(index), -1) {
		size, err := gzipSize(filepath.Join(desktopDir, "dist", filepath.FromSlash(m[1])))
		if err != nil {
			return 0, err
		}
		total += size
	}
	return total / 1024, nil
}

// gzipSize reports how many bytes a file takes after gzip -9, matching how a
// server delivers it.
func gzipSize(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return 0, err
	}
	if _, err := zw.Write(data); err != nil {
		return 0, err
	}
	if err := zw.Close(); err != nil {
		return 0, err
	}
	return buf.Len(), nil
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
		out, err = output("docker", "image", "inspect", "--format", "{{.Size}}", tag)
		if err != nil {
			return err
		}
		bytes, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
		if err != nil {
			return fmt.Errorf("image size: %q", out)
		}
		compressed := int(bytes >> 20)
		fmt.Printf("    %s image: %d MB unpacked (target < %d MB), %d MB compressed (target < %d MB)\n",
			target, mb, sizeTargets[target], compressed, compressedTargetMB)
		if mb >= sizeTargets[target] || compressed >= compressedTargetMB {
			return fmt.Errorf("%s image is over its size target", target)
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
		"-v", filepath.Join(dir, "shared")+":/shared",
		"-v", filepath.Join(dir, "secrets")+":/run/secrets:ro",
		"--entrypoint", "/t/hostcheck.test", "agentic-os:cli", "-test.run", "TestHostCheck", "-test.v")
}

// e2e runs the milestones' acceptance tests (tools/e2e): the cli Machine under
// Docker Compose, with recorded model conversations instead of OpenAI.
func e2e() error {
	return runEnv([]string{"AOS_E2E=1"}, "go", "test", "-count=1", "-v", "-timeout", "30m", "./tools/e2e")
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
