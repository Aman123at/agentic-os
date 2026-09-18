// Package settings holds the settings that can change while aosd runs (PLAN.md
// §6.4): a value saved from System Settings wins over the environment, which
// wins over the built-in default. The rest of §6.4 is fixed at startup, and
// OPENAI_BASE_URL stays environment-only because it decides where the API key
// is sent.
package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/Aman123at/agentic-os/internal/policy"
	"github.com/Aman123at/agentic-os/internal/store"
)

// Values are the settings in force.
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

// Setting is one setting as System Settings shows it.
type Setting struct {
	Key   string
	Value string
	// Env is the environment variable that also sets it.
	Env    string
	Source Source
	// Fallback is the value without the saved one: the environment's, or the default.
	Fallback string
}

// ErrUnknown is returned for a key that is not a runtime setting.
var ErrUnknown = errors.New("not a setting that can change while AOS runs")

type field struct {
	key, env string
	get      func(Values) string
	set      func(*Values, string) error
}

var fields = []field{
	{"model", "OPENAI_MODEL",
		func(v Values) string { return v.Model },
		func(v *Values, s string) error {
			if s == "" || len(s) > 100 || strings.ContainsAny(s, " \t\r\n") {
				return errors.New("the model is a name such as gpt-5.6-terra")
			}
			v.Model = s
			return nil
		}},
	{"reasoning_effort", "OPENAI_REASONING_EFFORT",
		func(v Values) string { return v.ReasoningEffort },
		func(v *Values, s string) error {
			switch s {
			case "none", "minimal", "low", "medium", "high", "xhigh":
				v.ReasoningEffort = s
				return nil
			}
			return errors.New("the reasoning effort is none, minimal, low, medium, high or xhigh")
		}},
	{"autonomy", "AOS_AUTONOMY",
		func(v Values) string { return AutonomyName(v.Autonomy) },
		func(v *Values, s string) error {
			a, ok := ParseAutonomy(s)
			if !ok {
				return errors.New("the Autonomy level is auto, confirm-risky or confirm-all")
			}
			v.Autonomy = a
			return nil
		}},
	{"max_tasks", "AOS_MAX_TASKS",
		func(v Values) string { return strconv.Itoa(v.MaxTasks) },
		intIn(1, 16, "the number of Tasks running at once", func(v *Values, n int) { v.MaxTasks = n })},
	{"max_retries", "AOS_MAX_RETRIES",
		func(v Values) string { return strconv.Itoa(v.MaxRetries) },
		intIn(0, 20, "the number of retries", func(v *Values, n int) { v.MaxRetries = n })},
	{"task_cost_limit_usd", "AOS_TASK_COST_LIMIT_USD",
		func(v Values) string { return money(v.TaskCostLimit) },
		usd("the per-Task Cost Limit", func(v *Values, n float64) { v.TaskCostLimit = n })},
	{"daily_cost_limit_usd", "AOS_DAILY_COST_LIMIT_USD",
		func(v Values) string { return money(v.DailyCostLimit) },
		usd("the daily Cost Limit", func(v *Values, n float64) { v.DailyCostLimit = n })},
	{"trash_retention_days", "AOS_TRASH_RETENTION_DAYS",
		func(v Values) string { return strconv.Itoa(v.TrashRetentionDays) },
		intIn(1, 3650, "the Trash retention in days", func(v *Values, n int) { v.TrashRetentionDays = n })},
	{"trash_max_gb", "AOS_TRASH_MAX_GB",
		func(v Values) string { return strconv.Itoa(v.TrashMaxGB) },
		intIn(1, 1024, "the Trash size cap in GB", func(v *Values, n int) { v.TrashMaxGB = n })},
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

// Store keeps the settings in force and saves the user's changes.
type Store struct {
	db *store.DB
	// base is the environment over the built-in defaults; fromEnv names the
	// variables the environment set.
	base    Values
	fromEnv map[string]bool

	mu       sync.RWMutex
	cur      Values
	saved    map[string]string
	onChange []func(Values)
}

// Open loads the saved settings over base. A saved value that no longer
// validates is ignored, so the setting falls back to the environment.
func Open(ctx context.Context, db *store.DB, base Values, fromEnv map[string]bool) (*Store, error) {
	s := &Store{db: db, base: base, fromEnv: fromEnv, saved: map[string]string{}}
	rows, err := db.Read().QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		if f, ok := lookup(k); ok && f.set(&Values{}, v) == nil {
			s.saved[k] = v
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.cur = s.compute()
	return s, nil
}

func lookup(key string) (field, bool) {
	for _, f := range fields {
		if f.key == key {
			return f, true
		}
	}
	return field{}, false
}

// compute applies the saved values over base. Callers hold s.mu, or own s.
func (s *Store) compute() Values {
	v := s.base
	for _, f := range fields {
		if saved, ok := s.saved[f.key]; ok {
			_ = f.set(&v, saved) // validated when it was loaded or saved
		}
	}
	return v
}

// Values returns the settings in force.
func (s *Store) Values() Values {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// List describes every runtime setting.
func (s *Store) List() []Setting {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Setting, 0, len(fields))
	for _, f := range fields {
		out = append(out, s.describe(f))
	}
	return out
}

func (s *Store) describe(f field) Setting {
	st := Setting{Key: f.key, Env: f.env, Value: f.get(s.cur), Fallback: f.get(s.base), Source: FromDefault}
	switch {
	case s.saved[f.key] != "":
		st.Source = FromSettings
	case s.fromEnv[f.env]:
		st.Source = FromEnv
	}
	return st
}

// Set saves a setting; an empty value removes the saved one, so the
// environment's value, or the default, applies again.
func (s *Store) Set(ctx context.Context, key, value string) (Setting, error) {
	f, ok := lookup(key)
	if !ok {
		return Setting{}, fmt.Errorf("%q: %w", key, ErrUnknown)
	}
	value = strings.TrimSpace(value)
	if value != "" {
		if err := f.set(&Values{}, value); err != nil {
			return Setting{}, err
		}
	}
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		if value == "" {
			_, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
		return err
	})
	if err != nil {
		return Setting{}, err
	}
	s.mu.Lock()
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

// OnChange calls fn with the new settings after each change.
func (s *Store) OnChange(fn func(Values)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = append(s.onChange, fn)
}
