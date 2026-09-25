//go:build race

package code

// The race detector slows code several times over, so the speed targets are
// not measured under it.
const raceEnabled = true
