package webui

import (
	"embed"
	"io/fs"
)

// dist holds the Desktop's built assets. The directory always exists (a tracked
// .keep keeps it in the tree), so this embeds cleanly even in a build that never
// ran the Desktop's Vite build — the Dockerfile's test stage, or a plain
// `go build ./cmd/aosd`. Vite's `npm run build` fills it with index.html and the
// hashed asset bundles; without that step only .keep is here.
//
//go:embed all:dist
var dist embed.FS

// Assets returns the Desktop's files, or nil when this binary was built without
// them (M6.10). The single image always compiles this embed, so Mode is a
// runtime choice, not a build-time one; nil here lets ui Mode print an honest
// "the Desktop was not built" note instead of serving a Desktop that 404s.
func Assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	// index.html is Vite's entry point; its absence means the Desktop was never
	// built into this binary (only .keep is embedded).
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
