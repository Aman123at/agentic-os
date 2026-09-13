//go:build !desktop

package webui

import "io/fs"

// Assets returns the Desktop's files, or nil when this binary has no Desktop.
func Assets() fs.FS { return nil }
