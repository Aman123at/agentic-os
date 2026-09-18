package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The Desktop's streams through a real worker process, as aosd runs them: the
// test binary plays aosd and is re-executed as `aosd __files`.

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == WorkerArg {
		os.Exit(WorkerMain())
	}
	os.Exit(m.Run())
}

func asMe(ops Ops) AsUser {
	return AsUser{Ops: ops, UID: uint32(os.Getuid()), GID: uint32(os.Getgid()), Exe: os.Args[0]}
}

func TestStreamsThroughAWorkerProcess(t *testing.T) {
	ops, home := machine(t)
	u := asMe(ops)
	ctx := context.Background()
	p := filepath.Join(home, "big.bin")
	want := bytes.Repeat([]byte("abcdefghij"), 300_000) // 3 MB

	if n, err := u.WriteStream(ctx, WriteStreamArgs{Path: p, Size: int64(len(want))}, bytes.NewReader(want)); err != nil || n != int64(len(want)) {
		t.Fatalf("upload: %d, %v", n, err)
	}
	var got bytes.Buffer
	if err := u.ReadStream(ctx, StreamArgs{Path: p, Range: "bytes=1000000-"}, func(StreamHeader) error { return nil }, &got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want[1_000_000:]) {
		t.Fatalf("range read: %d bytes, want %d", got.Len(), len(want)-1_000_000)
	}
	if _, err := u.WriteStream(ctx, WriteStreamArgs{Path: p, Size: 1}, bytes.NewReader([]byte("x"))); !errors.Is(err, ErrExists) {
		t.Errorf("an upload over an existing file: %v, want ErrExists", err)
	}
}

// stopAfter fails once it has taken some bytes, like a browser tab closed mid-download.
type stopAfter struct{ n int }

func (s *stopAfter) Write(p []byte) (int, error) {
	if s.n <= 0 {
		return 0, errors.New("the Desktop went away")
	}
	s.n -= len(p)
	return len(p), nil
}

func TestAWorkerIsStoppedWhenItsReaderGoesAway(t *testing.T) {
	ops, home := machine(t)
	p := filepath.Join(home, "big.bin")
	if err := os.WriteFile(p, bytes.Repeat([]byte("x"), 8<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	err := asMe(ops).ReadStream(ctx, StreamArgs{Path: p}, func(StreamHeader) error { return nil }, &stopAfter{n: 64 << 10})
	if err == nil || ctx.Err() != nil || time.Since(start) > 5*time.Second {
		t.Fatalf("ReadStream returned %v after %s; want the reader's error, promptly", err, time.Since(start))
	}
}

func TestAWorkerReadsOnlyWhatItsUserMay(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("running the worker as another user needs root")
	}
	// The worker runs as nobody, so its binary must be reachable by nobody:
	// copy it out of Go's private build folder.
	dir := t.TempDir()
	for _, d := range []string{dir, filepath.Dir(dir)} {
		if err := os.Chmod(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(dir, "aosd")
	bin, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, bin, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "token")
	if err := os.WriteFile(secret, []byte("root only"), 0o600); err != nil {
		t.Fatal(err)
	}

	u := AsUser{Ops: Ops{Home: dir, UID: 65534}, UID: 65534, GID: 65534, Exe: exe}
	err = u.ReadStream(context.Background(), StreamArgs{Path: secret}, func(StreamHeader) error { return nil }, io.Discard)
	if !errors.Is(err, ErrPermission) {
		t.Fatalf("reading a root-only file as nobody: %v, want ErrPermission", err)
	}
}
