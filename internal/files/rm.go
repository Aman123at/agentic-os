package files

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Rm is the `rm` shim early in an Agent Session's PATH (PLAN.md §7.8): it accepts
// rm's common options and moves operands to the Trash, or deletes disposable ones
// permanently. cwd resolves relative operands. It returns rm's exit code.
func Rm(ops Ops, cwd string, args []string, stdout, stderr io.Writer) int {
	var force, recursive, dirs, verbose bool
	var operands []string
	opts := true
	for _, a := range args {
		switch {
		case !opts || a == "-" || !strings.HasPrefix(a, "-"):
			operands = append(operands, a)
		case a == "--":
			opts = false
		case strings.HasPrefix(a, "--"):
			switch a {
			case "--force":
				force = true
			case "--recursive":
				recursive = true
			case "--dir":
				dirs = true
			case "--verbose":
				verbose = true
			case "--interactive", "--interactive=never", "--one-file-system", "--preserve-root", "--no-preserve-root":
			default:
				fmt.Fprintf(stderr, "rm: unrecognized option '%s'\n", a)
				return 1
			}
		default:
			for _, c := range a[1:] {
				switch c {
				case 'f':
					force = true
				case 'r', 'R':
					recursive = true
				case 'd':
					dirs = true
				case 'v':
					verbose = true
				case 'i', 'I':
				default:
					fmt.Fprintf(stderr, "rm: invalid option -- '%c'\n", c)
					return 1
				}
			}
		}
	}
	if len(operands) == 0 {
		if force {
			return 0
		}
		fmt.Fprintln(stderr, "rm: missing operand")
		return 1
	}
	code := 0
	fail := func(format string, a ...any) {
		fmt.Fprintf(stderr, "rm: "+format+"\n", a...)
		code = 1
	}
	for _, name := range operands {
		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		path = filepath.Clean(path)
		if path == "/" {
			fail("it is dangerous to operate recursively on '/'")
			continue
		}
		fi, err := os.Lstat(path)
		if err != nil {
			if !force || !errors.Is(err, fs.ErrNotExist) {
				fail("cannot remove '%s': %s", name, describeErr(err))
			}
			continue
		}
		if fi.IsDir() && !recursive {
			entries, _ := os.ReadDir(path)
			if !dirs || len(entries) > 0 {
				if dirs {
					fail("cannot remove '%s': Directory not empty", name)
				} else {
					fail("cannot remove '%s': Is a directory", name)
				}
				continue
			}
		}
		item, err := ops.Delete(path)
		if err != nil {
			fail("cannot remove '%s': %s", name, describeErr(err))
			continue
		}
		if verbose {
			if item.ID != "" {
				fmt.Fprintf(stdout, "removed '%s' (moved to the Trash)\n", name)
			} else {
				fmt.Fprintf(stdout, "removed '%s'\n", name)
			}
		}
	}
	return code
}

// describeErr formats err like coreutils does.
func describeErr(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "No such file or directory"
	case errors.Is(err, fs.ErrPermission):
		return "Permission denied"
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err.Error()
	}
	return err.Error()
}
