package code

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupsKeepTheLastFiveVersionsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "etc", "sudoers")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	b := &Backups{Dir: filepath.Join(dir, "backups"), Now: func() time.Time { now = now.Add(time.Second); return now }}

	// Nothing there yet: nothing to keep.
	if err := b.Save(file); err != nil {
		t.Fatal(err)
	}
	for i := range 7 {
		if err := os.WriteFile(file, []byte{byte('0' + i)}, 0o640); err != nil {
			t.Fatal(err)
		}
		if err := b.Save(file); err != nil {
			t.Fatal(err)
		}
	}
	list, err := b.List(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 5 || !list[0].SavedAt.After(list[4].SavedAt) {
		t.Fatalf("kept %+v, want the 5 newest, newest first", list)
	}
	newest, err := b.Read(file, list[0].ID)
	if err != nil || string(newest) != "6" {
		t.Errorf("newest = %q, %v; want 6", newest, err)
	}
	oldest, _ := b.Read(file, list[4].ID)
	if string(oldest) != "2" {
		t.Errorf("oldest kept = %q, want 2", oldest)
	}
	if _, err := b.Read(file, "../../etc/passwd"); !errors.Is(err, ErrNoBackup) {
		t.Errorf("a made-up id: %v, want ErrNoBackup", err)
	}
	// A folder keeps nothing; the Trash has it.
	if err := b.Save(filepath.Dir(file)); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.List(filepath.Dir(file)); len(got) != 0 {
		t.Errorf("a folder was kept: %+v", got)
	}
}

// fakeChecker runs a shell line in place of the real program, with the file
// being checked as $1.
func fakeChecker(script string) Checker {
	return Checker{
		LookPath: func(name string) (string, error) { return name, nil },
		Command: func(ctx context.Context, _ string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, "sh", "-c", script, "sh", args[len(args)-1]).CombinedOutput()
		},
	}
}

func TestCheckRunsTheFilesOwnChecker(t *testing.T) {
	ctx := context.Background()
	c, err := fakeChecker(`grep -q ALL "$1" && echo "parsed OK"`).Check(ctx, "/etc/sudoers", []byte("root ALL=(ALL) ALL\n"))
	if err != nil || c.Checker != "visudo -c" || !c.Passed || c.Output != "parsed OK" {
		t.Errorf("a good sudoers: %+v, %v", c, err)
	}
	c, err = fakeChecker(`echo "syntax error in $1 near line 1"; exit 1`).Check(ctx, "/etc/sudoers.d/90-aos", []byte("oops"))
	if err != nil || c.Passed || c.Output != "syntax error in /etc/sudoers.d/90-aos near line 1" {
		t.Errorf("a bad one: %+v, %v; want failed, naming the file rather than the temporary copy", c, err)
	}
	for path, label := range map[string]string{"/etc/ssh/sshd_config": "sshd -t", "/etc/fstab": "findmnt --verify"} {
		if c, _ := fakeChecker("true").Check(ctx, path, nil); c.Checker != label || !c.Passed {
			t.Errorf("%s: %+v, want %s", path, c, label)
		}
	}
	if c, err := fakeChecker("exit 1").Check(ctx, "/etc/hosts", nil); err != nil || c != (Check{}) {
		t.Errorf("a file with no checker: %+v, %v", c, err)
	}
	missing := Checker{LookPath: func(string) (string, error) { return "", exec.ErrNotFound }}
	if c, _ := missing.Check(ctx, "/etc/fstab", nil); !c.Unavailable || !strings.HasPrefix(c.Checker, "findmnt") {
		t.Errorf("no findmnt installed: %+v", c)
	}
}
