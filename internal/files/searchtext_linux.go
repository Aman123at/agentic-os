package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// readSearchText reads a search worker's lines: files, then the result.
func readSearchText(r io.Reader, emit func(TextFile) error, res *TextSearchResult) error {
	dec := json.NewDecoder(r)
	for {
		var line struct {
			Text *TextFile `json:"text,omitempty"`
			response
		}
		if err := dec.Decode(&line); err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("file worker exited without a result")
			}
			return fmt.Errorf("file worker: %w", err)
		}
		if line.Text != nil {
			if err := emit(*line.Text); err != nil {
				return err
			}
			continue
		}
		if line.Done {
			if line.Error != "" {
				return kindError(line.Kind, line.Error)
			}
			return decodeResult(line.Result, res)
		}
	}
}
