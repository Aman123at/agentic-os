// Boot id, kept here (no build constraint) so it can be unit-tested on any
// platform, not only the Linux build that assembles the Daemon.
package daemon

import (
	"crypto/rand"
	"encoding/hex"
)

// newBootID is a random id fixed for one run of aosd, new on every start: a
// restart (M7.3) is confirmed by watching SystemService.Info's boot_id change.
func newBootID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
