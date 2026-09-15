package files

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Streams (PLAN.md §13): GET /files/raw and POST /upload move a file's bytes as
// a stream rather than JSON, so a video can be sought through and a large file
// sent without aosd holding it in memory.
const (
	OpReadStream  = "read_stream"
	OpWriteStream = "write_stream"
)

// MaxUpload caps one upload.
const MaxUpload = 8 << 30

// StreamArgs asks for a file, or the one byte range an HTTP Range header names.
type StreamArgs struct {
	Path  string `json:"path"`
	Range string `json:"range,omitempty"`
}

// StreamHeader describes what a read stream sends before its bytes.
type StreamHeader struct {
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	// Start and Length are the bytes that follow. Partial is set when they
	// answer a Range; Unsatisfiable when the Range starts past the end, and
	// nothing follows.
	Start         int64 `json:"start"`
	Length        int64 `json:"length"`
	Partial       bool  `json:"partial,omitempty"`
	Unsatisfiable bool  `json:"unsatisfiable,omitempty"`
}

// WriteStreamArgs names the file an upload becomes.
type WriteStreamArgs struct {
	Path      string `json:"path"`
	Overwrite bool   `json:"overwrite,omitempty"`
	// Size is how many bytes the upload has. A worker sees a dropped
	// connection only as the end of its input, so fewer bytes than this
	// means the upload was cut short and nothing is kept.
	Size int64 `json:"size"`
}

// ErrNotFile is returned for a folder, a device or a FIFO where a file's bytes were asked for.
var ErrNotFile = errors.New("not a file")

// Streamer moves file bytes as streams.
type Streamer interface {
	// ReadStream calls header with what it will send, then writes those bytes to w.
	ReadStream(ctx context.Context, a StreamArgs, header func(StreamHeader) error, w io.Writer) error
	// WriteStream writes r to a.Path and returns how many bytes it wrote.
	WriteStream(ctx context.Context, a WriteStreamArgs, r io.Reader) (int64, error)
}

// ReadStream implements Streamer.
func (p InProcess) ReadStream(_ context.Context, a StreamArgs, header func(StreamHeader) error, w io.Writer) error {
	return p.Ops.ReadStream(a.Path, a.Range, header, w)
}

// WriteStream implements Streamer.
func (p InProcess) WriteStream(_ context.Context, a WriteStreamArgs, r io.Reader) (int64, error) {
	return p.Ops.WriteStream(a, r)
}

// ReadStream calls header with what it will send of path, then copies those
// bytes to w. Only regular files are read: a FIFO or a device would block or
// never end. A Range past the end of the file sends only the header.
func (o Ops) ReadStream(path, rangeHeader string, header func(StreamHeader) error, w io.Writer) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("%s is a folder: %w", path, ErrNotFile)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is a device or a pipe: %w", path, ErrNotFile)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if fi, err = f.Stat(); err != nil {
		return err
	}
	h := StreamHeader{Size: fi.Size(), ModTime: fi.ModTime()}
	h.Start, h.Length, h.Partial, h.Unsatisfiable = byteRange(rangeHeader, fi.Size())
	if err := header(h); err != nil || h.Unsatisfiable {
		return err
	}
	_, err = io.Copy(w, io.NewSectionReader(f, h.Start, h.Length))
	return err
}

// byteRange resolves an HTTP Range header (RFC 9110 §14) against a file size.
// A missing, malformed or multi-part header means the whole file, as a server
// may ignore it; a well-formed range starting past the end is unsatisfiable.
func byteRange(h string, size int64) (start, length int64, partial, unsatisfiable bool) {
	spec, ok := strings.CutPrefix(strings.TrimSpace(h), "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return 0, size, false, false
	}
	first, last, ok := strings.Cut(strings.TrimSpace(spec), "-")
	if !ok {
		return 0, size, false, false
	}
	if first == "" { // a suffix: the last n bytes
		n, err := strconv.ParseInt(last, 10, 64)
		if err != nil || n < 0 {
			return 0, size, false, false
		}
		if n == 0 || size == 0 {
			return 0, 0, false, true
		}
		n = min(n, size)
		return size - n, n, true, false
	}
	s, err := strconv.ParseInt(first, 10, 64)
	if err != nil || s < 0 {
		return 0, size, false, false
	}
	e := size - 1
	if last != "" {
		v, err := strconv.ParseInt(last, 10, 64)
		if err != nil || v < s {
			return 0, size, false, false
		}
		e = min(v, size-1)
	}
	if s >= size {
		return 0, 0, false, true
	}
	return s, e - s + 1, true, false
}

// WriteStream writes r to a.Path. The bytes go to a temporary file in the same
// folder, renamed into place once all a.Size of them arrived, so nobody sees
// half a file and a failed or cut-short upload leaves nothing behind. Without
// Overwrite an existing path is ErrExists; with it the file itself is replaced
// (a symlink there is replaced, not followed).
func (o Ops) WriteStream(a WriteStreamArgs, r io.Reader) (n int64, err error) {
	path := a.Path
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	if !a.Overwrite {
		// Claim the name first, so a file that appears meanwhile is not replaced.
		var claim *os.File
		claim, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			return 0, fmt.Errorf("write %s: %w", path, ErrExists)
		}
		if err != nil {
			return 0, err
		}
		_ = claim.Close()
		defer func() {
			if err != nil {
				_ = os.Remove(path)
			}
		}()
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if n, err = io.Copy(tmp, r); err == nil && n != a.Size {
		err = fmt.Errorf("the upload ended after %d of %d bytes", n, a.Size)
	}
	if err == nil {
		err = tmp.Chmod(0o644)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, err
	}
	return n, os.Rename(tmp.Name(), path)
}

// The worker side of streams, called from Serve.

func serveReadStream(req request, enc *json.Encoder, out io.Writer) error {
	var a StreamArgs
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return err
	}
	sent := false
	err := req.Ops.ReadStream(a.Path, a.Range, func(h StreamHeader) error {
		raw, err := json.Marshal(h)
		if err != nil {
			return err
		}
		sent = true
		return enc.Encode(response{Done: true, Result: raw})
	}, out)
	if err != nil && !sent {
		return enc.Encode(response{Done: true, Error: err.Error(), Kind: errorKind(err)})
	}
	return err
}

// serveWriteStream writes the bytes that follow the request, after the newline
// that ends it.
func serveWriteStream(req request, enc *json.Encoder, rest io.Reader) error {
	var a WriteStreamArgs
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return err
	}
	body := bufio.NewReader(rest)
	if b, err := body.ReadByte(); err != nil || b != '\n' {
		return errors.New("file worker: the upload does not follow its request")
	}
	n, err := req.Ops.WriteStream(a, body)
	resp := response{Done: true}
	if err != nil {
		resp.Error, resp.Kind = err.Error(), errorKind(err)
	} else if resp.Result, err = json.Marshal(n); err != nil {
		resp.Error = err.Error()
	}
	return enc.Encode(resp)
}

// The aosd side of streams, for workers reached through pipes.

// ReadStreamResponse reads a read stream from a worker's output: the header,
// passed to header, then the bytes, copied to w.
func ReadStreamResponse(r io.Reader, header func(StreamHeader) error, w io.Writer) error {
	br := bufio.NewReader(r)
	line, err := br.ReadBytes('\n')
	if err != nil {
		return errors.New("file worker exited without a result")
	}
	var resp response
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("file worker: %w", err)
	}
	if resp.Error != "" {
		return kindError(resp.Kind, resp.Error)
	}
	var h StreamHeader
	if err := json.Unmarshal(resp.Result, &h); err != nil {
		return fmt.Errorf("file worker: %w", err)
	}
	if err := header(h); err != nil || h.Unsatisfiable {
		return err
	}
	if n, err := io.CopyN(w, br, h.Length); err != nil {
		return fmt.Errorf("the file stream stopped after %d of %d bytes: %w", n, h.Length, err)
	}
	return nil
}

// StreamRequest is what a worker reads for a stream operation: the request,
// then, for an upload, its bytes.
func StreamRequest(ops Ops, op string, args any, body io.Reader) (io.Reader, error) {
	var req strings.Builder
	if err := WriteRequest(&req, ops, op, args); err != nil {
		return nil, err
	}
	if body == nil {
		return strings.NewReader(req.String()), nil
	}
	return io.MultiReader(strings.NewReader(req.String()), body), nil
}
