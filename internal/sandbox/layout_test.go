package sandbox

import (
	"slices"
	"strings"
	"testing"
)

// TestDefaultLayoutHasNoSharedFolder guards the M6.6 removal: no path in the
// default policy names the retired Shared Folder, and the Protected set is the
// dotfile store alone.
func TestDefaultLayoutHasNoSharedFolder(t *testing.T) {
	l := DefaultLayout()
	p := l.Policy()

	for _, set := range [][]string{p.Hidden, p.Protected, p.Writable} {
		for _, path := range set {
			if strings.Contains(strings.ToLower(path), "shared") || path == "/shared" {
				t.Errorf("policy still names the Shared Folder: %q", path)
			}
		}
	}
	if !slices.Equal(p.Protected, []string{l.Protected}) {
		t.Errorf("Protected = %v, want just the dotfile store %q", p.Protected, l.Protected)
	}
	if slices.Contains(p.Writable, "/shared") {
		t.Errorf("Writable still includes /shared: %v", p.Writable)
	}
}
