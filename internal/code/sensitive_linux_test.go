package code

import (
	"context"
	"os/exec"
	"testing"
)

// TestFstabIsCheckedWithTheRealFindmnt runs the one checker a plain Linux
// image has, so the arguments are the ones findmnt takes.
func TestFstabIsCheckedWithTheRealFindmnt(t *testing.T) {
	if _, err := exec.LookPath("findmnt"); err != nil {
		t.Skip("findmnt is not installed")
	}
	ctx := context.Background()
	good, err := Checker{}.Check(ctx, "/etc/fstab", []byte("# no mounts\n"))
	if err != nil || !good.Passed {
		t.Errorf("an empty fstab: %+v, %v; want passed", good, err)
	}
	bad, err := Checker{}.Check(ctx, "/etc/fstab", []byte("this is not a mount line at all\n"))
	if err != nil || bad.Passed || bad.Output == "" {
		t.Errorf("a broken fstab: %+v, %v; want failed, with findmnt's words", bad, err)
	}
}
