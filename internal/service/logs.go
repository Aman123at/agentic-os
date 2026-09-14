package service

import (
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	// ringBytes is how much recent output is kept in memory.
	ringBytes = 64 << 10
	// rotateBytes is the size at which a log file is rotated (one old file is kept).
	rotateBytes = 5 << 20
)

// logBuffer is a Service's output: the recent part in memory, all of it in a
// rotated file, and a feed for followers (aos service logs -f).
type logBuffer struct {
	path string

	mu       sync.Mutex
	ring     []byte
	file     *os.File
	size     int64
	watchers map[chan []byte]struct{}
}

// newLogBuffer opens a Service's log file, continuing the recent output of an
// earlier run. Without a folder, the log is kept in memory only.
func newLogBuffer(dir, name string) *logBuffer {
	l := &logBuffer{watchers: map[chan []byte]struct{}{}}
	if dir == "" {
		return l
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return l
	}
	l.path = filepath.Join(dir, name+".log")
	if f, err := os.Open(l.path); err == nil {
		if fi, err := f.Stat(); err == nil {
			if off := fi.Size() - ringBytes; off > 0 {
				_, _ = f.Seek(off, io.SeekStart)
			}
			l.ring, _ = io.ReadAll(f)
			l.size = fi.Size()
		}
		f.Close()
	}
	l.file, _ = os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	return l
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ring = append(l.ring, p...)
	if len(l.ring) > ringBytes {
		l.ring = append([]byte(nil), l.ring[len(l.ring)-ringBytes:]...)
	}
	if l.file != nil {
		if l.size+int64(len(p)) > rotateBytes {
			l.file.Close()
			_ = os.Rename(l.path, l.path+".1")
			l.file, _ = os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
			l.size = 0
		}
		if l.file != nil {
			n, _ := l.file.Write(p)
			l.size += int64(n)
		}
	}
	chunk := append([]byte(nil), p...)
	for w := range l.watchers {
		select {
		case w <- chunk:
		default: // a follower that falls behind misses output
		}
	}
	return len(p), nil
}

// Tail returns up to n bytes of the most recent output (all kept if n <= 0).
func (l *logBuffer) Tail(n int) []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.ring
	if n > 0 && len(r) > n {
		r = r[len(r)-n:]
	}
	return append([]byte(nil), r...)
}

// Watch returns up to tail bytes of recent output and a feed of what follows,
// until stop is called.
func (l *logBuffer) Watch(tail int) (recent []byte, feed <-chan []byte, stop func()) {
	ch := make(chan []byte, 256)
	l.mu.Lock()
	r := l.ring
	if tail > 0 && len(r) > tail {
		r = r[len(r)-tail:]
	}
	recent = append([]byte(nil), r...)
	l.watchers[ch] = struct{}{}
	l.mu.Unlock()
	var once sync.Once
	return recent, ch, func() {
		once.Do(func() {
			l.mu.Lock()
			delete(l.watchers, ch)
			l.mu.Unlock()
		})
	}
}

// Close closes the log file; followers stop receiving output.
func (l *logBuffer) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
	for w := range l.watchers {
		delete(l.watchers, w)
		close(w)
	}
}
