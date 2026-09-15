package files

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"syscall"
	"time"
)

// Operation names of the worker protocol.
const (
	OpStat        = "stat"
	OpList        = "list"
	OpReadText    = "read_text"
	OpRead        = "read"
	OpWrite       = "write"
	OpEdit        = "edit"
	OpMove        = "move"
	OpCopy        = "copy"
	OpDelete      = "delete"
	OpSearch      = "search"
	OpDownload    = "download"
	OpListTrash   = "list_trash"
	OpRestore     = "restore"
	OpEmptyTrash  = "empty_trash"
	OpExpireTrash = "expire_trash"
)

// ErrNotExist and ErrPermission survive the worker protocol, like ErrExists.
var (
	ErrNotExist   = fs.ErrNotExist
	ErrPermission = fs.ErrPermission
)

type PathArgs struct {
	Path string `json:"path"`
}

type WriteArgs struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Overwrite bool   `json:"overwrite,omitempty"`
}

type EditArgs struct {
	Path  string `json:"path"`
	Edits []Edit `json:"edits"`
}

type TransferArgs struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Overwrite   bool   `json:"overwrite,omitempty"`
}

type ReadArgs struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset,omitempty"`
	Limit  int64  `json:"limit,omitempty"`
}

type ReadTextArgs struct {
	Path      string `json:"path"`
	FirstLine int    `json:"first_line,omitempty"`
	LastLine  int    `json:"last_line,omitempty"`
	MaxBytes  int    `json:"max_bytes"`
}

type SearchArgs struct {
	Root  string      `json:"root"`
	Query SearchQuery `json:"query"`
}

type RestoreArgs struct {
	ID string `json:"id"`
}

type ExpireArgs struct {
	Retention time.Duration `json:"retention"`
	MaxBytes  int64         `json:"max_bytes"`
}

// Runner runs file operations: in this process, or in a confined worker process.
// A nil result discards the operation's result.
type Runner interface {
	Run(ctx context.Context, op string, args, result any, progress func(n, total int64)) error
}

// InProcess runs operations directly with Ops, for callers that already run as
// the right user (and for tests). It uses the same encoding as a worker.
type InProcess struct{ Ops Ops }

func (p InProcess) Run(ctx context.Context, op string, args, result any, progress func(n, total int64)) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	out, err := dispatch(ctx, p.Ops, op, raw, progress)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return decodeResult(encoded, result)
}

// request and response are the worker protocol: one request on stdin; progress
// lines and then one final response on stdout.
type request struct {
	Ops  Ops             `json:"ops"`
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args"`
}

type response struct {
	Progress *[2]int64       `json:"progress,omitempty"`
	Done     bool            `json:"done,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
	Kind     string          `json:"kind,omitempty"`
}

// WriteRequest encodes one operation for a worker.
func WriteRequest(w io.Writer, ops Ops, op string, args any) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	ops.Now = nil
	return json.NewEncoder(w).Encode(request{Ops: ops, Op: op, Args: raw})
}

// Serve runs one operation read from in and writes the response to out. It is
// the body of the worker process (`aosd __files`).
func Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	var req request
	dec := json.NewDecoder(in)
	if err := dec.Decode(&req); err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	switch req.Op {
	case OpReadStream:
		return serveReadStream(req, enc, out)
	case OpWriteStream:
		return serveWriteStream(req, enc, io.MultiReader(dec.Buffered(), in))
	}
	progress := func(n, total int64) { _ = enc.Encode(response{Progress: &[2]int64{n, total}}) }
	result, err := dispatch(ctx, req.Ops, req.Op, req.Args, progress)
	resp := response{Done: true}
	if err != nil {
		resp.Error, resp.Kind = err.Error(), errorKind(err)
	} else if resp.Result, err = json.Marshal(result); err != nil {
		resp.Error = err.Error()
	}
	return enc.Encode(resp)
}

// ReadResponse reads a worker's output, reporting progress, into result. The
// line buffer starts small and grows only for a long response (a 1 MiB Read):
// most operations answer in a few hundred bytes, and aosd runs one per Finder
// listing, so a large buffer each time was mostly garbage.
func ReadResponse(r io.Reader, result any, progress func(n, total int64)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 256<<20)
	for sc.Scan() {
		var resp response
		if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
			return fmt.Errorf("file worker: %w", err)
		}
		if resp.Progress != nil {
			if progress != nil {
				progress(resp.Progress[0], resp.Progress[1])
			}
			continue
		}
		if !resp.Done {
			continue
		}
		if resp.Error != "" {
			return kindError(resp.Kind, resp.Error)
		}
		return decodeResult(resp.Result, result)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("file worker exited without a result")
}

func decodeResult(raw []byte, result any) error {
	if result == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, result)
}

func dispatch(ctx context.Context, ops Ops, op string, raw json.RawMessage, progress func(n, total int64)) (any, error) {
	decode := func(v any) error { return json.Unmarshal(raw, v) }
	switch op {
	case OpStat:
		var a PathArgs
		if err := decode(&a); err != nil {
			return nil, err
		}
		return ops.Stat(a.Path)
	case OpList:
		var a PathArgs
		if err := decode(&a); err != nil {
			return nil, err
		}
		return ops.List(a.Path)
	case OpReadText:
		var a ReadTextArgs
		if err := decode(&a); err != nil {
			return nil, err
		}
		return ops.ReadText(a.Path, a.FirstLine, a.LastLine, a.MaxBytes)
	case OpRead:
		var a ReadArgs
		if err := decode(&a); err != nil {
			return nil, err
		}
		return ops.Read(a.Path, a.Offset, a.Limit)
	case OpWrite:
		var a WriteArgs
		if err := decode(&a); err != nil {
			return nil, err
		}
		return nil, ops.Write(a.Path, []byte(a.Content), a.Overwrite)
	case OpEdit:
		var a EditArgs
		if err := decode(&a); err != nil {
			return nil, err
		}
		return nil, ops.Edit(a.Path, a.Edits)
	case OpMove, OpCopy:
		var a TransferArgs
		if err := decode(&a); err != nil {
			return nil, err
		}
		if op == OpMove {
			return nil, ops.Move(a.Source, a.Destination, a.Overwrite)
		}
		return nil, ops.Copy(a.Source, a.Destination, a.Overwrite)
	case OpDelete:
		var a PathArgs
		if err := decode(&a); err != nil {
			return nil, err
		}
		return ops.Delete(a.Path)
	case OpSearch:
		var a SearchArgs
		if err := decode(&a); err != nil {
			return nil, err
		}
		return ops.Search(a.Root, a.Query)
	case OpDownload:
		var a DownloadRequest
		if err := decode(&a); err != nil {
			return nil, err
		}
		return ops.Download(ctx, a, progress)
	case OpListTrash:
		return ops.ListTrash()
	case OpRestore:
		var a RestoreArgs
		if err := decode(&a); err != nil {
			return nil, err
		}
		return ops.Restore(a.ID)
	case OpEmptyTrash:
		return ops.EmptyTrash()
	case OpExpireTrash:
		var a ExpireArgs
		if err := decode(&a); err != nil {
			return nil, err
		}
		return ops.ExpireTrash(a.Retention, a.MaxBytes)
	}
	return nil, fmt.Errorf("unknown file operation %q", op)
}

func errorKind(err error) string {
	switch {
	case errors.Is(err, ErrExists):
		return "exists"
	case errors.Is(err, fs.ErrNotExist):
		return "not_exist"
	case errors.Is(err, fs.ErrPermission), errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return "permission"
	case errors.Is(err, ErrNotFile):
		return "not_file"
	}
	return ""
}

// kindError rebuilds an error that errors.Is recognises from its kind.
func kindError(kind, msg string) error {
	var base error
	switch kind {
	case "exists":
		base = ErrExists
	case "not_exist":
		base = fs.ErrNotExist
	case "permission":
		base = fs.ErrPermission
	case "not_file":
		base = ErrNotFile
	default:
		return errors.New(msg)
	}
	return &workerError{msg: msg, base: base}
}

type workerError struct {
	msg  string
	base error
}

func (e *workerError) Error() string { return e.msg }
func (e *workerError) Unwrap() error { return e.base }
