package agent

import (
	"strconv"
	"strings"
	"testing"
)

func command(cmd string, exit int) attempt {
	result := "Exit code " + strconv.Itoa(exit) + " after 3ms.\nCurrent folder: ~\nOutput:\n"
	if exit != 0 {
		result += "make: *** No rule to make target 'all'.  Stop.\n"
	}
	return newAttempt("run_command", `{"command":`+strconv.Quote(cmd)+`,"timeout_seconds":null}`, "Run: "+cmd, result, exit != 0)
}

func TestTheRetryGuardPausesAProgramThatKeepsFailing(t *testing.T) {
	g := newRetryGuard(3)
	// Looking around between attempts doesn't reset the count.
	for i, a := range []attempt{command("make", 2), command("ls", 0), command("make -j4", 2), command("cat Makefile", 0), command("cd src && make", 2)} {
		if why := g.observe(a); why != "" {
			t.Fatalf("paused after attempt %d: %s", i+1, why)
		}
	}
	why := g.observe(command("sudo make all", 2))
	for _, want := range []string{"4 times", "AOS_MAX_RETRIES=3", "Run: make → Exit code 2: make: *** No rule to make target 'all'.  Stop.", "Run: sudo make all →"} {
		if !strings.Contains(why, want) {
			t.Errorf("the pause lacks %q:\n%s", want, why)
		}
	}
	g.reset()
	if why := g.observe(command("make", 2)); why != "" {
		t.Errorf("paused right after a reset: %s", why)
	}
}

func TestASuccessOfTheSameProgramResetsTheCount(t *testing.T) {
	g := newRetryGuard(3)
	for _, a := range []attempt{command("make a", 2), command("make b", 2), command("make c", 2), command("make d", 0), command("make e", 2), command("make f", 2), command("make g", 2)} {
		if why := g.observe(a); why != "" {
			t.Fatalf("paused: %s", why)
		}
	}
}

func TestTheSameCallRepeatedPausesEvenIfItSucceeds(t *testing.T) {
	g := newRetryGuard(3)
	for i := 0; i < 3; i++ {
		if why := g.observe(command("curl -s localhost:8081", 0)); why != "" {
			t.Fatalf("paused after call %d: %s", i+1, why)
		}
	}
	why := g.observe(command("curl -s localhost:8081", 0))
	if !strings.Contains(why, "same call 4 times in a row") || !strings.Contains(why, "Run: curl -s localhost:8081") {
		t.Errorf("why %q", why)
	}
	// Arguments that differ only in encoding are the same call.
	g.reset()
	g.observe(newAttempt("list_dir", `{"path": "~"}`, "List ~", "", false))
	g.observe(newAttempt("list_dir", `{"path":"~"}`, "List ~", "", false))
	if g.repeats != 1 {
		t.Errorf("repeats %d", g.repeats)
	}
}

func TestOtherToolsCountFailuresPerTool(t *testing.T) {
	g := newRetryGuard(3)
	why := ""
	for _, p := range []string{"~/a", "~/b", "~/c", "~/d"} {
		why = g.observe(newAttempt("read_file", `{"path":"`+p+`"}`, "Read "+p, "error: no such file", true))
	}
	if !strings.Contains(why, "4 times") || !strings.Contains(why, "Read ~/d → error: no such file") {
		t.Errorf("why %q", why)
	}
}

func TestNoRetriesPausesAtTheFirstFailureOrRepeat(t *testing.T) {
	g := newRetryGuard(0)
	if why := g.observe(command("ls", 0)); why != "" {
		t.Fatalf("a first success paused: %s", why)
	}
	if why := g.observe(command("ls", 0)); why == "" {
		t.Error("a repeat did not pause")
	}
	g.reset()
	if why := g.observe(command("make", 2)); why == "" {
		t.Error("a failure did not pause")
	}
}
