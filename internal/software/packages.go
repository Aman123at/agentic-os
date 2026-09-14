package software

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Pkg is an installed package as the Ledger records it.
type Pkg struct {
	Version string `json:"version"`
	// Auto marks a package installed as another's dependency.
	Auto bool `json:"auto,omitempty"`
}

// Packages maps package names to installed packages; apt names are name:arch.
type Packages map[string]Pkg

// dpkgFormat is the dpkg-query format parseDpkg reads.
const dpkgFormat = `${Package}\t${Architecture}\t${Version}\t${db:Status-Abbrev}\n`

// parseDpkg reads `dpkg-query -W -f dpkgFormat`. Only installed packages
// count; one removed with its configuration kept is absent.
func parseDpkg(out string) Packages {
	p := Packages{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 4 || len(f[3]) < 2 || f[3][1] != 'i' {
			continue
		}
		p[f[0]+":"+f[1]] = Pkg{Version: f[2]}
	}
	return p
}

// markAuto sets Auto from `apt-mark showauto`, which names packages of the
// native architecture (and arch all) without their architecture.
func (p Packages) markAuto(showauto, native string) {
	auto := map[string]bool{}
	for _, name := range strings.Fields(showauto) {
		auto[name] = true
	}
	for key, pkg := range p {
		name, arch := splitArch(key)
		if auto[key] || auto[name] && (arch == native || arch == "all") {
			pkg.Auto = true
			p[key] = pkg
		}
	}
}

func splitArch(key string) (name, arch string) {
	if i := strings.LastIndex(key, ":"); i >= 0 {
		return key[:i], key[i+1:]
	}
	return key, ""
}

// parsePipx reads `pipx list --json`.
func parsePipx(out string) (Packages, error) {
	var v struct {
		Venvs map[string]struct {
			Metadata struct {
				MainPackage struct {
					Package        string `json:"package"`
					PackageVersion string `json:"package_version"`
				} `json:"main_package"`
			} `json:"metadata"`
		} `json:"venvs"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return nil, fmt.Errorf("pipx list: %w", err)
	}
	p := Packages{}
	for name, venv := range v.Venvs {
		if pkg := venv.Metadata.MainPackage.Package; pkg != "" {
			name = pkg
		}
		p[name] = Pkg{Version: venv.Metadata.MainPackage.PackageVersion}
	}
	return p, nil
}

// parseNpm reads `npm ls -g --json --depth=0`.
func parseNpm(out string) (Packages, error) {
	var v struct {
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return nil, fmt.Errorf("npm ls: %w", err)
	}
	p := Packages{}
	for name, d := range v.Dependencies {
		p[name] = Pkg{Version: d.Version}
	}
	return p, nil
}

// diffPackages returns what changed between two states, by name.
func diffPackages(manager string, before, after Packages) []Change {
	var out []Change
	state := func(p Packages, name string) string {
		pkg, ok := p[name]
		if !ok {
			return ""
		}
		b, _ := json.Marshal(pkg)
		return string(b)
	}
	names := map[string]bool{}
	for n := range before {
		names[n] = true
	}
	for n := range after {
		names[n] = true
	}
	for n := range names {
		if b, a := state(before, n), state(after, n); b != a {
			out = append(out, Change{Kind: KindPackage, Manager: manager, Name: n, Before: b, After: a})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// debFile is where apt keeps the downloaded .deb of an apt package (name:arch)
// at version: an epoch's colon is escaped as %3a.
func debFile(archives, key, version string) string {
	name, arch := splitArch(key)
	return fmt.Sprintf("%s/%s_%s_%s.deb", archives, name, strings.ReplaceAll(version, ":", "%3a"), arch)
}
