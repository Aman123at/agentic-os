// Lazy browser install (PLAN.md M6.11, ADR-0008). The image no longer bakes in
// Chromium's headless shell; `sudo aos browser install` fetches it on demand.
// The download is Chrome-for-Testing from Google's bucket, pinned by a version
// and a sha256 we pin ourselves (Playwright verifies nothing), checked for its
// shared-library needs against `ldconfig -p`, refused under 1 GB free, and
// unpacked to a staging path that is renamed into place only once it verifies.
// It stays out of the Install Ledger — Restore would otherwise remove the
// browser's libraries out from under it (ADR-0008 M6 amendment).
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
// production values; tests override the pin, HTTP client, free-space and
// ldconfig hooks to run without a network or a Linux kernel.
type Installer struct {
	Root      string                                    // install dir; default InstallRoot
	Arch      string                                    // GOARCH; default runtime.GOARCH
	Pins      map[string]Pin                            // by GOARCH; default pins
	BaseURL   string                                    // download host; default the CfT bucket
	Client    *http.Client                              // default 10-minute client
	FreeBytes func(dir string) (uint64, error)          // free space at dir; default statfs
	Ldconfig  func(ctx context.Context) (string, error) // `ldconfig -p` output; default execs it
	Out       io.Writer                                 // progress; nil discards
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
	if err := in.checkLibraries(ctx, bin); err != nil {
		return err
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

// checkLibraries reads the binary's DT_NEEDED entries and refuses any that
// `ldconfig -p` does not resolve, so a box missing a library fails here with the
// names to install rather than at the browser's first launch.
func (in *Installer) checkLibraries(ctx context.Context, bin string) error {
	f, err := elf.Open(bin)
	if err != nil {
		return fmt.Errorf("reading the browser binary: %w", err)
	}
	needed, err := f.ImportedLibraries()
	f.Close()
	if err != nil {
		return fmt.Errorf("reading the browser's library needs: %w", err)
	}
	cache, err := in.ldconfig(ctx)
	if err != nil {
		return err
	}
	have := parseLdconfig(cache)
	var missing []string
	for _, lib := range needed {
		if !have[lib] {
			missing = append(missing, lib)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the browser needs libraries this system does not have: %s. Install the packages that provide them (on Ubuntu, apt-get install the matching lib* packages), then run `aos browser install` again", strings.Join(missing, ", "))
	}
	return nil
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
