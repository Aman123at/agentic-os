// Lazy browser install (PLAN.md M6.11, ADR-0008). The image no longer bakes in
// Chromium's headless shell; `sudo aos browser install` fetches it on demand.
// The download is Chrome-for-Testing from Google's bucket, pinned by a version
// and a sha256 we pin ourselves (Playwright verifies nothing), checked for its
// shared-library needs against `ldconfig -p`, refused under 1 GB free, and
// unpacked to a staging path that is renamed into place only once it verifies.
// When libraries are missing and InstallDeps is set, the install apt-gets the
// packages that provide them first (the command already runs as root), then
// re-checks. It stays out of the Install Ledger — Restore would otherwise remove
// the browser's libraries out from under it (ADR-0008 M6 amendment).
package browser

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// InstallRoot is where the headless shell is unpacked; Binary
// (/opt/aos-browser/chrome) is a symlink into it, so the Daemon's run path is
// unchanged whether the browser was baked in or installed later.
const InstallRoot = "/opt/aos-browser"

// minFreeBytes is the free space the install refuses to run below: the zip is
// ~120 MB and unpacks to ~600 MB, so 1 GB leaves room for both plus slack.
const minFreeBytes = 1 << 30

// Pin is one architecture's pinned Chrome-for-Testing download.
type Pin struct {
	// Version is the Chrome-for-Testing version, e.g. "153.0.8010.12".
	Version string
	// Platform is Google's bucket platform, e.g. "linux64".
	Platform string
	// SHA256 is the hex digest of the zip, pinned by us.
	SHA256 string
}

// defaultBaseURL is Google's Chrome-for-Testing bucket; tests point Installer at
// an httptest server instead.
const defaultBaseURL = "https://storage.googleapis.com/chrome-for-testing-public"

// url is the download for this pin under base.
func (p Pin) url(base string) string {
	return fmt.Sprintf("%s/%s/%s/chrome-headless-shell-%s.zip", base, p.Version, p.Platform, p.Platform)
}

// pins is keyed by GOARCH. Google's Chrome-for-Testing bucket ships linux64
// (amd64) only — there is no linux-arm64 headless shell — so arm64 has no pin
// and Install refuses on it, naming Compose as the alternative. The version
// matches the Chrome that Playwright pins for the Compose image (@playwright/test
// 1.63.0 → 153.0.8010.12), so both paths run the same browser.
var pins = map[string]Pin{
	"amd64": {
		Version:  "153.0.8010.12",
		Platform: "linux64",
		SHA256:   "a9da028861a0cf789ff25c2fed45f5f1aaf969ed9247835b6a7821a4f7af9d1d",
	},
}

// Installer fetches and unpacks the headless shell. Its fields default to the
// production values; tests override the pin, HTTP client, free-space, ldconfig
// and apt hooks to run without a network or a Linux kernel.
type Installer struct {
	Root        string                                                    // install dir; default InstallRoot
	Arch        string                                                    // GOARCH; default runtime.GOARCH
	Pins        map[string]Pin                                            // by GOARCH; default pins
	BaseURL     string                                                    // download host; default the CfT bucket
	Client      *http.Client                                              // default 10-minute client
	FreeBytes   func(dir string) (uint64, error)                          // free space at dir; default statfs
	Ldconfig    func(ctx context.Context) (string, error)                 // `ldconfig -p` output; default execs it
	InstallDeps bool                                                      // apt-get the missing libraries before failing
	Apt         func(ctx context.Context, args ...string) (string, error) // `apt-get`; default execs it
	AptCache    func(ctx context.Context, args ...string) (string, error) // `apt-cache`; default execs it
	Out         io.Writer                                                 // progress; nil discards
}

// aptEnv is the environment apt runs under: a clean PATH and a non-interactive,
// non-paging frontend so `apt-get install` never blocks on a prompt.
var aptEnv = []string{
	"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	"HOME=/root", "LANG=C.UTF-8", "DEBIAN_FRONTEND=noninteractive",
	"APT_LISTCHANGES_FRONTEND=none", "TERM=dumb",
}

func (in *Installer) root() string {
	if in.Root != "" {
		return in.Root
	}
	return InstallRoot
}

func (in *Installer) arch() string {
	if in.Arch != "" {
		return in.Arch
	}
	return runtime.GOARCH
}

func (in *Installer) baseURL() string {
	if in.BaseURL != "" {
		return in.BaseURL
	}
	return defaultBaseURL
}

func (in *Installer) client() *http.Client {
	if in.Client != nil {
		return in.Client
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

func (in *Installer) freeBytes(dir string) (uint64, error) {
	if in.FreeBytes != nil {
		return in.FreeBytes(dir)
	}
	return freeBytes(dir)
}

func (in *Installer) ldconfig(ctx context.Context) (string, error) {
	if in.Ldconfig != nil {
		return in.Ldconfig(ctx)
	}
	out, err := exec.CommandContext(ctx, "ldconfig", "-p").Output()
	if err != nil {
		return "", fmt.Errorf("running ldconfig -p: %w", err)
	}
	return string(out), nil
}

func (in *Installer) apt(ctx context.Context, args ...string) (string, error) {
	if in.Apt != nil {
		return in.Apt(ctx, args...)
	}
	cmd := exec.CommandContext(ctx, "apt-get", args...)
	cmd.Env = aptEnv
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (in *Installer) aptCache(ctx context.Context, args ...string) (string, error) {
	if in.AptCache != nil {
		return in.AptCache(ctx, args...)
	}
	cmd := exec.CommandContext(ctx, "apt-cache", args...)
	cmd.Env = aptEnv
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (in *Installer) logf(format string, args ...any) {
	if in.Out != nil {
		fmt.Fprintf(in.Out, format+"\n", args...)
	}
}

// Installed reports whether the headless shell is present.
func (in *Installer) Installed() bool {
	_, err := os.Stat(filepath.Join(in.root(), "chrome"))
	return err == nil
}

// Install fetches, verifies and unpacks the headless shell into Root, leaving
// Root/chrome (== Binary) pointing at it. It refuses if the browser is already
// installed, under 1 GB free, on an unsupported architecture, on a sha256
// mismatch, or if a shared library the binary needs is missing.
func (in *Installer) Install(ctx context.Context) error {
	if in.Installed() {
		return errors.New("the browser is already installed; run `aos browser remove` first to reinstall")
	}
	arch := in.arch()
	pin, ok := in.pins()[arch]
	if !ok {
		return fmt.Errorf("no browser download is pinned for %s: Chrome-for-Testing ships an amd64 (linux64) build only. Run the Browser under Docker Compose instead", arch)
	}

	root := in.root()
	parent := filepath.Dir(root)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if free, err := in.freeBytes(parent); err != nil {
		return err
	} else if free < minFreeBytes {
		return fmt.Errorf("only %d MB free at %s; the browser needs at least %d MB", free>>20, parent, minFreeBytes>>20)
	}

	in.logf("Downloading Chrome-for-Testing %s (%s) ...", pin.Version, pin.Platform)
	zipData, err := in.download(ctx, pin)
	if err != nil {
		return err
	}
	in.logf("Verifying checksum ... ok (%d MB)", len(zipData)>>20)

	staging := root + ".tmp"
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	defer os.RemoveAll(staging) // no-op after a successful rename
	if err := unzip(zipData, staging); err != nil {
		return err
	}
	bin, err := findBinary(staging)
	if err != nil {
		return err
	}
	missing, err := in.checkLibraries(ctx, bin)
	if err != nil {
		return err
	}
	if len(missing) > 0 && in.InstallDeps {
		if err := in.installDeps(ctx, missing); err != nil {
			return err
		}
		if missing, err = in.checkLibraries(ctx, bin); err != nil {
			return err
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the browser needs libraries this system does not have: %s. Install the packages that provide them (on Ubuntu, apt-get install the matching lib* packages), then run `aos browser install` again", strings.Join(missing, ", "))
	}
	// The Daemon runs Root/chrome; link it to the unpacked binary by a relative
	// path so it survives the rename from staging.
	rel, err := filepath.Rel(staging, bin)
	if err != nil {
		return err
	}
	if err := os.Symlink(rel, filepath.Join(staging, "chrome")); err != nil {
		return err
	}
	if err := os.Rename(staging, root); err != nil {
		return fmt.Errorf("moving the browser into place: %w", err)
	}
	in.logf("The browser is installed at %s.", root)
	return nil
}

// Remove deletes the installed headless shell. The profile and Downloads under
// the home folder are left alone. It is not an error to remove what is absent.
func (in *Installer) Remove() error {
	if err := os.RemoveAll(in.root()); err != nil {
		return err
	}
	in.logf("The browser is removed.")
	return nil
}

func (in *Installer) pins() map[string]Pin {
	if in.Pins != nil {
		return in.Pins
	}
	return pins
}

// download fetches the pinned zip and verifies its sha256 before returning the
// bytes, so a corrupt or substituted download never reaches the unpacker.
func (in *Installer) download(ctx context.Context, pin Pin) ([]byte, error) {
	url := pin.url(in.baseURL())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := in.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: HTTP %s", url, resp.Status)
	}
	h := sha256.New()
	buf, err := io.ReadAll(io.TeeReader(resp.Body, h))
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != pin.SHA256 {
		return nil, fmt.Errorf("checksum mismatch: the download does not match the pinned sha256 (got %s, want %s)", got, pin.SHA256)
	}
	return buf, nil
}

// checkLibraries reads the binary's DT_NEEDED entries and returns the sonames
// that `ldconfig -p` does not resolve, so a box missing a library is caught here
// — to install or to report — rather than at the browser's first launch. The
// error return is only for a failure to read the binary or the cache.
func (in *Installer) checkLibraries(ctx context.Context, bin string) ([]string, error) {
	f, err := elf.Open(bin)
	if err != nil {
		return nil, fmt.Errorf("reading the browser binary: %w", err)
	}
	needed, err := f.ImportedLibraries()
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("reading the browser's library needs: %w", err)
	}
	cache, err := in.ldconfig(ctx)
	if err != nil {
		return nil, err
	}
	have := parseLdconfig(cache)
	var missing []string
	for _, lib := range needed {
		if !have[lib] {
			missing = append(missing, lib)
		}
	}
	return missing, nil
}

// libPackages maps each shared-library soname the headless shell may need to the
// Debian/Ubuntu package(s) that provide it. Ubuntu 24.04's time_t transition
// renamed several packages with a "t64" suffix, so those sonames list both
// names; installDeps keeps only the ones the box's apt index actually has, so
// the same map works across releases without knowing the version.
var libPackages = map[string][]string{
	"libnss3.so":             {"libnss3"},
	"libnssutil3.so":         {"libnss3"},
	"libsmime3.so":           {"libnss3"},
	"libnspr4.so":            {"libnspr4"},
	"libatk-1.0.so.0":        {"libatk1.0-0t64", "libatk1.0-0"},
	"libatk-bridge-2.0.so.0": {"libatk-bridge2.0-0t64", "libatk-bridge2.0-0"},
	"libatspi.so.0":          {"libatspi2.0-0t64", "libatspi2.0-0"},
	"libcups.so.2":           {"libcups2t64", "libcups2"},
	"libdrm.so.2":            {"libdrm2"},
	"libgbm.so.1":            {"libgbm1"},
	"libxkbcommon.so.0":      {"libxkbcommon0"},
	"libXcomposite.so.1":     {"libxcomposite1"},
	"libXdamage.so.1":        {"libxdamage1"},
	"libXfixes.so.3":         {"libxfixes3"},
	"libXrandr.so.2":         {"libxrandr2"},
	"libXrender.so.1":        {"libxrender1"},
	"libXext.so.6":           {"libxext6"},
	"libX11.so.6":            {"libx11-6"},
	"libxcb.so.1":            {"libxcb1"},
	"libXi.so.6":             {"libxi6"},
	"libpango-1.0.so.0":      {"libpango-1.0-0"},
	"libpangocairo-1.0.so.0": {"libpangocairo-1.0-0"},
	"libcairo.so.2":          {"libcairo2"},
	"libasound.so.2":         {"libasound2t64", "libasound2"},
	"libdbus-1.so.3":         {"libdbus-1-3"},
	"libexpat.so.1":          {"libexpat1"},
	"libglib-2.0.so.0":       {"libglib2.0-0t64", "libglib2.0-0"},
	"libgio-2.0.so.0":        {"libglib2.0-0t64", "libglib2.0-0"},
	"libgobject-2.0.so.0":    {"libglib2.0-0t64", "libglib2.0-0"},
	"libudev.so.1":           {"libudev1"},
}

// installDeps apt-gets the packages that provide the missing sonames. It maps
// each soname to its candidate package name(s), keeps only those the apt index
// knows (so the 22.04/24.04 name fork resolves without checking the release),
// updates the package lists and installs them. It never fails the whole install
// for an unmapped or unavailable soname — the caller re-checks afterward and
// reports whatever is still missing.
func (in *Installer) installDeps(ctx context.Context, missing []string) error {
	pkgs := in.availablePackages(ctx, packagesFor(missing))
	if len(pkgs) == 0 {
		return nil
	}
	in.logf("Installing system libraries the browser needs: %s ...", strings.Join(pkgs, " "))
	if out, err := in.apt(ctx, "update"); err != nil {
		return fmt.Errorf("apt-get update: %w\n%s", err, lastLines(out, 15))
	}
	args := []string{"install", "-y", "--no-install-recommends"}
	args = append(args, pkgs...)
	if out, err := in.apt(ctx, args...); err != nil {
		return fmt.Errorf("installing the browser's libraries: %w\n%s", err, lastLines(out, 15))
	}
	return nil
}

// packagesFor maps missing sonames to candidate package names, de-duplicated in
// first-seen order.
func packagesFor(missing []string) []string {
	var pkgs []string
	seen := map[string]bool{}
	for _, lib := range missing {
		for _, pkg := range libPackages[lib] {
			if !seen[pkg] {
				seen[pkg] = true
				pkgs = append(pkgs, pkg)
			}
		}
	}
	return pkgs
}

// availablePackages keeps only the candidate packages the apt index knows, so a
// name that does not exist on this release (e.g. the non-t64 name on 24.04) is
// dropped rather than failing the whole `apt-get install`.
func (in *Installer) availablePackages(ctx context.Context, pkgs []string) []string {
	var avail []string
	for _, pkg := range pkgs {
		if _, err := in.aptCache(ctx, "show", pkg); err == nil {
			avail = append(avail, pkg)
		}
	}
	return avail
}

// lastLines returns the final n lines of s, so a long apt log is trimmed to the
// part that names the failure.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// parseLdconfig turns `ldconfig -p` output into the set of library sonames it
// knows. Each cache line looks like "\tlibnss3.so (libc6,x86-64) => /path".
func parseLdconfig(out string) map[string]bool {
	have := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		name, _, ok := strings.Cut(line, " ")
		if ok && name != "" {
			have[name] = true
		}
	}
	return have
}

// unzip extracts a zip archive into dir, preserving the executable bit.
func unzip(data []byte, dir string) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("reading the download: %w", err)
	}
	for _, f := range r.File {
		dest := filepath.Join(dir, f.Name)
		if !within(dir, dest) { // guard against a zip entry escaping dir with ".."
			return fmt.Errorf("the download contains an unsafe path: %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := writeZipFile(f, dest); err != nil {
			return err
		}
	}
	return nil
}

func writeZipFile(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	mode := f.Mode()
	if mode == 0 {
		mode = 0o644
	}
	w, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	// The archive is pinned and checksum-verified before it reaches here.
	_, err = io.Copy(w, rc)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	return err
}

// within reports whether path is inside dir, guarding against zip entries that
// try to escape with "..".
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// findBinary locates the chrome-headless-shell executable in the unpacked tree.
func findBinary(dir string) (string, error) {
	var found string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "chrome-headless-shell" {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", errors.New("the download did not contain chrome-headless-shell")
	}
	if err := os.Chmod(found, 0o755); err != nil {
		return "", err
	}
	return found, nil
}
