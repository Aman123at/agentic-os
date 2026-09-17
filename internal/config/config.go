// Package config reads aosd's environment (PLAN.md §6.4).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/amantiwari/agentic-os/internal/policy"
)

// Config is aosd's configuration.
type Config struct {
	Mode, ImageMode        string
	Model, ReasoningEffort string
	BaseURL                string
	Bind                   string
	HostPort               string
	AccessToken            string
	Autonomy               policy.Autonomy
	MaxTasks               int
	MaxRetries             int
	RequireLandlock        bool
	UID, GID               int
	TrashRetention         time.Duration
	TrashMaxBytes          int64
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
		ImageMode:       getenv("AOS_IMAGE_MODE"),
		Model:           str("OPENAI_MODEL", ""),
		ReasoningEffort: str("OPENAI_REASONING_EFFORT", ""),
		BaseURL:         str("OPENAI_BASE_URL", ""),
		Bind:            str("AOS_BIND", "127.0.0.1"),
		HostPort:        str("AOS_PORT", "7700"),
		AccessToken:     str("AOS_ACCESS_TOKEN", ""),
		MaxTasks:        num("AOS_MAX_TASKS", 3),
		MaxRetries:      num("AOS_MAX_RETRIES", 3),
		UID:             num("AOS_UID", 1000),
		GID:             num("AOS_GID", 1000),
		TrashRetention:  time.Duration(num("AOS_TRASH_RETENTION_DAYS", 30)) * 24 * time.Hour,
		TrashMaxBytes:   int64(num("AOS_TRASH_MAX_GB", 5)) << 30,
		FakeModel:       str("AOS_FAKE_MODEL", ""),
		Set:             set,
	}
	c.Mode = str("AOS_MODE", c.ImageMode)
	if c.Mode != "cli" && c.Mode != "ui" {
		errs = append(errs, fmt.Sprintf("AOS_MODE=%q must be cli or ui", c.Mode))
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

// Getenv is os.Getenv, for FromEnv.
var Getenv = os.Getenv
