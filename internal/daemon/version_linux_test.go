package daemon

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// The Version the Desktop shows went stale once already: it still said "m1" at
// M4 (PLAN.md M4.8 item 8.11). Tie it to the milestones in the plan so the next
// milestone cannot land without bumping it.
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
	// reports the one that is finished, so it trails by one.
	want := "0.1.0-m" + strconv.Itoa(newest-1)
	if Version != want {
		t.Errorf("Version is %q, but the plan's newest milestone is M%d, so it should be %q", Version, newest, want)
	}
}
