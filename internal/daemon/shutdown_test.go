package daemon

import (
	"context"
	"slices"
	"testing"
)

// On shutdown the control socket must close only after Tasks have drained, so a
// long-running Task can still be observed and the Desktop stays responsive until
// it settles (M6.2). The order is: front door, then drain, then socket.
func TestGracefulShutdownDrainsBeforeClosingTheSocket(t *testing.T) {
	var order []string
	step := func(name string) func(context.Context) {
		return func(context.Context) { order = append(order, name) }
	}
	gracefulShutdown(step("front"), step("drain"), step("control"))

	want := []string{"front", "drain", "control"}
	if !slices.Equal(order, want) {
		t.Errorf("shutdown ran %v, want %v", order, want)
	}
}
