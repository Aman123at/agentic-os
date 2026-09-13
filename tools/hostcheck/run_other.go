//go:build !linux

package hostcheck

import (
	"context"
	"errors"
)

// Options describes the Machine under test.
type Options struct{ RequireAosd bool }

// DefaultOptions matches the Machine image.
func DefaultOptions() Options { return Options{} }

// Run is only available inside the Machine.
func Run(context.Context, Options) (Report, error) {
	return nil, errors.New("the host check runs inside the Machine: docker compose exec aos aos doctor --host-check")
}
