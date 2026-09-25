package tool

import (
	"bytes"
	"context"
	"errors"
	"unicode/utf8"

	"github.com/Aman123at/agentic-os/internal/files"
)

// Before-images (PLAN.md M8.11): the first time a Task changes a file with a
// Files Tool, what the file was before is kept, so Code's Change Review can
// show the change and put the old lines back.
const (
	// MaxBeforeKeep is the largest file whose before-image is kept.
	MaxBeforeKeep = 64 << 20
	// MaxBeforeLines is the largest one compared line by line; a bigger one is
	// kept or put back whole.
	MaxBeforeLines = 4 << 20
)

// Before is what a file was before a Task first changed it.
type Before struct {
	Path string
	// Tool is the Tool whose call changed it.
	Tool string
	// Existed is false when the Task is creating the file.
	Existed bool
	Folder  bool
	Size    int64
	// Kept says Content holds the file as it was; false for a file that did
	// not exist, a folder, a link or a file over MaxBeforeKeep.
	Kept    bool
	Content []byte
	// Whole: too big or not text to compare line by line.
	Whole bool
}

// Befores keeps a Task's before-images.
type Befores interface {
	// Has reports whether the Task already kept path's before-image.
	Has(path string) bool
	// Keep stores a before-image; a second one for the same path is ignored.
	Keep(ctx context.Context, b Before) error
}

// before reads path's before-image ahead of a change by toolName, unless the
// Task already has it. keep stores it once the change has succeeded: a call
// that fails changed nothing.
func before(ctx context.Context, env *Env, r Run, toolName string, paths ...string) (keep func()) {
	if env.Befores == nil {
		return func() {}
	}
	var got []Before
	for _, p := range paths {
		if env.Befores.Has(p) {
			continue
		}
		if b, ok := readBefore(ctx, env.Files(r.Widen), toolName, p); ok {
			got = append(got, b)
		}
	}
	return func() {
		for _, b := range got {
			// Losing a before-image must not fail the Agent's change: Change
			// Review then has nothing to compare it with, and says so.
			_ = env.Befores.Keep(context.WithoutCancel(ctx), b)
		}
	}
}

// readBefore reads what path is now. ok is false when it can't be read, in
// which case the change is about to fail the same way.
func readBefore(ctx context.Context, run files.Runner, toolName, path string) (Before, bool) {
	b := Before{Path: path, Tool: toolName}
	var info files.Info
	err := run.Run(ctx, files.OpStat, files.PathArgs{Path: path}, &info, nil)
	switch {
	case errors.Is(err, files.ErrNotExist):
		return b, true
	case err != nil:
		return b, false
	}
	b.Existed, b.Size = true, info.Size
	switch {
	case info.Dir:
		b.Folder = true
		return b, true
	case info.Symlink, info.Size > MaxBeforeKeep:
		b.Whole = true
		return b, true
	}
	var buf bytes.Buffer
	for {
		var raw files.Raw
		if err := run.Run(ctx, files.OpRead, files.ReadArgs{Path: path, Offset: int64(buf.Len()), Limit: files.MaxRead}, &raw, nil); err != nil {
			return b, false
		}
		buf.Write(raw.Content)
		if raw.EOF || len(raw.Content) == 0 {
			break
		}
		if buf.Len() > MaxBeforeKeep {
			b.Whole = true
			return b, true
		}
	}
	b.Kept, b.Content, b.Size = true, buf.Bytes(), int64(buf.Len())
	b.Whole = buf.Len() > MaxBeforeLines || !utf8.Valid(b.Content) || bytes.IndexByte(b.Content, 0) >= 0
	return b, true
}
