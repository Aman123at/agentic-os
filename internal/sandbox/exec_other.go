//go:build !linux

package sandbox

// ABI returns 0: Landlock exists only on Linux.
func ABI() int { return 0 }

// RunHelperIfRequested does nothing outside Linux.
func RunHelperIfRequested() {}
