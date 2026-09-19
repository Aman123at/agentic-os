package main

import (
	"archive/tar"
	"compress/gzip"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Aman123at/agentic-os/internal/daemon"
)

// repoPath resolves a path relative to the repository root; `go test` runs with
// the package directory (tools/ci) as the working directory.
func repoPath(rel ...string) string {
	return filepath.Join(append([]string{"..", ".."}, rel...)...)
}

// The first trap of M6.18: `-ldflags -X` silently cannot write a const, so
// Version must be declared as a var. Assert the declaration kind directly —
// building with -X does not fail on a const, it just leaves the value untouched,
// so only the source shape proves the stamp will take.
func TestVersionIsAWritableVar(t *testing.T) {
	path := repoPath("internal", "daemon", "version.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	found := false
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range vs.Names {
				if name.Name != "Version" {
					continue
				}
				found = true
				if gd.Tok != token.VAR {
					t.Errorf("Version is declared with %s, but -ldflags -X can only write a var (M6.18)", gd.Tok)
				}
			}
		}
	}
	if !found {
		t.Fatalf("no Version declaration in %s", path)
	}
}

// The release stage serves exactly the two arches install.sh knows how to ask
// for; the asset name is the contract between the two.
func TestReleaseArchMatrix(t *testing.T) {
	want := map[string]bool{"amd64": true, "arm64": true}
	if len(releaseArches) != len(want) {
		t.Fatalf("releaseArches = %v, want amd64 and arm64", releaseArches)
	}
	for _, a := range releaseArches {
		if !want[a] {
			t.Errorf("unexpected release arch %q", a)
		}
	}
}

// install.sh fetches version-less asset names and a sibling SHA256SUMS; the
// release stage must produce exactly those. Assert both sides name the same
// files so a rename on one side breaks the build, not a user's install.
func TestReleaseAssetNamesMatchInstaller(t *testing.T) {
	data, err := os.ReadFile(repoPath("install.sh"))
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	sh := string(data)
	if !strings.Contains(sh, "agentic-os-linux-${ARCH}.tar.gz") {
		t.Error(`install.sh no longer fetches agentic-os-linux-${ARCH}.tar.gz`)
	}
	if !strings.Contains(sh, "SHA256SUMS") {
		t.Error("install.sh no longer fetches SHA256SUMS")
	}
	// install.sh must recognise every arch the release stage builds for.
	for _, a := range releaseArches {
		if !strings.Contains(sh, a+")") {
			t.Errorf("install.sh has no `uname -m` case producing %q", a)
		}
	}
}

// A tarball is two files: aosd (0755) and aos.service (0644), the latter
// byte-for-byte daemon.Unit() so a verified tarball is a verified unit. Pack a
// stand-in binary (the real build is a cross-compile the unit stage skips) and
// read the archive back.
func TestTarballLayout(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "aosd-fake")
	if err := os.WriteFile(binPath, []byte("ELF-stand-in"), 0o755); err != nil {
		t.Fatal(err)
	}
	tarPath := filepath.Join(dir, "agentic-os-linux-amd64.tar.gz")
	if err := writeTarball(tarPath, binPath, daemon.Unit()); err != nil {
		t.Fatalf("writeTarball: %v", err)
	}

	f, err := os.Open(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("not gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	got := map[string]*tar.Header{}
	bodies := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		got[h.Name] = h
		bodies[h.Name] = string(body)
	}
	if len(got) != 2 {
		t.Fatalf("tarball has %d entries, want aosd and aos.service", len(got))
	}
	if h := got["aosd"]; h == nil || h.Mode != 0o755 {
		t.Errorf("aosd entry = %+v, want mode 0755", h)
	}
	if h := got["aos.service"]; h == nil || h.Mode != 0o644 {
		t.Errorf("aos.service entry = %+v, want mode 0644", h)
	}
	if bodies["aos.service"] != daemon.Unit() {
		t.Error("aos.service in the tarball is not byte-for-byte daemon.Unit()")
	}
}

// The second trap of M6.18: the two version schemes must reconcile. The source
// default is the milestone-suffixed dev version (0.1.0-m5); a tag carries the
// clean base (v0.1.0). baseVersion is what bridges them, and the base must be a
// clean semver so `v<base>` is a release, not a prerelease that 404s
// /releases/latest.
func TestVersionSchemeReconciliation(t *testing.T) {
	data, err := os.ReadFile(repoPath("internal", "daemon", "version.go"))
	if err != nil {
		t.Fatalf("read version.go: %v", err)
	}
	src := parseVersion(data)
	if src == "" {
		t.Fatal("could not parse Version out of version.go")
	}
	base := baseVersion(src)
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(base) {
		t.Errorf("baseVersion(%q) = %q, want a clean semver like 0.1.0", src, base)
	}
}

// releaseVersion prefers an explicit tag and strips the leading v, so the
// stamped Version equals the tag the acceptance compares it against.
func TestReleaseVersionStripsTagPrefix(t *testing.T) {
	t.Setenv("GITHUB_REF_NAME", "")
	t.Setenv("RELEASE_VERSION", "v1.2.3")
	if got := releaseVersion(); got != "1.2.3" {
		t.Errorf("releaseVersion with RELEASE_VERSION=v1.2.3 = %q, want 1.2.3", got)
	}
	t.Setenv("RELEASE_VERSION", "")
	t.Setenv("GITHUB_REF_NAME", "v2.0.0")
	if got := releaseVersion(); got != "2.0.0" {
		t.Errorf("releaseVersion with GITHUB_REF_NAME=v2.0.0 = %q, want 2.0.0", got)
	}
}
