//go:build !linux

package files

import "context"

// watchNotify has no inotify to use off Linux: the watch polls.
func watchNotify(context.Context, WatchArgs, func(WatchEvent) error) string {
	return "live changes need Linux's inotify"
}
