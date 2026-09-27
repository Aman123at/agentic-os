package files

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Watch implements Watcher: the watch runs in a worker process as the user,
// which lives as long as ctx. The request goes through a pipe that stays open,
// so a worker whose aosd died sees its input end and exits.
func (u AsUser) Watch(ctx context.Context, a WatchArgs, emit func(WatchEvent) error) error {
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	defer pr.Close()
	defer pw.Close()
	if err := WriteRequest(pw, u.Ops, OpWatch, a); err != nil {
		return err
	}
	started := false
	var emitErr error
	err = runWorker(ctx, u.command(), pr, func(out io.Reader) error {
		return readWatch(out, func(ev WatchEvent) error {
			started = true
			if err := emit(ev); err != nil {
				emitErr = err
				return err
			}
			return nil
		})
	})
	switch {
	case ctx.Err() != nil, emitErr != nil:
		// The caller went away or stopped listening.
		return nil
	case started && err != nil:
		// The worker failed after the watch began: report it as the watch
		// stopping, as Ops.Watch itself does.
		_ = emit(WatchEvent{Status: &WatchStatus{Mode: WatchStopped, Reason: err.Error()}})
		return nil
	}
	return err
}

// debounce gathers a burst of kernel events (a save is several) into one
// batch; flushAfter caps how long a steady stream of them is held.
const (
	debounce   = 100 * time.Millisecond
	flushAfter = 500 * time.Millisecond
)

// readWatch reads a watch worker's lines: events, then a final response.
func readWatch(r io.Reader, emit func(WatchEvent) error) error {
	dec := json.NewDecoder(r)
	for {
		var line struct {
			Watch *WatchEvent `json:"watch,omitempty"`
			response
		}
		if err := dec.Decode(&line); err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("file worker exited without a result")
			}
			return fmt.Errorf("file worker: %w", err)
		}
		if line.Watch != nil {
			if err := emit(*line.Watch); err != nil {
				return err
			}
			continue
		}
		if line.Done {
			if line.Error != "" {
				return kindError(line.Kind, line.Error)
			}
			return nil
		}
	}
}

// watchMask is what a folder is watched for. IN_EXCL_UNLINK drops events for
// files already unlinked but still open, which only a busy log would send.
const watchMask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MODIFY | unix.IN_CLOSE_WRITE | unix.IN_ATTRIB |
	unix.IN_MOVED_FROM | unix.IN_MOVED_TO | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF |
	unix.IN_ONLYDIR | unix.IN_EXCL_UNLINK

// errTooMany ends the inotify watch in favour of polling; its text is the reason.
type errTooMany struct{ reason string }

func (e errTooMany) Error() string { return e.reason }

// notifier is a recursive inotify watch: one watch per folder, added as
// folders appear and dropped as they go.
type notifier struct {
	fd    int
	a     WatchArgs
	wds   map[int]string // watch descriptor → folder
	paths map[string]int // folder → watch descriptor
	p     pending
}

// watchNotify watches a.Root with inotify until ctx ends, emit fails or the
// root goes. It returns why it gave up for polling instead, or "" once it has ended.
func watchNotify(ctx context.Context, a WatchArgs, emit func(WatchEvent) error) string {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return "inotify is not available (" + err.Error() + ")"
	}
	// A non-blocking fd in an os.File is read through Go's poller, so Close
	// wakes a Read that is waiting.
	f := os.NewFile(uintptr(fd), "inotify")
	defer f.Close()
	n := &notifier{fd: fd, a: a, wds: map[int]string{}, paths: map[string]int{}}
	if err := n.addTree(a.Root, nil); err != nil {
		return fallbackReason(err)
	}
	if emit(WatchEvent{Status: &WatchStatus{Mode: WatchLive}}) != nil {
		return ""
	}

	reads := make(chan []byte)
	readErr := make(chan error, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		buf := make([]byte, 64<<10)
		for {
			k, err := f.Read(buf)
			if err != nil {
				readErr <- err
				return
			}
			b := make([]byte, k)
			copy(b, buf[:k])
			select {
			case reads <- b:
			case <-done:
				return
			}
		}
	}()
	stop := context.AfterFunc(ctx, func() { _ = f.Close() })
	defer stop()

	var timer *time.Timer
	var fire <-chan time.Time
	var since time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	flush := func() bool {
		changes := n.p.flush()
		since, fire = time.Time{}, nil
		return len(changes) == 0 || emit(WatchEvent{Changes: changes}) == nil
	}
	for {
		select {
		case <-ctx.Done():
			return ""
		case err := <-readErr:
			if ctx.Err() != nil {
				return ""
			}
			flush()
			_ = emit(WatchEvent{Status: &WatchStatus{Mode: WatchStopped, Reason: "reading changes failed: " + err.Error()}})
			<-ctx.Done()
			return ""
		case <-fire:
			if !flush() {
				return ""
			}
		case b := <-reads:
			rootGone, err := n.handle(b)
			if err != nil {
				// Past the limit: send what was gathered, ask for a fresh
				// listing, and poll from here on.
				if !flush() || emit(WatchEvent{Changes: []Change{{Kind: Rescan, Path: a.Root, Dir: true}}}) != nil {
					return ""
				}
				return fallbackReason(err)
			}
			if rootGone {
				flush()
				_ = emit(WatchEvent{Status: &WatchStatus{Mode: WatchStopped, Reason: "the folder was deleted or moved"}})
				<-ctx.Done()
				return ""
			}
			if n.p.empty() {
				continue
			}
			now := time.Now()
			if since.IsZero() {
				since = now
			}
			wait := min(debounce, flushAfter-now.Sub(since))
			if wait <= 0 {
				if !flush() {
					return ""
				}
				continue
			}
			if timer == nil {
				timer = time.NewTimer(wait)
			} else {
				timer.Reset(wait)
			}
			fire = timer.C
		}
	}
}

func fallbackReason(err error) string {
	var tm errTooMany
	if errors.As(err, &tm) {
		return tm.reason
	}
	return "watching for changes failed (" + err.Error() + ")"
}

// addTree watches dir and every folder under it, skipping skipDirs' insides
// and never following a symlink. found, when set, is told each entry on the
// way: a folder that just appeared may have gained entries before its watch
// was in place, and those would otherwise go unseen.
func (n *notifier) addTree(dir string, found func(path string, dir bool)) error {
	if len(n.wds) >= n.a.MaxDirs {
		return errTooMany{fmt.Sprintf("the folder has more than %d subfolders to watch", n.a.MaxDirs)}
	}
	mask := uint32(watchMask)
	if dir != n.a.Root {
		// The root may be a symlink to a folder; nothing under it is followed.
		mask |= unix.IN_DONT_FOLLOW
	}
	wd, err := unix.InotifyAddWatch(n.fd, dir, mask)
	switch {
	case errors.Is(err, unix.ENOSPC):
		return errTooMany{"the Machine's limit on watched folders (fs.inotify.max_user_watches) was reached"}
	case errors.Is(err, unix.EACCES), errors.Is(err, unix.ENOENT), errors.Is(err, unix.ENOTDIR), errors.Is(err, unix.ELOOP):
		// Unreadable to this user, or gone already: nothing here to watch.
		return nil
	case err != nil:
		return err
	}
	n.wds[wd] = dir
	n.paths[dir] = wd
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, de := range des {
		p := filepath.Join(dir, de.Name())
		if found != nil {
			found(p, de.IsDir())
		}
		if de.IsDir() && !skipDirs[de.Name()] {
			if err := n.addTree(p, found); err != nil {
				return err
			}
		}
	}
	return nil
}

// removeTree forgets the watches on dir and under it: a folder moved out of
// the tree (or within it, before its new name is watched) would otherwise go
// on reporting under its old path.
func (n *notifier) removeTree(dir string) {
	prefix := dir + string(filepath.Separator)
	for p, wd := range n.paths {
		if p == dir || strings.HasPrefix(p, prefix) {
			_, _ = unix.InotifyRmWatch(n.fd, uint32(wd))
			delete(n.paths, p)
			delete(n.wds, wd)
		}
	}
}

// handle adds a read's worth of kernel events to the pending burst. It
// reports whether the root itself went, and errTooMany past the limit.
func (n *notifier) handle(b []byte) (rootGone bool, err error) {
	for len(b) >= unix.SizeofInotifyEvent {
		wd := int(int32(binary.NativeEndian.Uint32(b[0:4])))
		mask := binary.NativeEndian.Uint32(b[4:8])
		nameLen := int(binary.NativeEndian.Uint32(b[12:16]))
		end := unix.SizeofInotifyEvent + nameLen
		if end > len(b) {
			break
		}
		name := strings.TrimRight(string(b[unix.SizeofInotifyEvent:end]), "\x00")
		b = b[end:]

		if mask&unix.IN_Q_OVERFLOW != 0 {
			// Events were lost: catch up on folders that appeared unwatched,
			// and have the tree listed again.
			if err := n.addMissing(n.a.Root); err != nil {
				return false, err
			}
			n.p.add(n.a.Root, Rescan, true)
			continue
		}
		dir, ok := n.wds[wd]
		if !ok {
			continue
		}
		if mask&unix.IN_IGNORED != 0 {
			delete(n.wds, wd)
			if n.paths[dir] == wd {
				delete(n.paths, dir)
			}
			continue
		}
		if mask&(unix.IN_DELETE_SELF|unix.IN_MOVE_SELF) != 0 {
			if dir == n.a.Root {
				return true, nil
			}
			continue // its parent reports it
		}
		if name == "" {
			continue // the folder's own change; its parent reports that too
		}
		p := filepath.Join(dir, name)
		isDir := mask&unix.IN_ISDIR != 0
		switch {
		case mask&(unix.IN_CREATE|unix.IN_MOVED_TO) != 0:
			n.p.add(p, Created, isDir)
			if isDir && !skipDirs[name] {
				if err := n.addTree(p, func(q string, d bool) { n.p.add(q, Created, d) }); err != nil {
					return false, err
				}
			}
		case mask&(unix.IN_DELETE|unix.IN_MOVED_FROM) != 0:
			n.p.add(p, Deleted, isDir)
			if isDir {
				n.removeTree(p)
			}
		default:
			n.p.add(p, Changed, isDir)
		}
	}
	return false, nil
}

// addMissing watches any folder under dir that has no watch yet.
func (n *notifier) addMissing(dir string) error {
	if _, ok := n.paths[dir]; !ok {
		return n.addTree(dir, nil)
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, de := range des {
		if de.IsDir() && !skipDirs[de.Name()] {
			if err := n.addMissing(filepath.Join(dir, de.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// pending gathers a burst of raw events per path; flush decides, from what is
// on disk by then, what each path's burst amounted to.
type pending struct {
	order []string
	first map[string]string // the first kind seen for a path in this burst
	dir   map[string]bool   // what the kernel said the path was, for one now gone
}

func (p *pending) add(path, kind string, dir bool) {
	if p.first == nil {
		p.first, p.dir = make(map[string]string), make(map[string]bool)
	}
	p.dir[path] = p.dir[path] || dir
	if _, ok := p.first[path]; ok {
		return
	}
	p.first[path] = kind
	p.order = append(p.order, path)
}

func (p *pending) empty() bool { return len(p.order) == 0 }

// flush turns the burst into changes: a path that is gone was Deleted (or never
// worth mentioning, if it also appeared in this burst, like an editor's temp
// file); one that appeared is Created; anything else there now Changed.
func (p *pending) flush() []Change {
	var out []Change
	for _, path := range p.order {
		first := p.first[path]
		if first == Rescan {
			out = append(out, Change{Kind: Rescan, Path: path, Dir: true})
			continue
		}
		fi, err := os.Lstat(path)
		switch {
		case err != nil && first == Created:
		case err != nil:
			out = append(out, Change{Kind: Deleted, Path: path, Dir: p.dir[path]})
		case first == Created:
			out = append(out, Change{Kind: Created, Path: path, Dir: fi.IsDir()})
		default:
			out = append(out, Change{Kind: Changed, Path: path, Dir: fi.IsDir()})
		}
	}
	p.order, p.first, p.dir = nil, nil, nil
	return out
}
