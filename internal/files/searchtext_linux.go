package files

import (
	"context"
	"errors"
	"io"
)

// SearchText implements TextSearcher: the search runs in a worker process as
// the user, and a caller that stops listening ends it.
func (u AsUser) SearchText(ctx context.Context, a TextSearchArgs, emit func(TextFile) error) (TextSearchResult, error) {
	in, err := StreamRequest(u.Ops, OpSearchText, a, nil)
	if err != nil {
		return TextSearchResult{}, err
	}
	var res TextSearchResult
	var emitErr error
	err = runWorker(ctx, u.command(), in, func(out io.Reader) error {
		return readSearchText(out, func(f TextFile) error {
			if err := emit(f); err != nil {
				emitErr = err
				return err
			}
			return nil
		}, &res)
	})
	if emitErr != nil {
		return res, emitErr
	}
	if err != nil && errors.Is(err, context.Canceled) {
		return res, ctx.Err()
	}
	return res, err
}
