package settings

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Aman123at/agentic-os/internal/policy"
	"github.com/Aman123at/agentic-os/internal/store"
)

// base is what aosd reads from the environment: here AOS_MAX_TASKS was set and
// the rest are built-in defaults.
var base = Values{Model: "gpt-5.6-terra", Autonomy: policy.ConfirmRisky, MaxTasks: 5, MaxRetries: 3,
	TrashRetentionDays: 30, TrashMaxGB: 5}

func open(t *testing.T, path string) *Store {
	t.Helper()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := Open(context.Background(), db, base, map[string]bool{"AOS_MAX_TASKS": true})
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
	s := open(t, filepath.Join(t.TempDir(), "aos.db"))
	ctx := context.Background()

	if st := find(t, s, "max_tasks"); st.Value != "5" || st.Source != FromEnv || st.Env != "AOS_MAX_TASKS" {
		t.Errorf("max_tasks before any change: %+v", st)
	}
	if st := find(t, s, "autonomy"); st.Value != "confirm-risky" || st.Source != FromDefault {
		t.Errorf("autonomy before any change: %+v", st)
	}

	if _, err := s.Set(ctx, "autonomy", "confirm-all"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(ctx, "max_tasks", "2"); err != nil {
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
	if st, err := s.Set(ctx, "max_tasks", ""); err != nil || st.Value != "5" || st.Source != FromEnv {
		t.Errorf("reset max_tasks: %+v, %v", st, err)
	}
}

func TestSavedSettingsSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aos.db")
	s := open(t, path)
	if _, err := s.Set(context.Background(), "daily_cost_limit_usd", "$2.50"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(context.Background(), "model", "gpt-5.6-mini"); err != nil {
		t.Fatal(err)
	}

	again := open(t, path)
	if v := again.Values(); v.DailyCostLimit != 2.5 || v.Model != "gpt-5.6-mini" {
		t.Errorf("after reopening: %+v", v)
	}
}

func TestInvalidValuesAreRefusedAndChangeNothing(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "aos.db"))
	ctx := context.Background()
	for key, value := range map[string]string{
		"autonomy":             "yolo",
		"max_tasks":            "0",
		"max_retries":          "-1",
		"task_cost_limit_usd":  "a lot",
		"trash_retention_days": "1.5",
		"model":                "gpt 5",
		"reasoning_effort":     "maximum",
	} {
		if _, err := s.Set(ctx, key, value); err == nil {
			t.Errorf("%s=%q was accepted", key, value)
		}
	}
	if v := s.Values(); v != base {
		t.Errorf("refused changes still changed the settings: %+v", v)
	}
	if _, err := s.Set(ctx, "base_url", "https://example.com"); !errors.Is(err, ErrUnknown) {
		t.Errorf("an unknown or environment-only setting: %v, want ErrUnknown", err)
	}
}

func TestListenersHearEachChange(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "aos.db"))
	var got []int
	s.OnChange(func(v Values) { got = append(got, v.MaxTasks) })
	for _, n := range []string{"4", "7"} {
		if _, err := s.Set(context.Background(), "max_tasks", n); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 2 || got[0] != 4 || got[1] != 7 {
		t.Errorf("listeners heard %v, want [4 7]", got)
	}
}
