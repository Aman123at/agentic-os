package session

import (
	"bytes"
	"strconv"
)

// Frame extracts one command's output and exit code from a Session's PTY stream.
//
// The Session shell writes OSC 133-style markers around each command, carrying
// a random per-command nonce so output that merely looks like a marker is never
// mistaken for one:
//
//	ESC ] 133 ; C ; aos=<nonce> BEL           output starts
//	ESC ] 133 ; D ; aos=<nonce> ; <exit> BEL  output ended with <exit>
type Frame struct {
	start, end []byte
	started    bool
	done       bool
	exit       int
	pending    []byte
}

// NewFrame returns a Frame for the command whose markers carry nonce.
func NewFrame(nonce string) *Frame {
	return &Frame{
		start: []byte("\x1b]133;C;aos=" + nonce + "\x07"),
		end:   []byte("\x1b]133;D;aos=" + nonce + ";"),
	}
}

// Feed consumes the next chunk of PTY output and returns the part of it that
// belongs to the command's output. Bytes that might begin a marker are held
// back until the next chunk decides.
func (f *Frame) Feed(chunk []byte) []byte {
	if f.done {
		return nil
	}
	f.pending = append(f.pending, chunk...)
	if !f.started {
		i := bytes.Index(f.pending, f.start)
		if i < 0 {
			f.pending = f.pending[len(f.pending)-partialPrefix(f.pending, f.start):]
			return nil
		}
		f.started = true
		f.pending = f.pending[i+len(f.start):]
	}
	var out []byte
	for {
		i := bytes.Index(f.pending, f.end)
		if i < 0 {
			keep := partialPrefix(f.pending, f.end)
			out = append(out, f.pending[:len(f.pending)-keep]...)
			f.pending = append([]byte(nil), f.pending[len(f.pending)-keep:]...)
			return out
		}
		exit, n, complete := parseExit(f.pending[i+len(f.end):])
		switch {
		case !complete:
			out = append(out, f.pending[:i]...)
			f.pending = append([]byte(nil), f.pending[i:]...)
			return out
		case n < 0:
			// Not a real end marker: keep its first byte as output and search on.
			out = append(out, f.pending[:i+1]...)
			f.pending = f.pending[i+1:]
		default:
			out = append(out, f.pending[:i]...)
			f.exit, f.done, f.pending = exit, true, nil
			return out
		}
	}
}

// parseExit parses "<digits> BEL". It returns n < 0 when b cannot be an exit code,
// and complete=false when more bytes are needed to decide.
func parseExit(b []byte) (exit, n int, complete bool) {
	for i, c := range b {
		switch {
		case c == '\x07' && i > 0:
			v, err := strconv.Atoi(string(b[:i]))
			if err != nil {
				return 0, -1, true
			}
			return v, i + 1, true
		case c < '0' || c > '9' || i >= 3:
			return 0, -1, true
		}
	}
	return 0, 0, false
}

// partialPrefix returns the length of the longest suffix of b that is a proper
// prefix of marker.
func partialPrefix(b, marker []byte) int {
	for n := min(len(b), len(marker)-1); n > 0; n-- {
		if bytes.HasSuffix(b, marker[:n]) {
			return n
		}
	}
	return 0
}

// Done reports whether the end marker has been seen.
func (f *Frame) Done() bool { return f.done }

// ExitCode returns the command's exit code once Done.
func (f *Frame) ExitCode() int { return f.exit }
