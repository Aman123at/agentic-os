package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
)

// pipeRunner runs operations through Serve, as a confined worker process would.
type pipeRunner struct{ ops Ops }

func (p pipeRunner) Run(ctx context.Context, op string, args, result any, progress func(n, total int64)) error {
	var in bytes.Buffer
	if err := WriteRequest(&in, p.ops, op, args); err != nil {
		return err
	}
	outR, outW := io.Pipe()
	go func() {
		outW.CloseWithError(Serve(ctx, &in, outW))
	}()
	return ReadResponse(outR, result, progress)
}

func TestOperationsThroughTheWorkerProtocolKeepTheirErrors(t *testing.T) {
	ops, home, _ := machine(t)
	r := pipeRunner{ops: ops}
	p := filepath.Join(home, "a.txt")
	if err := r.Run(context.Background(), OpWrite, WriteArgs{Path: p, Content: "hi"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	err := r.Run(context.Background(), OpWrite, WriteArgs{Path: p, Content: "again"}, nil, nil)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("second write: %v, want ErrExists", err)
	}
	var text Text
	if err := r.Run(context.Background(), OpReadText, ReadTextArgs{Path: p, MaxBytes: 100}, &text, nil); err != nil || text.Content != "hi" {
		t.Fatalf("read: %+v, %v", text, err)
	}
	var item TrashItem
	if err := r.Run(context.Background(), OpDelete, PathArgs{Path: p}, &item, nil); err != nil || item.OriginalPath != p {
		t.Fatalf("delete: %+v, %v", item, err)
	}
	if err := r.Run(context.Background(), OpStat, PathArgs{Path: p}, &Info{}, nil); !errors.Is(err, ErrNotExist) {
		t.Fatalf("stat of a deleted file: %v, want ErrNotExist", err)
	}
}
