package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/amantiwari/agentic-os/internal/files"
	"github.com/amantiwari/agentic-os/internal/policy"
)

// maxReadBytes caps one read_file result.
const maxReadBytes = 64 << 10

// FilesTools returns the Files group (PLAN.md §9).
func FilesTools() []Tool {
	return []Tool{listDir{}, readFile{}, fileInfo{}, searchFiles{}, writeFile{}, editFile{}, transfer{move: true}, transfer{}, deleteFile{}}
}

// runFiles runs a file operation for the call and turns failures into Results.
func runFiles(ctx context.Context, env *Env, r Run, op string, args, result any) *Result {
	err := env.Files(r.Widen).Run(ctx, op, args, result, nil)
	if err == nil {
		return nil
	}
	res := fileError(env, err)
	return &res
}

func fileError(env *Env, err error) Result {
	msg := err.Error()
	switch {
	case errors.Is(err, files.ErrExists):
		return Errorf("%s (set overwrite to replace it)", msg)
	case errors.Is(err, files.ErrPermission):
		return Errorf("%s. The sandbox refused this: it is outside the folders Agents may change, or a Protected Path. To change a Protected Path, call the Tool again; the user will be asked", msg)
	}
	return Errorf("%s", msg)
}

// ---------------------------------------------------------------- list_dir

type listDir struct{}

func (listDir) Spec() Spec {
	return Spec{Name: "list_dir", Description: "List a folder: folders first, then files, with sizes and modification times.",
		Parameters: object(map[string]any{"path": str("Folder to list; ~ is the home folder")})}
}

func (listDir) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct{ Path string }
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	path := env.Abs(a.Path)
	return &Call{Summary: "List " + env.Display(path), ReadOnly: true, Policy: policy.Call{Tool: "list_dir", Folder: path},
		Run: func(ctx context.Context, r Run) Result {
			var entries []files.Info
			if res := runFiles(ctx, env, r, files.OpList, files.PathArgs{Path: path}, &entries); res != nil {
				return *res
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%s: %d entries\n", env.Display(path), len(entries))
			for i, e := range entries {
				if i == 500 {
					fmt.Fprintf(&b, "… %d more (use search_files or run_command to narrow down)\n", len(entries)-i)
					break
				}
				mod := e.ModTime.Format("2006-01-02 15:04")
				switch {
				case e.Symlink:
					fmt.Fprintf(&b, "link  %s  %s -> %s\n", mod, e.Name, e.LinkTarget)
				case e.Dir:
					fmt.Fprintf(&b, "dir   %s  %s/\n", mod, e.Name)
				default:
					fmt.Fprintf(&b, "file  %s  %s  %s\n", mod, e.Name, humanSize(e.Size))
				}
			}
			return Result{Output: b.String()}
		}}, nil
}

// ---------------------------------------------------------------- read_file

type readFile struct{}

func (readFile) Spec() Spec {
	return Spec{Name: "read_file", Description: "Read a text file, optionally a range of lines. Results are capped at 64 KB; read further ranges to continue.",
		Parameters: object(map[string]any{
			"path":       str("File to read"),
			"start_line": optInt("First line (1-based); null for the start"),
			"end_line":   optInt("Last line, inclusive; null for the end"),
		})}
}

func (readFile) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct {
		Path      string
		StartLine int `json:"start_line"`
		EndLine   int `json:"end_line"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	path := env.Abs(a.Path)
	return &Call{Summary: "Read " + env.Display(path), ReadOnly: true, Policy: policy.Call{Tool: "read_file", Folder: filepath.Dir(path)},
		Run: func(ctx context.Context, r Run) Result {
			var t files.Text
			if res := runFiles(ctx, env, r, files.OpReadText, files.ReadTextArgs{Path: path, FirstLine: a.StartLine, LastLine: a.EndLine, MaxBytes: maxReadBytes}, &t); res != nil {
				return *res
			}
			if t.Binary {
				return Result{Output: fmt.Sprintf("%s is a binary file (%s). Use run_command with a suitable program (file, unzip -l, pdftotext, …).", env.Display(path), humanSize(t.Size))}
			}
			if t.TotalLines == 0 {
				return Result{Output: env.Display(path) + " is empty."}
			}
			header := fmt.Sprintf("%s, lines %d-%d of %d:\n", env.Display(path), t.FirstLine, t.LastLine, t.TotalLines)
			if t.FirstLine == 0 {
				header = fmt.Sprintf("%s has %d lines; none in the requested range.\n", env.Display(path), t.TotalLines)
			}
			out := header + t.Content
			if t.Truncated {
				out += fmt.Sprintf("\n… truncated at 64 KB; continue with start_line=%d\n", t.LastLine+1)
			}
			return Result{Output: out}
		}}, nil
}

// ---------------------------------------------------------------- file_info

type fileInfo struct{}

func (fileInfo) Spec() Spec {
	return Spec{Name: "file_info", Description: "Show whether a path exists, its type, size, permissions, modification time and symlink target.",
		Parameters: object(map[string]any{"path": str("Path to inspect")})}
}

func (fileInfo) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct{ Path string }
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	path := env.Abs(a.Path)
	return &Call{Summary: "Inspect " + env.Display(path), ReadOnly: true, Policy: policy.Call{Tool: "file_info", Folder: filepath.Dir(path)},
		Run: func(ctx context.Context, r Run) Result {
			var info files.Info
			err := env.Files(r.Widen).Run(ctx, files.OpStat, files.PathArgs{Path: path}, &info, nil)
			if errors.Is(err, files.ErrNotExist) {
				return Result{Output: env.Display(path) + " does not exist."}
			}
			if err != nil {
				return fileError(env, err)
			}
			kind := "file"
			switch {
			case info.Symlink:
				kind = "symlink to " + info.LinkTarget
			case info.Dir:
				kind = "folder"
			}
			return Result{Output: fmt.Sprintf("%s: %s, %s, mode %v, modified %s", env.Display(path), kind, humanSize(info.Size), info.Mode.Perm(), info.ModTime.Format(time.RFC3339))}
		}}, nil
}

// ---------------------------------------------------------------- search_files

type searchFiles struct{}

func (searchFiles) Spec() Spec {
	return Spec{Name: "search_files", Description: "Find files under a folder by name pattern (glob such as *.pdf) and/or by text they contain. Skips .git, node_modules and caches.",
		Parameters: object(map[string]any{
			"path":    str("Folder to search"),
			"name":    optStr("Glob matched against file names, e.g. *.pdf"),
			"content": optStr("Text that matching lines contain"),
			"limit":   optInt("Maximum results (default 100)"),
		})}
}

func (searchFiles) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct {
		Path, Name, Content string
		Limit               int
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if a.Name == "" && a.Content == "" {
		return nil, errors.New("give name, content or both")
	}
	if a.Limit <= 0 || a.Limit > 1000 {
		a.Limit = 100
	}
	root := env.Abs(a.Path)
	return &Call{Summary: "Search " + env.Display(root), ReadOnly: true, Policy: policy.Call{Tool: "search_files", Folder: root},
		Run: func(ctx context.Context, r Run) Result {
			var matches []files.Match
			q := files.SearchQuery{Name: a.Name, Content: a.Content, Limit: a.Limit}
			if res := runFiles(ctx, env, r, files.OpSearch, files.SearchArgs{Root: root, Query: q}, &matches); res != nil {
				return *res
			}
			if len(matches) == 0 {
				return Result{Output: "No matches."}
			}
			var b strings.Builder
			for _, m := range matches {
				if m.Line > 0 {
					fmt.Fprintf(&b, "%s:%d: %s\n", env.Display(m.Path), m.Line, m.Text)
				} else {
					fmt.Fprintln(&b, env.Display(m.Path))
				}
			}
			if len(matches) == a.Limit {
				fmt.Fprintf(&b, "(stopped at %d results)\n", a.Limit)
			}
			return Result{Output: b.String()}
		}}, nil
}

// ---------------------------------------------------------------- write_file

type writeFile struct{}

func (writeFile) Spec() Spec {
	return Spec{Name: "write_file", Description: "Create a file with the given content, creating missing folders. Replacing an existing file requires overwrite=true and is a Risky Action.",
		Parameters: object(map[string]any{
			"path":      str("File to write"),
			"content":   str("Complete file content"),
			"overwrite": optBool("Replace the file if it exists"),
		})}
}

func (writeFile) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct {
		Path, Content string
		Overwrite     bool
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	path := env.Abs(a.Path)
	exists, dir := env.stat(path)
	if dir {
		return nil, fmt.Errorf("%s is a folder", env.Display(path))
	}
	c := &Call{Summary: "Create " + env.Display(path), Policy: policy.Call{Tool: "write_file", Folder: filepath.Dir(path), Effects: []policy.Effect{{Path: path, Op: policy.Write}}}}
	if exists && a.Overwrite {
		c.Summary = "Replace " + env.Display(path)
		c.Policy.Risky = true
		c.Policy.Reasons = []string{"overwrites " + env.Display(path)}
	}
	c.Run = func(ctx context.Context, r Run) Result {
		if res := runFiles(ctx, env, r, files.OpWrite, files.WriteArgs{Path: path, Content: a.Content, Overwrite: a.Overwrite}, nil); res != nil {
			return *res
		}
		return Result{Output: fmt.Sprintf("Wrote %s (%s).", env.Display(path), humanSize(int64(len(a.Content))))}
	}
	return c, nil
}

// ---------------------------------------------------------------- edit_file

type editFile struct{}

func (editFile) Spec() Spec {
	return Spec{Name: "edit_file", Description: "Edit a text file by exact search and replace. Each old text must match exactly once (include surrounding lines to make it unique), unless all=true. Either every edit applies or none.",
		Parameters: object(map[string]any{
			"path": str("File to edit"),
			"edits": map[string]any{"type": "array", "items": object(map[string]any{
				"old": str("Exact text to find"),
				"new": str("Replacement text"),
				"all": optBool("Replace every occurrence"),
			})},
		})}
}

func (editFile) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct {
		Path  string
		Edits []files.Edit
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if len(a.Edits) == 0 {
		return nil, errors.New("no edits given")
	}
	path := env.Abs(a.Path)
	return &Call{Summary: fmt.Sprintf("Edit %s (%d change(s))", env.Display(path), len(a.Edits)),
		Policy: policy.Call{Tool: "edit_file", Folder: filepath.Dir(path), Effects: []policy.Effect{{Path: path, Op: policy.Write}}},
		Run: func(ctx context.Context, r Run) Result {
			if res := runFiles(ctx, env, r, files.OpEdit, files.EditArgs{Path: path, Edits: a.Edits}, nil); res != nil {
				return *res
			}
			return Result{Output: fmt.Sprintf("Edited %s.", env.Display(path))}
		}}, nil
}

// ---------------------------------------------------------------- move and copy

type transfer struct{ move bool }

func (t transfer) name() string {
	if t.move {
		return "move"
	}
	return "copy"
}

func (t transfer) Spec() Spec {
	verb := "Copy a file or folder"
	if t.move {
		verb = "Move or rename a file or folder"
	}
	return Spec{Name: t.name(), Description: verb + ". The destination is the new path itself, not a folder to put it in. Replacing an existing destination requires overwrite=true and is a Risky Action.",
		Parameters: object(map[string]any{
			"source":      str("Existing path"),
			"destination": str("New path"),
			"overwrite":   optBool("Replace the destination if it exists"),
		})}
}

func (t transfer) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct {
		Source, Destination string
		Overwrite           bool
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	src, dst := env.Abs(a.Source), env.Abs(a.Destination)
	if exists, _ := env.stat(src); !exists && env.Stat != nil {
		return nil, fmt.Errorf("%s does not exist", env.Display(src))
	}
	verb := "Copy"
	c := &Call{Policy: policy.Call{Tool: t.name(), Folder: filepath.Dir(dst), Effects: []policy.Effect{{Path: dst, Op: policy.Write}}}}
	if t.move {
		verb = "Move"
		c.Policy.Effects = append([]policy.Effect{{Path: src, Op: policy.Delete}}, c.Policy.Effects...)
	}
	c.Summary = fmt.Sprintf("%s %s to %s", verb, env.Display(src), env.Display(dst))
	if exists, _ := env.stat(dst); exists && a.Overwrite {
		c.Policy.Risky = true
		c.Policy.Reasons = []string{"overwrites " + env.Display(dst)}
	}
	op := files.OpCopy
	if t.move {
		op = files.OpMove
	}
	c.Run = func(ctx context.Context, r Run) Result {
		if res := runFiles(ctx, env, r, op, files.TransferArgs{Source: src, Destination: dst, Overwrite: a.Overwrite}, nil); res != nil {
			return *res
		}
		done := map[string]string{"Move": "Moved", "Copy": "Copied"}[verb]
		return Result{Output: fmt.Sprintf("%s %s to %s.", done, env.Display(src), env.Display(dst))}
	}
	return c, nil
}

// ---------------------------------------------------------------- delete

type deleteFile struct{}

func (deleteFile) Spec() Spec {
	return Spec{Name: "delete", Description: "Delete a file or folder by moving it to the Trash, where the user can restore it. Temporary folders and caches (/tmp, node_modules, __pycache__, .cache) are deleted permanently. Always a Risky Action.",
		Parameters: object(map[string]any{"path": str("Path to delete")})}
}

func (deleteFile) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct{ Path string }
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	path := env.Abs(a.Path)
	if path == "/" || path == env.Home {
		return nil, fmt.Errorf("refusing to delete %s", env.Display(path))
	}
	summary := "Move " + env.Display(path) + " to the Trash"
	return &Call{Summary: summary,
		Policy: policy.Call{Tool: "delete", Folder: filepath.Dir(path), Risky: true, Reasons: []string{"deletes " + env.Display(path)},
			Effects: []policy.Effect{{Path: path, Op: policy.Delete}}},
		Run: func(ctx context.Context, r Run) Result {
			var item files.TrashItem
			if res := runFiles(ctx, env, r, files.OpDelete, files.PathArgs{Path: path}, &item); res != nil {
				return *res
			}
			if item.ID == "" {
				return Result{Output: fmt.Sprintf("Deleted %s permanently (temporary or cache files are not kept in the Trash).", env.Display(path))}
			}
			return Result{Output: fmt.Sprintf("Moved %s to the Trash (%s); the user can restore it.", env.Display(path), humanSize(item.Size))}
		}}, nil
}

func humanSize(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
}
