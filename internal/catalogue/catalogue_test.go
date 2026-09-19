package catalogue

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestDefaultModelsParse guards the seed: it must parse, name a default model and
// carry the corrected per-model effort lists — max present, minimal gone.
func TestDefaultModelsParse(t *testing.T) {
	c, err := Parse([]byte(DefaultModels))
	if err != nil {
		t.Fatalf("the seed catalogue does not parse: %v", err)
	}
	if c.DefaultModel() != BuiltinModel {
		t.Errorf("default model %q, want %q", c.DefaultModel(), BuiltinModel)
	}
	// The M6.16 fix: the default model offers max and no longer minimal.
	terra := c.Efforts("gpt-5.6-terra")
	if !slices.Contains(terra, "max") {
		t.Errorf("gpt-5.6-terra should offer max: %v", terra)
	}
	if slices.Contains(terra, "minimal") {
		t.Errorf("gpt-5.6-terra should not offer the legacy minimal: %v", terra)
	}
	// The sets genuinely differ per model: gpt-5.5 has no max.
	if slices.Contains(c.Efforts("gpt-5.5"), "max") {
		t.Errorf("gpt-5.5 should not offer max: %v", c.Efforts("gpt-5.5"))
	}
	// Astra has neither none nor minimal.
	astra := c.Efforts("gpt-6-astra")
	if slices.Contains(astra, "none") || slices.Contains(astra, "minimal") {
		t.Errorf("gpt-6-astra should offer neither none nor minimal: %v", astra)
	}
}

// TestLookup covers exact id, alias and the dash-prefix snapshot fallback.
func TestLookup(t *testing.T) {
	c, err := Parse([]byte(DefaultModels))
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := c.Lookup("gpt-5.6-terra"); !ok || m.ID != "gpt-5.6-terra" {
		t.Errorf("exact lookup: %+v %v", m, ok)
	}
	if m, ok := c.Lookup("gpt-5.6"); !ok || m.ID != "gpt-5.6-sol" {
		t.Errorf("alias gpt-5.6 should route to sol: %+v %v", m, ok)
	}
	if m, ok := c.Lookup("gpt-5.5-2026-04-23"); !ok || m.ID != "gpt-5.5" {
		t.Errorf("dated snapshot should fall back to gpt-5.5: %+v %v", m, ok)
	}
	if _, ok := c.Lookup("gpt-9-nonesuch"); ok {
		t.Errorf("an unlisted model should not be found")
	}
}

// TestEffortsOpenForUnknown: an unlisted model is offered the full wire enum, not
// narrowed — the catalogue is open, not an allow-list.
func TestEffortsOpenForUnknown(t *testing.T) {
	c, _ := Parse([]byte(DefaultModels))
	got := c.Efforts("some-future-model")
	if !slices.Equal(got, WireEfforts) {
		t.Errorf("unknown model efforts %v, want the wire enum %v", got, WireEfforts)
	}
	if c.Accepts("gpt-6-astra", "none") {
		t.Errorf("astra must reject none (it is an HTTP 400)")
	}
	if !c.Accepts("gpt-6-astra", "") {
		t.Errorf("an empty effort means the model default and is always accepted")
	}
	if !c.Accepts("gpt-5.6-terra", "max") {
		t.Errorf("terra should accept max")
	}
}

// TestParseRejectsBadFiles: an unknown version, a bad effort or two defaults are
// caught at load, not deferred to a task failure.
func TestParseRejectsBadFiles(t *testing.T) {
	for name, data := range map[string]string{
		"unknown version": "version: 2\nmodels: {}\n",
		"bad effort":      "version: 1\nmodels:\n  m:\n    efforts: [medium, turbo]\n",
		"two defaults":    "version: 1\nmodels:\n  a:\n    default: true\n  b:\n    default: true\n",
	} {
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// TestFileReloads: the File re-reads models.yaml when it changes, and a missing
// file falls back to the wire enum rather than erroring the caller.
func TestFileReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.yaml")
	f := &File{Path: path}
	if got := f.Efforts("gpt-5.6-terra"); !slices.Equal(got, WireEfforts) {
		t.Errorf("missing file should fall back to the wire enum: %v", got)
	}
	if err := os.WriteFile(path, []byte(DefaultModels), 0o600); err != nil {
		t.Fatal(err)
	}
	got := f.Efforts("gpt-5.6-terra")
	if slices.Contains(got, "minimal") || !slices.Contains(got, "max") {
		t.Errorf("after seeding, terra efforts should be the corrected list: %v", got)
	}
}
