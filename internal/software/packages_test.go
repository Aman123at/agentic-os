package software

import (
	"testing"
)

func TestDpkgStatesDiffWithDependenciesMarked(t *testing.T) {
	before := parseDpkg("bash\tarm64\t5.2.21-2ubuntu4\tii \nlibc6\tarm64\t2.39-0ubuntu8\tii \nold-conf\tall\t1.0\trc \n")
	after := parseDpkg("bash\tarm64\t5.2.21-2ubuntu4\tii \nlibc6\tarm64\t2.39-0ubuntu9\tii \nnginx\tarm64\t1.24.0-2ubuntu7\tii \nnginx-common\tall\t1.24.0-2ubuntu7\tii \nlibc6\ti386\t2.39-0ubuntu9\tii \n")
	if _, ok := before["old-conf:all"]; ok {
		t.Error("a package removed with its configuration kept counts as installed")
	}
	after.markAuto("nginx-common\nlibc6:i386\n", "arm64")
	if !after["nginx-common:all"].Auto || !after["libc6:i386"].Auto || after["nginx:arm64"].Auto || after["libc6:arm64"].Auto {
		t.Errorf("auto marks %+v", after)
	}
	var got []string
	for _, c := range diffPackages("apt", before, after) {
		got = append(got, c.Name+" "+c.Before+" → "+c.After)
	}
	want := []string{
		`libc6:arm64 {"version":"2.39-0ubuntu8"} → {"version":"2.39-0ubuntu9"}`,
		`libc6:i386  → {"version":"2.39-0ubuntu9","auto":true}`,
		`nginx-common:all  → {"version":"1.24.0-2ubuntu7","auto":true}`,
		`nginx:arm64  → {"version":"1.24.0-2ubuntu7"}`,
	}
	if len(got) != len(want) {
		t.Fatalf("diff\n%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("diff[%d] = %s\nwant       %s", i, got[i], want[i])
		}
	}
}

func TestPipxAndNpmListsParse(t *testing.T) {
	pipx, err := parsePipx(`{"pipx_spec_version":"0.1","venvs":{"httpie":{"metadata":{"main_package":{"package":"httpie","package_version":"3.2.4"}}}}}`)
	if err != nil || pipx["httpie"].Version != "3.2.4" {
		t.Errorf("pipx %v, %v", pipx, err)
	}
	npm, err := parseNpm(`{"name":"lib","dependencies":{"cowsay":{"version":"1.6.0","overridden":false}}}`)
	if err != nil || npm["cowsay"].Version != "1.6.0" || len(npm) != 1 {
		t.Errorf("npm %v, %v", npm, err)
	}
	if empty, err := parseNpm(`{}`); err != nil || len(empty) != 0 {
		t.Errorf("no global packages: %v, %v", empty, err)
	}
}

func TestDebFilesAreNamedLikeApt(t *testing.T) {
	for _, c := range []struct{ key, version, want string }{
		{"nginx:arm64", "1.24.0-2ubuntu7", "/c/nginx_1.24.0-2ubuntu7_arm64.deb"},
		{"nginx-common:all", "1.24.0-2ubuntu7", "/c/nginx-common_1.24.0-2ubuntu7_all.deb"},
		{"libssl3t64:arm64", "3.0.13-0ubuntu3.5", "/c/libssl3t64_3.0.13-0ubuntu3.5_arm64.deb"},
		{"python3-yaml:arm64", "1:6.0.1-2build2", "/c/python3-yaml_1%3a6.0.1-2build2_arm64.deb"},
	} {
		if got := debFile("/c", c.key, c.version); got != c.want {
			t.Errorf("debFile(%s, %s) = %s, want %s", c.key, c.version, got, c.want)
		}
	}
}
