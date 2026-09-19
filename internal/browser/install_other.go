//go:build !linux

package browser

// freeBytes is stubbed off Linux so the package builds on a developer's Mac; the
// real browser install runs only on the Linux Machine. Tests inject their own
// Installer.FreeBytes, so this value is never the one under test.
func freeBytes(dir string) (uint64, error) { return 1 << 40, nil }
