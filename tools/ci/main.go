// Command ci runs every check (PLAN.md §17): `go run ./tools/ci [stage…]`.
// It works the same locally and in GitHub Actions.
//
// Stages: lint, unit, unit-linux, ui, auth-ui, image, e2e, playwright. With
// no arguments, all run in order. `live` (the real-model suite, §16 M4.7) is
// optional: it spends the key, so it runs only when named — `go run ./tools/ci live`.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Aman123at/agentic-os/internal/daemon"
)

// Image size targets (PLAN.md §16): unpacked MB per image, compressed MB per
// image. One runtime image carries both Modes (M6.10) and no baked-in browser
// (M6.11) — `sudo aos browser install` fetches it on demand — so there is a
// single build to measure.
var sizeTargets = map[string]int{"default": 540}

var compressedTargets = map[string]int{"default": 180}

// images are the builds the image stage measures.
var images = []struct {
	name, target, tag string
	args              []string
}{
	{"default", "aos", "agentic-os", nil},
}

// bundleBudgetKB is the Desktop's initial bundle target, gzipped (PLAN.md §16).
const bundleBudgetKB = 150

const desktopDir = "desktop"

var stages = []struct {
	name     string
	run      func() error
	optional bool // runs only when named explicitly, never in the default sweep
}{
	{"lint", lint, false},
	{"unit", unit, false},
	{"unit-linux", unitLinux, false},
	{"ui", ui, false},
	{"auth-ui", authUI, false},
	{"image", image, false},
	{"e2e", e2e, false},
	{"playwright", playwright, false},
	// `live` spends the real key, so it is never part of the default sweep.
	{"live", live, true},
}

func main() {
	selected := os.Args[1:]
	failed := false
	for _, s := range stages {
		if len(selected) > 0 && !contains(selected, s.name) {
			continue
		}
		if s.optional && !contains(selected, s.name) {
			continue // optional stages run only when named
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
	if err := unitGolden(); err != nil {
		return err
	}
	if err := desktopProductLanguage(); err != nil {
		return err
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

// desktopProductLanguage guards the M6.14 sweep: "Host" is retired product
// language for the user's own computer, so it must not reappear in desktop/src.
// The generated protos (src/gen) legitimately say "Host" for a cgroup's host, so
// they are skipped.
func desktopProductLanguage() error {
	term := regexp.MustCompile(`\bHost\b`)
	root := filepath.Join(desktopDir, "src")
	var hits []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == filepath.Join(root, "gen") {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".ts", ".tsx", ".css":
		default:
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if term.MatchString(line) {
				hits = append(hits, fmt.Sprintf("%s:%d: %s", path, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(hits) > 0 {
		return fmt.Errorf("the retired product term \"Host\" is back in desktop/src (M6.14); say \"your computer\" or \"the browser\":\n%s", strings.Join(hits, "\n"))
	}
	return nil
}

// unitGolden keeps the committed systemd unit in sync with its source. The unit
// install.sh ships (deploy/systemd/aos.service) must match daemon.Unit(), so a
// hand edit to either without the other fails the build (M6.2).
func unitGolden() error {
	golden, err := os.ReadFile(filepath.Join("deploy", "systemd", "aos.service"))
	if err != nil {
		return fmt.Errorf("reading the systemd unit golden: %w", err)
	}
	if got := daemon.Unit(); got != string(golden) {
		return fmt.Errorf("deploy/systemd/aos.service is out of date with daemon.Unit(); regenerate it")
	}
	return nil
}

func unit() error {
	return run("go", "test", "./...")
}

// unitLinux runs the unit tests under Linux (docker/Dockerfile's go-test
// target), where the _linux.go code (Sessions, the sandbox, the socket guard)
// builds and runs. The repository goes in as build context, never a bind mount,
// which hangs Docker Desktop for folders under ~/Desktop.
func unitLinux() error {
	dir, err := os.MkdirTemp("", "aos-unit-linux-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := run("docker", "buildx", "build", "-f", "docker/Dockerfile", "--target", "go-test",
		"--output", "type=local,dest="+dir, "."); err != nil {
		return err
	}
	report, err := os.ReadFile(filepath.Join(dir, "test.json"))
	if err != nil {
		return err
	}
	r := readTestReport(report)
	fmt.Printf("    linux: %d passed, %d skipped, %d failed\n", r.passed, len(r.skipped), len(r.failed))
	for _, name := range r.skipped {
		fmt.Printf("    skipped: %s\n", name)
	}
	status, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(status)) == "0" {
		return nil
	}
	for _, name := range r.failed {
		fmt.Printf("--- FAIL: %s\n%s", name, r.output[name])
	}
	if len(r.failed) == 0 {
		// Nothing failed by name, so a package failed to build or panicked.
		stderr, _ := os.ReadFile(filepath.Join(dir, "stderr.txt"))
		fmt.Print(string(stderr))
		for _, pkg := range r.brokenPkgs {
			fmt.Printf("--- FAIL: %s\n%s", pkg, r.output[pkg])
		}
	}
	return fmt.Errorf("go test failed under Linux (exit status %s)", strings.TrimSpace(string(status)))
}

// testReport sums up `go test -json` output. Names are "package TestName"; a
// package's own output is kept under its bare import path.
type testReport struct {
	passed          int
	skipped, failed []string
	brokenPkgs      []string
	output          map[string]string
}

func readTestReport(data []byte) testReport {
	r := testReport{output: map[string]string{}}
	for _, line := range bytes.Split(data, []byte("\n")) {
		var ev struct{ Action, Package, Test, Output string }
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		name := ev.Package
		if ev.Test != "" {
			name += " " + ev.Test
		}
		switch ev.Action {
		case "output":
			r.output[name] += ev.Output
		case "pass":
			if ev.Test != "" {
				r.passed++
			}
		case "skip":
			if ev.Test != "" {
				r.skipped = append(r.skipped, name)
			}
		case "fail":
			if ev.Test != "" {
				r.failed = append(r.failed, name)
			} else {
				r.brokenPkgs = append(r.brokenPkgs, name)
			}
		}
	}
	return r
}

// ui builds the Desktop and checks its initial bundle against the §16 budget.
func ui() error {
	if err := npmInstall(); err != nil {
		return err
	}
	if err := run("npm", "--prefix", desktopDir, "run", "build"); err != nil {
		return err
	}
	kb, lazy, err := bundleSizes()
	if err != nil {
		return err
	}
	fmt.Printf("    initial bundle: %d KB gzipped (target < %d KB)\n", kb, bundleBudgetKB)
	for _, c := range lazy {
		fmt.Printf("    lazy chunk %-24s %4d KB gzipped\n", c.name, c.kb)
	}
	if kb >= bundleBudgetKB {
		return fmt.Errorf("the Desktop's initial bundle is over its %d KB budget", bundleBudgetKB)
	}
	return nil
}

// authUI runs the Desktop's authentication-screen suite (PLAN.md §18 M6.5). It
// needs no Docker and no aosd: a Vite dev server serves the Desktop and the
// specs fake aosd's auth RPCs (desktop/e2e-auth), so the three boot phases, the
// expiry modal and logout run on any machine. Chromium is cached like playwright.
func authUI() error {
	if _, err := os.Stat(filepath.Join(desktopDir, "e2e-auth")); os.IsNotExist(err) {
		fmt.Println("    no auth specs")
		return nil
	}
	if err := npmInstall(); err != nil {
		return err
	}
	if err := run("npm", "--prefix", desktopDir, "exec", "--", "playwright", "install", "chromium"); err != nil {
		return err
	}
	return run("npm", "--prefix", desktopDir, "run", "e2e:auth")
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

// live runs the Desktop's real-model suite (§16, §17, M4.7): the same Machine as
// playwright but against the provider in .env, so it spends the key. It is
// optional — it runs only when named (`go run ./tools/ci live`), never in the
// default sweep. The suite's global setup refuses to start without a real key,
// runs headed for the real-GPU drag, and prints the run's spend at the end.
func live() error {
	if _, err := os.Stat(filepath.Join(desktopDir, "e2e-live")); os.IsNotExist(err) {
		fmt.Println("    no live specs")
		return nil
	}
	if err := npmInstall(); err != nil {
		return err
	}
	if err := run("npm", "--prefix", desktopDir, "exec", "--", "playwright", "install", "chromium"); err != nil {
		return err
	}
	return run("npm", "--prefix", desktopDir, "run", "e2e:live")
}

// npmInstall installs the Desktop's dependencies, skipping the reinstall when a
// developer already has node_modules in place; CI starts from a clean checkout.
func npmInstall() error {
	if _, err := os.Stat(filepath.Join(desktopDir, "node_modules")); err == nil {
		return nil
	}
	return run("npm", "ci", "--prefix", desktopDir)
}

// chunkName splits Vite's [name]-[hash] file names; the hash is 8 characters
// that may themselves include '-' or '_'.
var chunkName = regexp.MustCompile(`^(.+)-[\w-]{8}$`)

// chunk is a lazily loaded script or stylesheet, named without its hash.
type chunk struct {
	name string
	kb   int
}

// bundleSizes reports gzipped sizes: the initial bundle, meaning the assets
// index.html pulls in (entry script, its module preloads and stylesheets), and
// each lazily loaded chunk, largest first. Lazy chunks are not referenced there,
// so they do not count against the budget.
func bundleSizes() (initialKB int, lazy []chunk, err error) {
	dist := filepath.Join(desktopDir, "dist")
	index, err := os.ReadFile(filepath.Join(dist, "index.html"))
	if err != nil {
		return 0, nil, err
	}
	initial := map[string]bool{}
	ref := regexp.MustCompile(`(?:src|href)="/assets/([^"]+)"`)
	total := 0
	for _, m := range ref.FindAllStringSubmatch(string(index), -1) {
		initial[m[1]] = true
		size, err := gzipSize(filepath.Join(dist, "assets", m[1]))
		if err != nil {
			return 0, nil, err
		}
		total += size
	}
	assets, err := os.ReadDir(filepath.Join(dist, "assets"))
	if err != nil {
		return 0, nil, err
	}
	for _, a := range assets {
		n := a.Name()
		ext := filepath.Ext(n)
		if initial[n] || (ext != ".js" && ext != ".css") {
			continue
		}
		size, err := gzipSize(filepath.Join(dist, "assets", n))
		if err != nil {
			return 0, nil, err
		}
		name := strings.TrimSuffix(n, ext)
		if m := chunkName.FindStringSubmatch(name); m != nil {
			name = m[1]
		}
		lazy = append(lazy, chunk{name + ext, (size + 1023) / 1024})
	}
	sort.Slice(lazy, func(i, j int) bool { return lazy[i].kb > lazy[j].kb })
	return total / 1024, lazy, nil
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
	for _, img := range images {
		args := append([]string{"buildx", "build", "-f", "docker/Dockerfile", "--target", img.target, "--load", "-t", img.tag}, img.args...)
		if err := run("docker", append(args, ".")...); err != nil {
			return err
		}
		out, err := output("docker", "run", "--rm", "--entrypoint", "sh", img.tag, "-c", "du -sx --block-size=1M / | cut -f1")
		if err != nil {
			return err
		}
		mb, err := strconv.Atoi(strings.TrimSpace(out))
		if err != nil {
			return fmt.Errorf("image size: %q", out)
		}
		out, err = output("docker", "image", "inspect", "--format", "{{.Size}}", img.tag)
		if err != nil {
			return err
		}
		bytes, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
		if err != nil {
			return fmt.Errorf("image size: %q", out)
		}
		compressed := int(bytes >> 20)
		fmt.Printf("    %s image: %d MB unpacked (target < %d MB), %d MB compressed (target < %d MB)\n",
			img.name, mb, sizeTargets[img.name], compressed, compressedTargets[img.name])
		if mb >= sizeTargets[img.name] || compressed >= compressedTargets[img.name] {
			return fmt.Errorf("%s image is over its size target", img.name)
		}
		// The browser is never baked in now (M6.11): the image must not carry it;
		// `sudo aos browser install` fetches it at runtime.
		out, err = output("docker", "run", "--rm", "--entrypoint", "sh", img.tag, "-c", "test -e /opt/aos-browser/chrome && echo yes || echo no")
		if err != nil {
			return err
		}
		if strings.TrimSpace(out) != "no" {
			return fmt.Errorf("%s image: browser present = %s, want no", img.name, strings.TrimSpace(out))
		}
	}
	return nil
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
