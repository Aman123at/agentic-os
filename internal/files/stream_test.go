package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestByteRangeFollowsRFC9110(t *testing.T) {
	const size = 1000
	for _, tc := range []struct {
		header                 string
		start, length          int64
		partial, unsatisfiable bool
	}{
		{"", 0, size, false, false},
		{"bytes=0-99", 0, 100, true, false},
		{"bytes=900-", 900, 100, true, false},
		{"bytes=900-5000", 900, 100, true, false}, // clipped to the end
		{"bytes=-100", 900, 100, true, false},     // the last 100 bytes
		{"bytes=-5000", 0, size, true, false},     // more than the file: all of it
		{"bytes=1000-", 0, 0, false, true},        // starts past the end
		{"bytes=-0", 0, 0, false, true},
		{"bytes=0-1,5-6", 0, size, false, false}, // multi-part: the whole file
		{"bytes=5-1", 0, size, false, false},     // malformed: ignored
		{"items=0-1", 0, size, false, false},
		{"bytes=x-", 0, size, false, false},
	} {
		start, length, partial, unsat := byteRange(tc.header, size)
		if start != tc.start || length != tc.length || partial != tc.partial || unsat != tc.unsatisfiable {
			t.Errorf("%q: start %d length %d partial %v unsatisfiable %v, want %d %d %v %v",
				tc.header, start, length, partial, unsat, tc.start, tc.length, tc.partial, tc.unsatisfiable)
		}
	}
}

// pipeStreamer runs stream operations through Serve over in-memory pipes, as a
// worker process would.
type pipeStreamer struct{ ops Ops }

func (p pipeStreamer) run(ctx context.Context, op string, args any, body io.Reader, read func(io.Reader) error) error {
	in, err := StreamRequest(p.ops, op, args, body)
	if err != nil {
		return err
	}
	outR, outW := io.Pipe()
	go func() { outW.CloseWithError(Serve(ctx, in, outW)) }()
	err = read(outR)
	_ = outR.Close()
	return err
}

func (p pipeStreamer) ReadStream(ctx context.Context, a StreamArgs, header func(StreamHeader) error, w io.Writer) error {
	return p.run(ctx, OpReadStream, a, nil, func(r io.Reader) error { return ReadStreamResponse(r, header, w) })
}

func (p pipeStreamer) WriteStream(ctx context.Context, a WriteStreamArgs, body io.Reader) (int64, error) {
	var n int64
	err := p.run(ctx, OpWriteStream, a, body, func(r io.Reader) error { return ReadResponse(r, &n, nil) })
	return n, err
}

func TestStreamsRoundTripThroughTheWorkerProtocol(t *testing.T) {
	ops, home := machine(t)
	s := pipeStreamer{ops: ops}
	ctx := context.Background()
	p := filepath.Join(home, "movie.bin")
	want := bytes.Repeat([]byte("0123456789"), 50_000) // 500 KB, well past any pipe buffer

	if n, err := s.WriteStream(ctx, WriteStreamArgs{Path: p, Size: int64(len(want))}, bytes.NewReader(want)); err != nil || n != int64(len(want)) {
		t.Fatalf("upload: %d, %v", n, err)
	}
	if got, err := os.ReadFile(p); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("the uploaded file differs (%d bytes, %v)", len(got), err)
	}

	var head StreamHeader
	var body bytes.Buffer
	if err := s.ReadStream(ctx, StreamArgs{Path: p, Range: "bytes=100-199"}, func(h StreamHeader) error { head = h; return nil }, &body); err != nil {
		t.Fatal(err)
	}
	if head.Size != int64(len(want)) || head.Start != 100 || head.Length != 100 || !head.Partial || !bytes.Equal(body.Bytes(), want[100:200]) {
		t.Fatalf("range read: %+v, %d bytes", head, body.Len())
	}

	body.Reset()
	if err := s.ReadStream(ctx, StreamArgs{Path: p}, func(StreamHeader) error { return nil }, &body); err != nil || !bytes.Equal(body.Bytes(), want) {
		t.Fatalf("whole read: %d bytes, %v", body.Len(), err)
	}

	// Errors keep their kind across the protocol.
	for name, tc := range map[string]struct {
		path string
		want error
	}{
		"a missing file": {filepath.Join(home, "missing"), ErrNotExist},
		"a folder":       {home, ErrNotFile},
	} {
		if err := s.ReadStream(ctx, StreamArgs{Path: tc.path}, func(StreamHeader) error { return nil }, io.Discard); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
	if _, err := s.WriteStream(ctx, WriteStreamArgs{Path: p, Size: 5}, strings.NewReader("again")); !errors.Is(err, ErrExists) {
		t.Errorf("an upload over an existing file: %v, want ErrExists", err)
	}
}

func TestAnUploadReplacesOnlyWhenAskedAndKeepsNothingFromAFailure(t *testing.T) {
	ops, home := machine(t)
	dir := filepath.Join(home, "notes")
	p := filepath.Join(dir, "a.txt")
	if _, err := ops.WriteStream(WriteStreamArgs{Path: p, Size: 5}, strings.NewReader("first")); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.WriteStream(WriteStreamArgs{Path: p, Size: 6}, strings.NewReader("second")); !errors.Is(err, ErrExists) {
		t.Fatalf("without overwrite: %v, want ErrExists", err)
	}
	if _, err := ops.WriteStream(WriteStreamArgs{Path: p, Overwrite: true, Size: 6}, strings.NewReader("second")); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p); got != "second" {
		t.Errorf("after overwrite: %q", got)
	}

	// An upload that fails, or ends before its size, leaves neither the file
	// nor its temporary copy; one over an existing file keeps the old file.
	for name, tc := range map[string]struct {
		args WriteStreamArgs
		body io.Reader
	}{
		"a dropped connection": {WriteStreamArgs{Path: filepath.Join(dir, "b.txt"), Size: 100}, io.MultiReader(strings.NewReader("part"), failingReader{})},
		"a short body":         {WriteStreamArgs{Path: filepath.Join(dir, "c.txt"), Size: 100}, strings.NewReader("only this")},
		"a short replacement":  {WriteStreamArgs{Path: p, Overwrite: true, Size: 100}, strings.NewReader("cut")},
	} {
		if _, err := ops.WriteStream(tc.args, tc.body); err == nil {
			t.Errorf("%s: the upload succeeded", name)
		}
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != "a.txt" || readFile(t, p) != "second" {
		t.Errorf("after the failed uploads the folder holds %v and a.txt is %q; want only a.txt, unchanged", names, readFile(t, p))
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("the connection dropped") }
