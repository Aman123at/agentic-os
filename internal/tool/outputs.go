package tool

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Outputs keeps full command output on disk so Agents can page through it with
// read_output (PLAN.md §8.2), under /var/lib/aos/outputs.
type Outputs struct {
	Dir string
}

var refPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+/[A-Za-z0-9_.-]+$`)

// Create opens a new output file for a Task step; ref names it for read_output.
func (o *Outputs) Create(taskID, name string) (ref string, f *os.File, err error) {
	ref = taskID + "/" + name
	if !refPattern.MatchString(ref) {
		return "", nil, fmt.Errorf("invalid output name %q", ref)
	}
	if err := os.MkdirAll(filepath.Join(o.Dir, taskID), 0o700); err != nil {
		return "", nil, err
	}
	f, err = os.OpenFile(filepath.Join(o.Dir, ref), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	return ref, f, err
}

// Page reads up to limit bytes of an output from offset, for taskID only.
func (o *Outputs) Page(taskID, ref string, offset, limit int64) (data []byte, size int64, err error) {
	if !refPattern.MatchString(ref) || !strings.HasPrefix(ref, taskID+"/") {
		return nil, 0, fmt.Errorf("unknown output %q", ref)
	}
	f, err := os.Open(filepath.Join(o.Dir, ref))
	if err != nil {
		return nil, 0, fmt.Errorf("unknown output %q", ref)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, 0, err
	}
	data, err = io.ReadAll(io.LimitReader(f, limit))
	return data, fi.Size(), err
}

const (
	headBytes = 2 << 10
	tailBytes = 6 << 10
)

// Shape trims output for the model: the first 2 KB and the last 6 KB, with a
// pointer to the full output in between.
func Shape(out []byte, ref string) string {
	if len(out) <= headBytes+tailBytes {
		return string(out)
	}
	head, tail := validUTF8Prefix(out[:headBytes]), validUTF8Suffix(out[len(out)-tailBytes:])
	omitted := len(out) - len(head) - len(tail)
	return fmt.Sprintf("%s\n… [%d bytes omitted; read_output ref=%q offset=%d shows them] …\n%s", head, omitted, ref, len(head), tail)
}

func validUTF8Prefix(b []byte) []byte {
	for i := 0; i < utf8.UTFMax && len(b) > 0 && !utf8.Valid(b); i++ {
		b = b[:len(b)-1]
	}
	return b
}

func validUTF8Suffix(b []byte) []byte {
	for i := 0; i < utf8.UTFMax && len(b) > 0 && !utf8.RuneStart(b[0]); i++ {
		b = b[1:]
	}
	return b
}
