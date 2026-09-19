package settings

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aman123at/agentic-os/internal/config"
	"github.com/Aman123at/agentic-os/internal/policy"
)

// base is what aosd reads from the environment: here AOS_MAX_TASKS was set and
// the rest are built-in defaults.
var base = Values{Model: "gpt-5.6-terra", ReasoningEffort: "", Autonomy: policy.ConfirmRisky,
	MaxTasks: 5, MaxRetries: 3, TrashRetentionDays: 30, TrashMaxGB: 5}

// newCfg is the config the environment produced: AOS_MAX_TASKS set, the
// startup-only keys at their defaults.
func newCfg() *config.Config {
	return &config.Config{Set: map[string]bool{"AOS_MAX_TASKS": true}}
}

func open(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path, base, newCfg())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func find(t *testing.T, s *Store, key string) Setting {
	t.Helper()
	for _, st := range s.List() {
		if st.Key == key {
			return st
		}
	}
	t.Fatalf("no setting %q", key)
	return Setting{}
}

func TestSystemSettingsWinOverTheEnvironmentOverTheDefault(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "config.yml"))

	if st := find(t, s, "max_tasks"); st.Value != "5" || st.Source != FromEnv || st.Env != "AOS_MAX_TASKS" {
		t.Errorf("max_tasks before any change: %+v", st)
	}
	if st := find(t, s, "autonomy"); st.Value != "confirm-risky" || st.Source != FromDefault {
		t.Errorf("autonomy before any change: %+v", st)
	}

	if _, err := s.Set("autonomy", "confirm-all"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set("max_tasks", "2"); err != nil {
		t.Fatal(err)
	}
	v := s.Values()
	if v.Autonomy != policy.ConfirmAll || v.MaxTasks != 2 {
		t.Errorf("after the changes: %+v", v)
	}
	if st := find(t, s, "max_tasks"); st.Source != FromSettings || st.Fallback != "5" {
		t.Errorf("a saved value should show where the environment's value went: %+v", st)
	}

	// An empty value removes the saved one: the environment applies again.
	if st, err := s.Set("max_tasks", ""); err != nil || st.Value != "5" || st.Source != FromEnv {
		t.Errorf("reset max_tasks: %+v, %v", st, err)
	}
}

func TestSavedSettingsSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	s := open(t, path)
	if _, err := s.Set("daily_cost_limit_usd", "$2.50"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set("model", "gpt-5.6-mini"); err != nil {
		t.Fatal(err)
	}

	again := open(t, path)
	if v := again.Values(); v.DailyCostLimit != 2.5 || v.Model != "gpt-5.6-mini" {
		t.Errorf("after reopening: %+v", v)
	}
	if st := find(t, again, "model"); st.Source != FromSettings {
		t.Errorf("a value changed at runtime should read back as saved: %+v", st)
	}
}

// TestPerModelEffortValidation covers the M6.16 fix: max is a wire value, an
// effort the chosen model does not accept is refused (it would be an HTTP 400),
// and changing the model substitutes a saved effort the new model rejects.
func TestPerModelEffortValidation(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "config.yml"))
	// A stand-in catalogue: terra takes none..max, astra rejects none.
	efforts := map[string][]string{
		"gpt-5.6-terra": {"none", "low", "medium", "high", "xhigh", "max"},
		"gpt-6-astra":   {"low", "medium", "high", "xhigh", "max"},
	}
	s.Efforts = func(model string) []string { return efforts[model] }

	if _, err := s.Set("reasoning_effort", "max"); err != nil {
		t.Fatalf("terra should accept max: %v", err)
	}
	if _, err := s.Set("reasoning_effort", "none"); err != nil {
		t.Fatalf("terra should accept none: %v", err)
	}
	// Switch to astra: the saved effort none is a 400 there, so it is substituted.
	if _, err := s.Set("model", "gpt-6-astra"); err != nil {
		t.Fatalf("set model astra: %v", err)
	}
	if v := s.Values(); v.ReasoningEffort != SubstituteEffort {
		t.Errorf("changing to astra should substitute the invalid effort with %q, got %q", SubstituteEffort, v.ReasoningEffort)
	}
	// none is now refused outright on astra, naming what it accepts.
	if _, err := s.Set("reasoning_effort", "none"); err == nil || !strings.Contains(err.Error(), "accepts") {
		t.Errorf("astra should refuse none: %v", err)
	}
	if _, err := s.Set("reasoning_effort", "max"); err != nil {
		t.Errorf("astra should accept max: %v", err)
	}
}

func TestInvalidValuesAreRefusedAndChangeNothing(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "config.yml"))
	for key, value := range map[string]string{
		"autonomy":             "yolo",
		"max_tasks":            "0",
		"max_retries":          "-1",
		"task_cost_limit_usd":  "a lot",
		"trash_retention_days": "1.5",
		"model":                "gpt 5",
		"reasoning_effort":     "maximum",
		"base_url":             "not-a-url",
		"require_landlock":     "maybe",
		"filesystem":           "everywhere",
		"mode":                 "gui",
	} {
		if _, err := s.Set(key, value); err == nil {
			t.Errorf("%s=%q was accepted", key, value)
		}
	}
	if v := s.Values(); v != base {
		t.Errorf("refused changes still changed the settings: %+v", v)
	}
	if _, err := s.Set("nonsense_key", "x"); !errors.Is(err, ErrUnknown) {
		t.Errorf("an unknown setting: %v, want ErrUnknown", err)
	}
}

func TestListenersHearEachRuntimeChange(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "config.yml"))
	var got []int
	s.OnChange(func(v Values) { got = append(got, v.MaxTasks) })
	for _, n := range []string{"4", "7"} {
		if _, err := s.Set("max_tasks", n); err != nil {
			t.Fatal(err)
		}
	}
	// A startup-only change does not fire the runtime listeners.
	if _, err := s.Set("base_url", "https://example.com/v1"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 4 || got[1] != 7 {
		t.Errorf("listeners heard %v, want [4 7]", got)
	}
}

// A hand-added comment must survive a write-back (ADR-0010).
func TestHandEditsSurviveAWriteBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	seed := "# my own note\nmodel: gpt-5.6-terra\nmax_tasks: 4  # kept low on purpose\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	s := open(t, path)
	if _, err := s.Set("autonomy", "auto"); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{"# my own note", "kept low on purpose", "autonomy: auto"} {
		if !strings.Contains(got, want) {
			t.Errorf("after a write-back the file lost %q:\n%s", want, got)
		}
	}
	// The value the user hand-set is still honoured.
	if again := open(t, path); again.Values().MaxTasks != 4 {
		t.Errorf("hand-set max_tasks not honoured: %d", again.Values().MaxTasks)
	}
}

func TestAMalformedFileOrUnknownKeyRefusesTheStart(t *testing.T) {
	cases := map[string]string{
		"not yaml":      "model: [unterminated\n",
		"not a mapping": "- just\n- a\n- list\n",
		"unknown key":   "model: gpt-5.6-terra\nwibble: 3\n",
		"bad value":     "max_tasks: 99\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(path, base, newCfg()); err == nil {
				t.Fatalf("%s was accepted, want a refusal", name)
			}
		})
	}
	// The unknown-key refusal is ErrUnknown, so a caller can name it.
	path := filepath.Join(t.TempDir(), "config.yml")
	_ = os.WriteFile(path, []byte("wibble: 3\n"), 0o600)
	if _, err := Open(path, base, newCfg()); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown key at start: %v, want ErrUnknown", err)
	}
}

func TestStartupKeyIsPendingUntilRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	cfg := newCfg()
	s, err := Open(path, base, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if st := find(t, s, "base_url"); st.Value != "" || st.Source != FromDefault || st.PendingRestart {
		t.Errorf("base_url before any change: %+v", st)
	}
	st, err := s.Set("base_url", "https://proxy.example/v1")
	if err != nil {
		t.Fatal(err)
	}
	if st.Value != "https://proxy.example/v1" || !st.PendingRestart || st.Source != FromSettings {
		t.Errorf("a startup-only change should be saved but pending: %+v", st)
	}
	// It is written to the file but NOT applied live: cfg still holds the old value.
	if cfg.BaseURL != "" {
		t.Errorf("startup key was applied live: BaseURL = %q", cfg.BaseURL)
	}

	// A restart re-reads it: now it is in force, no longer pending, and applied to cfg.
	cfg2 := newCfg()
	again, err := Open(path, base, cfg2)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.BaseURL != "https://proxy.example/v1" {
		t.Errorf("after a restart the startup key was not applied: BaseURL = %q", cfg2.BaseURL)
	}
	if st := find(t, again, "base_url"); st.PendingRestart {
		t.Errorf("after a restart the startup key should no longer be pending: %+v", st)
	}
}

// root_mode is a startup-only key, but the generic Set refuses it: switching
// Realms restarts AOS behind a warning and a password (M7.7, M7.11), so
// `aos config set root_mode true` must point the user at `aos root on` rather
// than flip the Realm as an ordinary setting.
func TestRootModeCannotBeSetThroughConfig(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "config.yml"))
	if _, err := s.Set("root_mode", "true"); !errors.Is(err, ErrRootModeNotHere) {
		t.Fatalf("Set(root_mode) error = %v, want ErrRootModeNotHere", err)
	}
	if !strings.Contains(ErrRootModeNotHere.Error(), "aos root on") {
		t.Errorf("the refusal should point at `aos root on`: %q", ErrRootModeNotHere)
	}
	// It is startup-only and reads from config.yml like the others: a file that
	// carries root_mode: true boots into Root and reports it no longer pending.
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("root_mode: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := newCfg()
	again, err := Open(path, base, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.RootMode {
		t.Error("root_mode: true in the file did not apply to cfg.RootMode")
	}
	if st := find(t, again, "root_mode"); st.Value != "true" || st.PendingRestart {
		t.Errorf("root_mode after a restart into Root: %+v, want value true and not pending", st)
	}
}

// The generated file is self-documenting and stable; testdata/config.golden.yml
// is what a fresh start writes from the built-in defaults.
func TestGeneratedFileMatchesTheGolden(t *testing.T) {
	defaults := Values{Model: "gpt-5.6-terra", Autonomy: policy.ConfirmRisky, MaxTasks: 3, MaxRetries: 3,
		TrashRetentionDays: 30, TrashMaxGB: 5}
	path := filepath.Join(t.TempDir(), "config.yml")
	if _, err := Open(path, defaults, &config.Config{Set: map[string]bool{}}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "config.golden.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("generated config.yml differs from the golden.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestFreshStartGeneratesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if _, err := Open(path, base, newCfg()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the first start should generate %s: %v", path, err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config.yml mode is %v, want 0600", info.Mode().Perm())
	}
}
