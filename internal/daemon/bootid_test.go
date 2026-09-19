package daemon

import (
	"encoding/hex"
	"testing"
)

// Run stamps a fresh boot id into every Daemon it builds, so SystemService.Info's
// boot_id is new on every start and a restart (M7.3) can be told from the process
// simply staying up. Two ids from two starts must differ.
func TestBootIDIsNewEveryStart(t *testing.T) {
	a, b := newBootID(), newBootID()
	if a == b {
		t.Fatalf("two starts produced the same boot id %q", a)
	}
	for _, id := range []string{a, b} {
		if raw, err := hex.DecodeString(id); err != nil || len(raw) != 16 {
			t.Errorf("boot id %q is not 16 random bytes of hex (err=%v)", id, err)
		}
	}
}
