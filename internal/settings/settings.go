// Package settings is the configuration in force (PLAN.md §6.4). The single
// source of truth is /etc/aos/config.yml (ADR-0010): a value saved from System
// Settings or `aos config set` is written back into the file, so a restart
// re-reads it and never silently reverts a change made at runtime. The file
// replaces the SQLite `settings` table that held these values until M6.
//
// Runtime keys change while aosd runs; startup-only keys (such as base_url,
// which decides where the API key is sent) are accepted and written but reported
// as *(pending restart)* until the Daemon is restarted.
package settings

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/Aman123at/agentic-os/internal/config"
	"github.com/Aman123at/agentic-os/internal/policy"
)

// Values are the runtime settings in force.
type Values struct {
	Model, ReasoningEffort string
	Autonomy               policy.Autonomy
	MaxTasks, MaxRetries   int
	// Cost Limits in USD; 0 means none (PLAN.md §8.4).
	TaskCostLimit, DailyCostLimit  float64
	TrashRetentionDays, TrashMaxGB int
}

// Source is where a setting's value comes from.
type Source string

const (
	FromDefault  Source = "default"
	FromEnv      Source = "env"
	FromSettings Source = "settings"
)

// Setting is one setting as System Settings and `aos config` show it.
type Setting struct {
	Key   string
	Value string
	// Env is the environment variable that also sets it (Compose bootstrap).
	Env    string
	Source Source
	// Fallback is the value without the saved one: the environment's, or the default.
	Fallback string
	// PendingRestart is set for a startup-only key whose saved value is not yet
	// the one in force — a restart will apply it.
	PendingRestart bool
}

// ErrUnknown is returned for a key that is not a setting.
var ErrUnknown = errors.New("not a setting AOS knows")

// ---- Runtime keys: changeable while aosd runs. ----

type field struct {
	key, env, doc string
	get           func(Values) string
	set           func(*Values, string) error
}

var fields = []field{
	{"model", "OPENAI_MODEL", "The model every Task runs on, such as gpt-5.6-terra.",
		func(v Values) string { return v.Model },
		func(v *Values, s string) error {
			if s == "" || len(s) > 100 || strings.ContainsAny(s, " \t\r\n") {
				return errors.New("the model is a name such as gpt-5.6-terra")
			}
			v.Model = s
			return nil
		}},
	{"reasoning_effort", "OPENAI_REASONING_EFFORT", "How hard the model thinks: none, minimal, low, medium, high or xhigh.",
		func(v Values) string { return v.ReasoningEffort },
		func(v *Values, s string) error {
			switch s {
			case "none", "minimal", "low", "medium", "high", "xhigh":
				v.ReasoningEffort = s
				return nil
			}
			return errors.New("the reasoning effort is none, minimal, low, medium, high or xhigh")
		}},
	{"autonomy", "AOS_AUTONOMY", "How bold Agents are: auto, confirm-risky or confirm-all.",
		func(v Values) string { return AutonomyName(v.Autonomy) },
		func(v *Values, s string) error {
			a, ok := ParseAutonomy(s)
			if !ok {
				return errors.New("the Autonomy level is auto, confirm-risky or confirm-all")
			}
			v.Autonomy = a
			return nil
		}},
	{"max_tasks", "AOS_MAX_TASKS", "How many Tasks run at once (1-16).",
		func(v Values) string { return strconv.Itoa(v.MaxTasks) },
		intIn(1, 16, "the number of Tasks running at once", func(v *Values, n int) { v.MaxTasks = n })},
	{"max_retries", "AOS_MAX_RETRIES", "How many times a model call is retried (0-20).",
		func(v Values) string { return strconv.Itoa(v.MaxRetries) },
		intIn(0, 20, "the number of retries", func(v *Values, n int) { v.MaxRetries = n })},
	{"task_cost_limit_usd", "AOS_TASK_COST_LIMIT_USD", "Per-Task Cost Limit in USD; 0 means none.",
		func(v Values) string { return money(v.TaskCostLimit) },
		usd("the per-Task Cost Limit", func(v *Values, n float64) { v.TaskCostLimit = n })},
	{"daily_cost_limit_usd", "AOS_DAILY_COST_LIMIT_USD", "Daily Cost Limit in USD; 0 means none.",
		func(v Values) string { return money(v.DailyCostLimit) },
		usd("the daily Cost Limit", func(v *Values, n float64) { v.DailyCostLimit = n })},
	{"trash_retention_days", "AOS_TRASH_RETENTION_DAYS", "How long the Trash keeps an item, in days (1-3650).",
		func(v Values) string { return strconv.Itoa(v.TrashRetentionDays) },
		intIn(1, 3650, "the Trash retention in days", func(v *Values, n int) { v.TrashRetentionDays = n })},
	{"trash_max_gb", "AOS_TRASH_MAX_GB", "Trash size cap in GB (1-1024).",
		func(v Values) string { return strconv.Itoa(v.TrashMaxGB) },
		intIn(1, 1024, "the Trash size cap in GB", func(v *Values, n int) { v.TrashMaxGB = n })},
}

// ---- Startup-only keys: written to the file, applied on the next start. ----

type startupField struct {
	key, env, doc string
	validate      func(string) error
	apply         func(*config.Config, string) error
	current       func(config.Config) string
}

var startupFields = []startupField{
	{key: "base_url", env: "OPENAI_BASE_URL",
		doc: "OpenAI-compatible API base URL; empty uses OpenAI's own. Decides where the API key is sent.",
		validate: func(s string) error {
			if s == "" {
				return nil
			}
			if len(s) > 2048 || !(strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")) {
				return errors.New("the base URL is an http(s) URL, or empty for OpenAI's own")
			}
			return nil
		},
		apply:   func(c *config.Config, s string) error { c.BaseURL = s; return nil },
		current: func(c config.Config) string { return c.BaseURL }},
	{key: "require_landlock", env: "AOS_REQUIRE_LANDLOCK",
		doc: "Refuse to start when the kernel has no Landlock, rather than run Agents under policy checks only.",
		validate: func(s string) error {
			if s == "true" || s == "false" {
				return nil
			}
			return errors.New("require_landlock is true or false")
		},
		apply:   func(c *config.Config, s string) error { c.RequireLandlock = s == "true"; return nil },
		current: func(c config.Config) string { return strconv.FormatBool(c.RequireLandlock) }},
}

func intIn(lo, hi int, what string, put func(*Values, int)) func(*Values, string) error {
	return func(v *Values, s string) error {
		n, err := strconv.Atoi(s)
		if err != nil || n < lo || n > hi {
			return fmt.Errorf("%s is a whole number from %d to %d", what, lo, hi)
		}
		put(v, n)
		return nil
	}
}

func usd(what string, put func(*Values, float64)) func(*Values, string) error {
	return func(v *Values, s string) error {
		n, err := strconv.ParseFloat(strings.TrimPrefix(s, "$"), 64)
		if err != nil || n < 0 || n > 100000 {
			return fmt.Errorf("%s is an amount in USD, or 0 for none", what)
		}
		put(v, n)
		return nil
	}
}

func money(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }

// AutonomyName is the name AOS_AUTONOMY uses.
func AutonomyName(a policy.Autonomy) string {
	switch a {
	case policy.Auto:
		return "auto"
	case policy.ConfirmAll:
		return "confirm-all"
	}
	return "confirm-risky"
}

// ParseAutonomy reads an AOS_AUTONOMY name.
func ParseAutonomy(s string) (policy.Autonomy, bool) {
	switch s {
	case "auto":
		return policy.Auto, true
	case "confirm-risky":
		return policy.ConfirmRisky, true
	case "confirm-all":
		return policy.ConfirmAll, true
	}
	return 0, false
}

func lookup(key string) (field, bool) {
	for _, f := range fields {
		if f.key == key {
			return f, true
		}
	}
	return field{}, false
}

func lookupStartup(key string) (startupField, bool) {
	for _, sf := range startupFields {
		if sf.key == key {
			return sf, true
		}
	}
	return startupField{}, false
}

// Store keeps the settings in force, reading them from config.yml over the
// environment-and-defaults base, and writing each change back to the file.
type Store struct {
	file *configFile

	// base is the environment over the built-in defaults for runtime keys;
	// fromEnv names the environment variables that were set (Compose bootstrap).
	base    Values
	fromEnv map[string]bool

	// For startup-only keys: the value the Daemon started with, and the fallback
	// (environment or default) it would use without a file entry.
	startupInForce map[string]string
	startupBase    map[string]string

	mu       sync.RWMutex
	cur      Values
	saved    map[string]string // runtime keys present in the file
	onChange []func(Values)
}

// Open loads config.yml at path over base (the runtime environment-or-defaults)
// and overlays the file's startup-only keys onto cfg, so a restart honours them.
// A malformed file or an unknown key refuses the start (ADR-0010). On a fresh
// Machine (no file yet) it generates the file from the values in force, so the
// native and Compose installs share one configuration system.
func Open(path string, base Values, cfg *config.Config) (*Store, error) {
	file, exists, err := readConfigFile(path)
	if err != nil {
		return nil, err
	}
	s := &Store{
		file:           file,
		base:           base,
		fromEnv:        cfg.Set,
		saved:          map[string]string{},
		startupInForce: map[string]string{},
		startupBase:    map[string]string{},
	}
	// Validate and load every key in the file. Unknown or invalid → refuse start.
	// An empty value means "unset": the environment or the built-in default
	// applies, and the generated file writes empty keys (base_url, reasoning_effort)
	// so they are documented without forcing a value.
	for _, key := range file.keys() {
		val, _ := file.get(key)
		switch {
		case isRuntime(key):
			if val == "" {
				continue
			}
			f, _ := lookup(key)
			if err := f.set(&Values{}, val); err != nil {
				return nil, fmt.Errorf("%s: %s = %q: %w", file.path, key, val, err)
			}
			s.saved[key] = val
		case isStartup(key):
			if val == "" {
				continue
			}
			sf, _ := lookupStartup(key)
			if err := sf.validate(val); err != nil {
				return nil, fmt.Errorf("%s: %s = %q: %w", file.path, key, val, err)
			}
		default:
			return nil, fmt.Errorf("%s: %q: %w", file.path, key, ErrUnknown)
		}
	}
	// Startup keys: record the fallback (environment or default), then overlay the
	// file's value onto cfg. Done after validation, so cfg is only touched with
	// values that passed.
	for _, sf := range startupFields {
		fallback := sf.current(*cfg)
		s.startupBase[sf.key] = fallback
		if v, ok := file.get(sf.key); ok && v != "" {
			if err := sf.apply(cfg, v); err != nil {
				return nil, err
			}
			s.startupInForce[sf.key] = v
		} else {
			s.startupInForce[sf.key] = fallback
		}
	}
	s.cur = s.compute()
	if !exists {
		s.generate()
		if err := s.file.writeAtomic(); err != nil {
			return nil, fmt.Errorf("writing %s: %w", file.path, err)
		}
	}
	return s, nil
}

func isRuntime(key string) bool { _, ok := lookup(key); return ok }
func isStartup(key string) bool { _, ok := lookupStartup(key); return ok }

// compute applies the saved runtime values over base. Callers hold s.mu, or own s.
func (s *Store) compute() Values {
	v := s.base
	for _, f := range fields {
		if saved, ok := s.saved[f.key]; ok {
			_ = f.set(&v, saved) // validated when it was loaded or saved
		}
	}
	return v
}

// generate builds a fresh, self-documenting config.yml from the values in force.
func (s *Store) generate() {
	doc := yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	doc.HeadComment = "Agentic OS configuration (ADR-0010).\n" +
		"The single source of truth for every setting that is not a secret.\n" +
		"Managed by `aos config` and System Settings; comments and hand-edits are kept.\n" +
		"Secrets (the OpenAI key, the JWT signing key) live in /var/lib/aos/, never here."
	m := doc.Content[0]
	add := func(key, value, doc string) {
		m.Content = append(m.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key, HeadComment: doc},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: scalarTag(value), Value: value})
	}
	for _, f := range fields {
		add(f.key, f.get(s.cur), f.doc)
	}
	for _, sf := range startupFields {
		add(sf.key, s.startupInForce[sf.key], sf.doc+" (startup-only)")
	}
	s.file.doc = doc
}

// Values returns the runtime settings in force.
func (s *Store) Values() Values {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// List describes every setting: the runtime keys first, then the startup-only ones.
func (s *Store) List() []Setting {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Setting, 0, len(fields)+len(startupFields))
	for _, f := range fields {
		out = append(out, s.describe(f))
	}
	for _, sf := range startupFields {
		out = append(out, s.describeStartup(sf))
	}
	return out
}

func (s *Store) describe(f field) Setting {
	// The file may hold a key at its environment-or-default value — the first
	// start writes every key so the file is self-documenting (generate). Only a
	// value that differs from that base counts as saved by the user; otherwise the
	// underlying environment or default is the source.
	fallback := f.get(s.base)
	st := Setting{Key: f.key, Env: f.env, Value: f.get(s.cur), Fallback: fallback, Source: FromDefault}
	switch saved, inFile := s.saved[f.key]; {
	case inFile && saved != fallback:
		st.Source = FromSettings
	case s.fromEnv[f.env]:
		st.Source = FromEnv
	}
	return st
}

func (s *Store) describeStartup(sf startupField) Setting {
	fallback := s.startupBase[sf.key]
	st := Setting{Key: sf.key, Env: sf.env, Value: fallback, Fallback: fallback, Source: FromDefault}
	if v, ok := s.file.get(sf.key); ok && v != "" {
		st.Value = v
		st.PendingRestart = v != s.startupInForce[sf.key]
		if v != fallback {
			st.Source = FromSettings
			return st
		}
	} else {
		st.PendingRestart = fallback != s.startupInForce[sf.key]
	}
	if st.Source == FromDefault && s.fromEnv[sf.env] {
		st.Source = FromEnv
	}
	return st
}

// Set saves a setting, writing it back to config.yml. An empty value removes the
// saved one, so the environment's value, or the default, applies again. A
// runtime key takes effect at once; a startup-only key is written and reported as
// pending a restart.
func (s *Store) Set(key, value string) (Setting, error) {
	value = strings.TrimSpace(value)
	if f, ok := lookup(key); ok {
		return s.setRuntime(f, key, value)
	}
	if sf, ok := lookupStartup(key); ok {
		return s.setStartup(sf, key, value)
	}
	return Setting{}, fmt.Errorf("%q: %w", key, ErrUnknown)
}

func (s *Store) setRuntime(f field, key, value string) (Setting, error) {
	if value != "" {
		if err := f.set(&Values{}, value); err != nil {
			return Setting{}, err
		}
	}
	s.mu.Lock()
	if err := s.write(key, value); err != nil {
		s.mu.Unlock()
		return Setting{}, err
	}
	if value == "" {
		delete(s.saved, key)
	} else {
		s.saved[key] = value
	}
	s.cur = s.compute()
	cur, st, listeners := s.cur, s.describe(f), append([]func(Values){}, s.onChange...)
	s.mu.Unlock()
	for _, fn := range listeners {
		fn(cur)
	}
	return st, nil
}

func (s *Store) setStartup(sf startupField, key, value string) (Setting, error) {
	if value != "" {
		if err := sf.validate(value); err != nil {
			return Setting{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.write(key, value); err != nil {
		return Setting{}, err
	}
	return s.describeStartup(sf), nil
}

// write persists one change to config.yml, rolling the in-file node back if the
// atomic write fails, so the file on disk and the tree in memory stay in step.
// Callers hold s.mu.
func (s *Store) write(key, value string) error {
	old, had := s.file.get(key)
	if value == "" {
		s.file.remove(key)
	} else {
		s.file.set(key, value)
	}
	if err := s.file.writeAtomic(); err != nil {
		if had {
			s.file.set(key, old)
		} else {
			s.file.remove(key)
		}
		return fmt.Errorf("saving %s: %w", key, err)
	}
	return nil
}

// OnChange calls fn with the new settings after each runtime change.
func (s *Store) OnChange(fn func(Values)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = append(s.onChange, fn)
}
