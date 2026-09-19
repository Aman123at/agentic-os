package daemon

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The Version the Desktop shows went stale once already: it still said "m1" at
// M4 (PLAN.md M4.8 item 8.11). Tie its -m<n> suffix to the milestones in the
// plan so the next milestone cannot land without bumping it — while leaving the
// base free to advance, because a tagged release stamps that base in with
// -ldflags -X and drops the suffix (M6.18).
func TestVersionMatchesTheCurrentMilestone(t *testing.T) {
	plan, err := os.ReadFile("../../docs/PLAN.md")
	if err != nil {
		t.Fatalf("read the plan: %v", err)
	}
	newest := 0
	for _, m := range regexp.MustCompile(`(?m)^### M(\d+)\b`).FindAllStringSubmatch(string(plan), -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > newest {
			newest = n
		}
	}
	if newest == 0 {
		t.Fatal("the plan lists no milestones")
	}
	// The newest heading is the milestone being worked towards; the Machine
	// reports the one that is finished, so the suffix trails by one.
	wantSuffix := "-m" + strconv.Itoa(newest-1)
	if !strings.HasSuffix(Version, wantSuffix) {
		t.Errorf("Version is %q, but the plan's newest milestone is M%d, so it should end with %q", Version, newest, wantSuffix)
	}
	// The base (before the -m<n> suffix) is the semver the release tag carries:
	// a tagged build stamps `v<base>` in and drops the suffix (M6.18). Keep it a
	// clean X.Y.Z so `v<base>` is a real release and not a prerelease — a -m6
	// suffix on the tag makes /releases/latest 404 (release_test.go).
	base := strings.TrimSuffix(Version, wantSuffix)
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(base) {
		t.Errorf("Version base is %q, want a clean semver like 0.1.0 (the release tag scheme, M6.18)", base)
	}
}
