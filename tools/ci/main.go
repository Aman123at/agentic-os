// Command ci runs every check (PLAN.md §17): `go run ./tools/ci [stage…]`.
// It works the same locally and in GitHub Actions.
//
// Stages: lint, unit, unit-linux, ui, auth-ui, image, e2e, playwright. With
// no arguments, all run in order. `live` (the real-model suite, §16 M4.7),
// `release` (the tarball build, M6.18) and `docs` (the documentation site,
// M6.21) are optional: they run only when named — `go run ./tools/ci live`,
// `go run ./tools/ci release`, `go run ./tools/ci docs`.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
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
	// `release` builds cross-compiled tarballs; it runs only when named (a tag
	// build calls `go run ./tools/ci release`), never in the default sweep.
	{"release", release, true},
	// `docs` builds the documentation site and checks its sub-path base; it needs
	// Node, so it runs only when named (a tag build calls `go run ./tools/ci
	// docs`), never in the default sweep.
	{"docs", docs, true},
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
	if err := installerRehearsal(); err != nil {
		return err
	}
	if err := installerShellcheck(); err != nil {
		return err
	}
	if err := docsReference(); err != nil {
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

// docsReference fails when the committed command reference no longer matches the
// aos command tree. It is pure Go (needs no Node, unlike the `docs` stage), so it
// belongs in lint: a command whose Short changes drifts the reference the way an
// unformatted file drifts gofmt.
func docsReference() error {
	return run("go", "run", "./tools/docsgen", "-check")
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

// installerRehearsal runs install.sh with DRY_RUN=1 (M6.17) and asserts it
// exits 0. Every mutating step goes through run() and every refusal through
// fail(), so the dry run prints the whole plan and touches nothing — the one
// way the installer is testable from a machine that is not the target VPS.
func installerRehearsal() error {
	cmd := exec.Command("sh", "install.sh")
	cmd.Env = append(os.Environ(), "DRY_RUN=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("install.sh DRY_RUN=1 rehearsal failed: %w\n%s", err, out)
	}
	return nil
}

// installerShellcheck lints install.sh with shellcheck (M6.17). shellcheck is
// present in the CI image but not on every developer's machine, so a missing
// binary is a skip, not a failure — the CI run still enforces it.
func installerShellcheck() error {
	if _, err := exec.LookPath("shellcheck"); err != nil {
		fmt.Println("    shellcheck not installed — skipping install.sh lint (CI runs it)")
		return nil
	}
	if err := run("shellcheck", "install.sh"); err != nil {
		return fmt.Errorf("shellcheck found problems in install.sh: %w", err)
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

// releaseArches are the two platforms the installer serves (M6.18). install.sh
// maps `uname -m` to exactly these; adding one here means adding a case there.
var releaseArches = []string{"amd64", "arm64"}

// releaseDir is where the release stage writes tarballs and SHA256SUMS.
const releaseDir = "dist"

// versionSymbol is the -ldflags -X path stamped at release time. Version lives
// in internal/daemon as a var (not a const), which is what lets -X write it.
const versionSymbol = "github.com/Aman123at/agentic-os/internal/daemon.Version"

// release builds the linux/amd64 and linux/arm64 tarballs install.sh fetches
// (M6.18). Version-less asset names — agentic-os-linux-<arch>.tar.gz — let
// /releases/latest/download resolve without the GitHub API. Each tarball carries
// aosd (the one binary) and aos.service (byte-for-byte daemon.Unit(), so a
// verified tarball is a verified unit); a sibling SHA256SUMS lists them all.
func release() error {
	version := releaseVersion()
	fmt.Printf("    version %s\n", version)
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		return err
	}
	unit := daemon.Unit()
	var sums []string
	for _, arch := range releaseArches {
		bin := filepath.Join(releaseDir, "aosd-"+arch)
		env := []string{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=" + arch}
		ldflags := "-s -w -X " + versionSymbol + "=" + version
		if err := runEnv(env, "go", "build", "-trimpath", "-ldflags", ldflags, "-o", bin, "./cmd/aosd"); err != nil {
			return err
		}
		name := "agentic-os-linux-" + arch + ".tar.gz"
		if err := writeTarball(filepath.Join(releaseDir, name), bin, unit); err != nil {
			return err
		}
		// The staged binary was only an input to the tarball.
		if err := os.Remove(bin); err != nil {
			return err
		}
		sum, err := sha256File(filepath.Join(releaseDir, name))
		if err != nil {
			return err
		}
		// GNU sha256sum's binary form (`<hex> *<name>`); install.sh greps this
		// exact line and runs `sha256sum -c` on it.
		sums = append(sums, fmt.Sprintf("%s *%s", sum, name))
		fmt.Printf("    %s  %s\n", name, sum)
	}
	return os.WriteFile(filepath.Join(releaseDir, "SHA256SUMS"), []byte(strings.Join(sums, "\n")+"\n"), 0o644)
}

// releaseVersion is the version stamped into the release binaries. A tagged
// build — the only kind that reaches users — takes the tag; RELEASE_VERSION or
// GitHub's ref override for a manual run; otherwise the source default's base,
// with the -m<n> development suffix dropped, so a local `release` matches what a
// tag would produce (v0.1.0 → 0.1.0). The first tag MUST be v0.1.0: a -m6 suffix
// makes GitHub mark the release a prerelease and /releases/latest 404s.
func releaseVersion() string {
	if v := os.Getenv("RELEASE_VERSION"); v != "" {
		return strings.TrimPrefix(v, "v")
	}
	if v := os.Getenv("GITHUB_REF_NAME"); strings.HasPrefix(v, "v") {
		return strings.TrimPrefix(v, "v")
	}
	if out, err := output("git", "describe", "--tags", "--exact-match"); err == nil {
		return strings.TrimPrefix(strings.TrimSpace(out), "v")
	}
	data, _ := os.ReadFile(filepath.Join("internal", "daemon", "version.go"))
	return baseVersion(parseVersion(data))
}

// versionRe pulls the string out of `var Version = "…"` in version.go.
var versionRe = regexp.MustCompile(`(?m)^var Version = "([^"]+)"`)

func parseVersion(data []byte) string {
	if m := versionRe.FindSubmatch(data); m != nil {
		return string(m[1])
	}
	return ""
}

// baseVersion drops a development prerelease suffix (0.1.0-m5 → 0.1.0), which is
// the clean form the git tag carries.
func baseVersion(v string) string {
	if i := strings.IndexByte(v, '-'); i >= 0 {
		return v[:i]
	}
	return v
}

// writeTarball writes a .tar.gz carrying aosd (0755) and aos.service (0644) —
// the two-file layout install.sh unpacks (M6.17/M6.18).
func writeTarball(path, binPath, unit string) error {
	binData, err := os.ReadFile(binPath)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	entries := []struct {
		name string
		mode int64
		data []byte
	}{
		{"aosd", 0o755, binData},
		{"aos.service", 0o644, []byte(unit)},
	}
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     e.name,
			Mode:     e.mode,
			Size:     int64(len(e.data)),
		}); err != nil {
			return err
		}
		if _, err := tw.Write(e.data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// sha256File returns a file's hex SHA-256, the form SHA256SUMS lists.
func sha256File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// npmInstall installs the Desktop's dependencies, skipping the reinstall when a
// developer already has node_modules in place; CI starts from a clean checkout.
func npmInstall() error {
	if _, err := os.Stat(filepath.Join(desktopDir, "node_modules")); err == nil {
		return nil
	}
	return run("npm", "ci", "--prefix", desktopDir)
}

const docsDir = "docs-site"

// docsBase is the sub-path the site is served from (astro.config.mjs `base`).
// Every root-absolute href and asset the build emits must start with it, or the
// page 404s once uploaded — and only there, never in `astro dev`/`preview`.
const docsBase = "/agentic-os/"

// docs builds the documentation site and asserts its sub-path base is baked in.
// It needs Node, so it is optional (a tag build names it); the default sweep
// never runs it.
func docs() error {
	if err := docsInstall(); err != nil {
		return err
	}
	if err := run("npm", "--prefix", docsDir, "run", "build"); err != nil {
		return err
	}
	return docsBaseCheck()
}

// docsInstall installs the site's dependencies, skipping the reinstall when a
// developer already has node_modules in place; CI starts from a clean checkout.
func docsInstall() error {
	if _, err := os.Stat(filepath.Join(docsDir, "node_modules")); err == nil {
		return nil
	}
	return run("npm", "ci", "--prefix", docsDir)
}

// docsAttr pulls href/src values out of the built index.html.
var docsAttr = regexp.MustCompile(`(?:href|src)="([^"]*)"`)

// docsBaseCheck fails if any root-absolute URL in dist/index.html points outside
// the sub-path base. It ignores external URLs (http:, //, mailto:, data:),
// in-page anchors and relative links: only paths starting with a single "/"
// must live under docsBase. This is the one place the base mistake is caught —
// it never shows up locally.
func docsBaseCheck() error {
	index := filepath.Join(docsDir, "dist", "index.html")
	data, err := os.ReadFile(index)
	if err != nil {
		return err
	}
	var bad []string
	for _, m := range docsAttr.FindAllStringSubmatch(string(data), -1) {
		v := m[1]
		if !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") {
			continue // external, relative or in-page: not a root-absolute path
		}
		if !strings.HasPrefix(v, docsBase) {
			bad = append(bad, v)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("%s has %d root-absolute URL(s) outside %q (base misconfigured?):\n%s",
			index, len(bad), docsBase, strings.Join(bad, "\n"))
	}
	return nil
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
