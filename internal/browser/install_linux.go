package browser

import "syscall"

// freeBytes reports the space available to a non-root process at dir. Install
// runs as root (it writes /opt), so the distinction rarely matters, but Bavail
// is the honest figure either way.
func freeBytes(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
