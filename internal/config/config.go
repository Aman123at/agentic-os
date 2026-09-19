// Package config reads aosd's environment (PLAN.md §6.4).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Aman123at/agentic-os/internal/policy"
)

// Config is aosd's configuration.
type Config struct {
	// Mode is ui (the Desktop, behind a password) or cli (the control socket
	// only). One image carries both (M6.10); AOS_MODE seeds the generated
	// config.yml, then the file's mode: key is the source of truth (ADR-0010).
	Mode                   string
	Model, ReasoningEffort string
	BaseURL                string
	// Filesystem is where Agents may write (ADR-0004, M6.8): "home" confines them
	// to /home/aos (the Compose default), "host" widens the Writable set to all of
	// `/` minus the Protected list on a native install.
	Filesystem string
	// RootMode is set when the Machine boots into the Root Realm (Root Mode, M7):
	// Agents, the Terminal and Finder act as root and Protected Paths are not
	// enforced. Startup-only, from `root_mode:` in config.yml; the Daemon resolves
	// it once into an internal/realm.Realm and no other package reads it directly.
	RootMode bool
	// ConfigPath is /etc/aos/config.yml, the single source of truth for every
	// non-secret setting (ADR-0010). AOS_CONFIG overrides it (tests, dev).
	ConfigPath      string
	Bind            string
	HostPort        string
	Autonomy        policy.Autonomy
	MaxTasks        int
	MaxRetries      int
	RequireLandlock bool
	UID, GID        int
	TrashRetention  time.Duration
	TrashMaxBytes   int64
	// Cost Limits in USD; 0 means none (PLAN.md §8.4).
	TaskCostLimit, DailyCostLimit float64
	// IncludeBrowser asks for the Browser app (ui Mode only, PLAN.md M5.2). The
	// image must also have been built with INCLUDE_BROWSER=true.
	IncludeBrowser bool
	// FakeModel is a folder of cassettes that replaces OpenAI (tests only).
	FakeModel string
	// Set names the variables the environment set, so a setting can say whether
	// its value came from there or is the built-in default.
	Set map[string]bool
}

// FromEnv reads the configuration; getenv is os.Getenv in production.
func FromEnv(getenv func(string) string) (Config, error) {
	var errs []string
	set := map[string]bool{}
	str := func(name, def string) string {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			set[name] = true
			return v
		}
		return def
	}
	num := func(name string, def int) int {
		v := str(name, "")
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			errs = append(errs, fmt.Sprintf("%s=%q is not a whole number", name, v))
			return def
		}
		return n
	}
	money := func(name string) float64 {
		v := str(name, "")
		if v == "" {
			return 0
		}
		n, err := strconv.ParseFloat(strings.TrimPrefix(v, "$"), 64)
		if err != nil || n < 0 {
			errs = append(errs, fmt.Sprintf("%s=%q is not an amount in USD", name, v))
			return 0
		}
		return n
	}
	c := Config{
		TaskCostLimit:   money("AOS_TASK_COST_LIMIT_USD"),
		DailyCostLimit:  money("AOS_DAILY_COST_LIMIT_USD"),
		Model:           str("OPENAI_MODEL", ""),
		ReasoningEffort: str("OPENAI_REASONING_EFFORT", ""),
		BaseURL:         str("OPENAI_BASE_URL", ""),
		Bind:            str("AOS_BIND", "127.0.0.1"),
		HostPort:        str("AOS_PORT", "7700"),
		MaxTasks:        num("AOS_MAX_TASKS", 3),
		MaxRetries:      num("AOS_MAX_RETRIES", 3),
		UID:             num("AOS_UID", 1000),
		GID:             num("AOS_GID", 1000),
		TrashRetention:  time.Duration(num("AOS_TRASH_RETENTION_DAYS", 30)) * 24 * time.Hour,
		TrashMaxBytes:   int64(num("AOS_TRASH_MAX_GB", 5)) << 30,
		FakeModel:       str("AOS_FAKE_MODEL", ""),
		Set:             set,
	}
	c.ConfigPath = strings.TrimSpace(getenv("AOS_CONFIG"))
	if c.ConfigPath == "" {
		c.ConfigPath = "/etc/aos/config.yml"
	}
	c.Mode = str("AOS_MODE", "ui")
	if c.Mode != "cli" && c.Mode != "ui" {
		errs = append(errs, fmt.Sprintf("AOS_MODE=%q must be cli or ui", c.Mode))
	}
	c.Filesystem = str("AOS_FILESYSTEM", "home")
	if c.Filesystem != "home" && c.Filesystem != "host" {
		errs = append(errs, fmt.Sprintf("AOS_FILESYSTEM=%q must be home or host", c.Filesystem))
	}
	if c.MaxTasks == 0 {
		c.MaxTasks = 1
	}
	switch a := str("AOS_AUTONOMY", "confirm-risky"); a {
	case "auto":
		c.Autonomy = policy.Auto
	case "confirm-risky":
		c.Autonomy = policy.ConfirmRisky
	case "confirm-all":
		c.Autonomy = policy.ConfirmAll
	default:
		errs = append(errs, fmt.Sprintf("AOS_AUTONOMY=%q must be auto, confirm-risky or confirm-all", a))
	}
	switch v := strings.ToLower(str("AOS_REQUIRE_LANDLOCK", "false")); v {
	case "true", "1", "yes":
		c.RequireLandlock = true
	case "false", "0", "no":
	default:
		errs = append(errs, fmt.Sprintf("AOS_REQUIRE_LANDLOCK=%q must be true or false", v))
	}
	switch v := strings.ToLower(str("AOS_ROOT_MODE", "false")); v {
	case "true", "1", "yes":
		c.RootMode = true
	case "false", "0", "no":
	default:
		errs = append(errs, fmt.Sprintf("AOS_ROOT_MODE=%q must be true or false", v))
	}
	// INCLUDE_BROWSER is a build argument too, where only true and false work, so
	// it takes exactly those words.
	switch v := str("INCLUDE_BROWSER", "false"); v {
	case "true":
		c.IncludeBrowser = true
	case "false":
	default:
		errs = append(errs, fmt.Sprintf("INCLUDE_BROWSER=%q must be true or false", v))
	}
	if len(errs) > 0 {
		return c, fmt.Errorf("invalid configuration: %s", strings.Join(errs, "; "))
	}
	return c, nil
}

// Native reports whether this is a native install (ADR-0009). There is no other
// runtime signal for it: install.sh marks a native box by writing filesystem:
// host (M6.8), so that key doubles as the native marker. It gates the whole-`/`
// widening (ADR-0004) and turning Replay off at boot (ADR-0003, M6.9).
func (c Config) Native() bool { return c.Filesystem == "host" }

// Getenv is os.Getenv, for FromEnv.
var Getenv = os.Getenv
