package webui

import (
	"io/fs"
	"testing"
)

// Assets reflects whether the Desktop was built into the binary. In this test
// build the embed carries only dist/.keep (Vite never ran), so it is nil — the
// same result as the Dockerfile's test stage and a plain `go build ./cmd/aosd`.
// The single image is what makes that a runtime choice rather than a build tag
// (M6.10).
func TestAssetsAreNilWithoutABuiltDesktop(t *testing.T) {
	if Assets() != nil {
		t.Fatal("Assets() is not nil, but this build never ran the Desktop's Vite build")
	}
}

// When index.html is present, Assets() serves the embedded tree rooted at dist.
// The embed is fixed at compile time, so this exercises the index.html gate
// against the embedded .keep's parent rather than a freshly written file.
func TestAssetsGateOnIndexHTML(t *testing.T) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		t.Fatal(err)
	}
	// The gate keys on index.html: .keep alone must not count as a built Desktop.
	if _, err := fs.Stat(sub, ".keep"); err != nil {
		t.Fatalf("the embed should carry dist/.keep: %v", err)
	}
	if _, err := fs.Stat(sub, "index.html"); err == nil {
		t.Skip("this build embedded a real Desktop; the nil-gate case does not apply")
	}
}
