package policy

import "testing"

func TestProtectedPaths(t *testing.T) {
	p := NewProtection(home, []string{"/home/aos/Documents/taxes"})
	// ~/work/link points into the Protected dotfiles; tools pass the resolved path too.
	p.Resolve = func(path string) string {
		if within(path, "/home/aos/work/link") {
			return "/home/.aos-protected/ssh" + path[len("/home/aos/work/link"):]
		}
		return path
	}
	p.DirtyRepo = func(path string) (string, bool) {
		if within(path, "/home/aos/dirty") {
			return "/home/aos/dirty", true
		}
		if within(path, "/home/aos/clean") {
			return "/home/aos/clean", false
		}
		return "", false
	}

	for _, tc := range []struct {
		effect Effect
		want   bool
	}{
		{Effect{"/etc/hosts", Write}, true},
		{Effect{"/usr/local/bin/tool", Write}, true},
		{Effect{"/var/lib/dpkg/status", Write}, true},
		{Effect{"/var/lib/aos/aos.db", Delete}, true},
		{Effect{"/home/aos/.ssh/config", Write}, true},
		{Effect{"/home/.aos-protected/bashrc", Write}, true},
		{Effect{"/home/aos/Documents/taxes/2025.pdf", Write}, true},
		{Effect{"/home/aos/work/link/id_ed25519", Delete}, true},
		{Effect{"/home/aos/Documents", Delete}, true},   // contains a locked path
		{Effect{"/home/aos", Delete}, true},             // contains ~/.ssh
		{Effect{"/home/aos/Documents/*", Delete}, true}, // a glob that reaches the locked path
		{Effect{"/home/aos/Documents/notes.txt", Write}, false},
		{Effect{"/home/aos/Documents", Write}, false}, // creating a file next to it
		{Effect{"/home/aos/project/.env", Write}, true},
		{Effect{"/home/aos/project/.env.local", Delete}, true},
		{Effect{"/home/aos/project/.env.example", Write}, false},
		{Effect{"/home/aos/dirty", Discard}, true},
		{Effect{"/home/aos/dirty/src", Discard}, true},
		{Effect{"/home/aos/dirty", Delete}, true},
		{Effect{"/home/aos/dirty/src/main.go", Write}, false},
		{Effect{"/home/aos/dirty/src/main.go", Delete}, false},
		{Effect{"/home/aos/clean", Discard}, false},
		{Effect{"/tmp/x", Delete}, false},
		{Effect{"/etcetera/x", Write}, false},
	} {
		if _, got := p.Check(tc.effect); got != tc.want {
			t.Errorf("%+v: protected=%v, want %v", tc.effect, got, tc.want)
		}
	}
}
