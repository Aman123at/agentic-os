// Package catalogue is the model catalogue (PLAN.md §18 item 16): which models
// System Settings offers, which reasoning efforts each one accepts, and what each
// costs. It ships as data — GET /v1/models reports nothing about reasoning
// effort, and OpenAI publishes the per-model mapping as prose only, so the
// catalogue cannot be built at runtime and is seeded into a user-editable
// /var/lib/aos/models.yaml exactly as internal/usage seeds prices.yaml.
//
// The catalogue is open, not a strict allow-list: a model absent from the file
// stays usable, falling back to the full wire effort enum with its cost
// untracked, so a new OpenAI model does not need a release of AOS before it can
// be selected.
package catalogue

import (
	"fmt"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultModels is models.yaml as aosd first writes it. The efforts, defaults and
// prices were read from OpenAI's documentation on 2026-09-17 (see
// .scratch/native-linux-release/research/model-catalogue.md for the sources).
const DefaultModels = `# The model catalogue for aosd: which models System Settings offers, which
# reasoning efforts each one accepts, and what each costs.
#
# Read from OpenAI's documentation on 2026-09-17:
#   efforts + defaults  https://developers.openai.com/api/docs/models/<id>
#   prices              https://developers.openai.com/api/docs/pricing
#
# OpenAI does not publish this mapping in machine-readable form, and
# GET /v1/models does not report effort support, so this file is the source of
# truth. Edit it when OpenAI changes prices or ships a model:
#   docker compose exec aos nano /var/lib/aos/models.yaml
# aosd re-reads it before it offers the Settings dropdowns.
#
# The catalogue is open, not an allow-list: a model not listed here stays usable,
# with the full effort list and its cost untracked.
#
# efforts: the values this model accepts. Sending any other value is an API
#   error (HTTP 400), not a silent clamp, so System Settings offers only these.
# default_effort: what OpenAI uses when the request omits reasoning.effort.
# price: USD per 1M tokens, Standard tier, prompts up to 272K input tokens.
# long_context: USD per 1M tokens once a prompt exceeds 272K input tokens.
#   OpenAI applies these to the WHOLE request, not just the excess.

version: 1

models:
  gpt-6-astra:
    label: GPT-6 Astra
    efforts: [low, medium, high, xhigh, max]   # note: no "none", no "minimal"
    default_effort: ""                          # not documented by OpenAI
    context_window: 1050000
    price:        { input: 10.00, cached_input: 1.00, output: 50.00 }
    long_context: { input: 20.00, cached_input: 2.00, output: 75.00 }

  gpt-5.6-sol:
    label: GPT-5.6 Sol
    aliases: [gpt-5.6]
    efforts: [none, low, medium, high, xhigh, max]
    default_effort: medium
    context_window: 1050000
    price:        { input: 4.00, cached_input: 0.40, output: 20.00 }
    long_context: { input: 8.00, cached_input: 0.80, output: 30.00 }

  gpt-5.6-terra:
    label: GPT-5.6 Terra
    default: true                              # aosd's built-in default model
    efforts: [none, low, medium, high, xhigh, max]
    default_effort: medium
    context_window: 1050000
    price:        { input: 2.00, cached_input: 0.20, output: 12.00 }
    long_context: { input: 4.00, cached_input: 0.40, output: 18.00 }

  gpt-5.6-luna:
    label: GPT-5.6 Luna
    efforts: [none, low, medium, high, xhigh, max]
    default_effort: medium
    context_window: 1050000
    price:        { input: 0.20, cached_input: 0.02, output: 1.20 }
    long_context: { input: 0.40, cached_input: 0.04, output: 1.80 }

  gpt-5.5:
    label: GPT-5.5
    efforts: [none, low, medium, high, xhigh]  # note: no "max"
    default_effort: medium
    context_window: 1050000
    price:        { input: 5.00, cached_input: 0.50, output: 30.00 }
    long_context: { input: 10.00, cached_input: 1.00, output: 45.00 }

  gpt-5.4:
    label: GPT-5.4
    efforts: [none, low, medium, high, xhigh]  # note: no "max"
    default_effort: none                       # not medium, unlike the 5.6 family
    context_window: 1050000
    price:        { input: 2.50, cached_input: 0.25, output: 15.00 }
    long_context: { input: 5.00, cached_input: 0.50, output: 22.50 }
`

// BuiltinModel is the model aosd defaults to when models.yaml names none.
const BuiltinModel = "gpt-5.6-terra"

// WireEfforts is every reasoning effort the OpenAI wire protocol accepts, newest
// first. A model absent from the catalogue is offered all of these, because the
// catalogue cannot know what an unlisted model supports (openness over a strict
// allow-list). A listed model is offered only its own, narrower set.
var WireEfforts = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

var wireEffort = func() map[string]bool {
	m := map[string]bool{}
	for _, e := range WireEfforts {
		m[e] = true
	}
	return m
}()

// Price is what a model costs, in USD per million tokens: the same three keys
// internal/usage already parses from prices.yaml.
type Price struct {
	Input       float64 `yaml:"input"`
	CachedInput float64 `yaml:"cached_input"`
	Output      float64 `yaml:"output"`
}

// Model is one catalogue entry.
type Model struct {
	ID            string   `yaml:"-"`
	Label         string   `yaml:"label"`
	Aliases       []string `yaml:"aliases"`
	Default       bool     `yaml:"default"`
	Efforts       []string `yaml:"efforts"`
	DefaultEffort string   `yaml:"default_effort"`
	ContextWindow int      `yaml:"context_window"`
	Price         Price    `yaml:"price"`
	LongContext   Price    `yaml:"long_context"`
}

// Catalogue is the parsed models.yaml.
type Catalogue struct {
	Version int
	// Models keeps the entries in file order, so the dropdown reads as written.
	Models []Model
	byName map[string]*Model
}

type rawCatalogue struct {
	Version int       `yaml:"version"`
	Models  yaml.Node `yaml:"models"`
}

// Parse reads models.yaml. It refuses an unknown version or an effort outside the
// wire enum, so a typo in the file is caught at load rather than as an HTTP 400
// on the first Task.
func Parse(data []byte) (*Catalogue, error) {
	var raw rawCatalogue
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("models.yaml: %w", err)
	}
	if raw.Version != 1 {
		return nil, fmt.Errorf("models.yaml: version %d is not supported (this build reads version 1)", raw.Version)
	}
	c := &Catalogue{Version: raw.Version, byName: map[string]*Model{}}
	if raw.Models.Kind == 0 {
		return c, nil
	}
	if raw.Models.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("models.yaml: models is a mapping of model id to its settings")
	}
	seenDefault := ""
	for i := 0; i+1 < len(raw.Models.Content); i += 2 {
		id := raw.Models.Content[i].Value
		var m Model
		if err := raw.Models.Content[i+1].Decode(&m); err != nil {
			return nil, fmt.Errorf("models.yaml: %s: %w", id, err)
		}
		m.ID = id
		for _, e := range m.Efforts {
			if !wireEffort[e] {
				return nil, fmt.Errorf("models.yaml: %s: %q is not a reasoning effort", id, e)
			}
		}
		if m.Default {
			if seenDefault != "" {
				return nil, fmt.Errorf("models.yaml: %s and %s are both marked default", seenDefault, id)
			}
			seenDefault = id
		}
		c.Models = append(c.Models, m)
	}
	for i := range c.Models {
		m := &c.Models[i]
		c.byName[m.ID] = m
		for _, a := range m.Aliases {
			c.byName[a] = m
		}
	}
	return c, nil
}

// Lookup returns the entry for model: its own id or an alias, then the longest
// entry its name continues with a dash (a dated snapshot such as
// gpt-5.5-2026-04-23), matching prices.Lookup.
func (c *Catalogue) Lookup(model string) (Model, bool) {
	if c == nil {
		return Model{}, false
	}
	if m, ok := c.byName[model]; ok {
		return *m, true
	}
	best := ""
	for name := range c.byName {
		if len(name) > len(best) && len(model) > len(name) && model[:len(name)+1] == name+"-" {
			best = name
		}
	}
	if best == "" {
		return Model{}, false
	}
	return *c.byName[best], true
}

// Efforts returns the reasoning efforts model accepts: its own list if the
// catalogue lists it, otherwise the full wire enum (the model is unknown, so AOS
// does not narrow what the user may send).
func (c *Catalogue) Efforts(model string) []string {
	if m, ok := c.Lookup(model); ok && len(m.Efforts) > 0 {
		return append([]string(nil), m.Efforts...)
	}
	return append([]string(nil), WireEfforts...)
}

// Accepts reports whether model accepts effort. An empty effort is always
// accepted: it means "let OpenAI use the model's default".
func (c *Catalogue) Accepts(model, effort string) bool {
	if effort == "" {
		return true
	}
	for _, e := range c.Efforts(model) {
		if e == effort {
			return true
		}
	}
	return false
}

// DefaultModel is the model marked default in the file, or the built-in default.
func (c *Catalogue) DefaultModel() string {
	if c != nil {
		for _, m := range c.Models {
			if m.Default {
				return m.ID
			}
		}
	}
	return BuiltinModel
}

// File is models.yaml, re-read whenever it changes on disk.
type File struct {
	Path string

	mu   sync.Mutex
	mod  time.Time
	size int64
	cat  *Catalogue
	err  error
}

// Catalogue returns the file's parsed catalogue; a missing file has none.
func (f *File) Catalogue() (*Catalogue, error) {
	if f == nil {
		return nil, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	fi, err := os.Stat(f.Path)
	if err != nil {
		f.cat, f.err, f.mod = nil, nil, time.Time{}
		if !os.IsNotExist(err) {
			f.err = err
		}
		return f.cat, f.err
	}
	if !fi.ModTime().Equal(f.mod) || fi.Size() != f.size {
		f.mod, f.size = fi.ModTime(), fi.Size()
		data, err := os.ReadFile(f.Path)
		if err == nil {
			f.cat, f.err = Parse(data)
		} else {
			f.cat, f.err = nil, err
		}
	}
	return f.cat, f.err
}

// Efforts returns the efforts model accepts, reading the file. A broken or
// missing file falls back to the full wire enum, so a bad edit never blocks the
// user from setting an effort.
func (f *File) Efforts(model string) []string {
	cat, err := f.Catalogue()
	if err != nil || cat == nil {
		return append([]string(nil), WireEfforts...)
	}
	return cat.Efforts(model)
}
