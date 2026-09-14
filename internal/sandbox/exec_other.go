//go:build !linux

package sandbox

import "errors"

// ABI returns 0: Landlock exists only on Linux.
func ABI() int { return 0 }

// RunHelperIfRequested does nothing outside Linux.
func RunHelperIfRequested() {}

// PrepareHome is Linux-only: the Machine is always Linux.
func PrepareHome(Layout, int, int) ([]string, error) {
	return nil, errors.New("sandbox: PrepareHome needs Linux")
}
