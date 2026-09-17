package config

import (
	"strings"
	"testing"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestIncludeBrowser(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
		err   bool
	}{
		{"", false, false},
		{"false", false, false},
		{"true", true, false},
		{"  true ", true, false},
		{"yes", false, true},
		{"TRUE", false, true},
		{"1", false, true},
	} {
		c, err := FromEnv(env(map[string]string{"AOS_MODE": "ui", "INCLUDE_BROWSER": tc.value}))
		if tc.err {
			if err == nil || !strings.Contains(err.Error(), "INCLUDE_BROWSER") {
				t.Errorf("INCLUDE_BROWSER=%q: err = %v, want an INCLUDE_BROWSER error", tc.value, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("INCLUDE_BROWSER=%q: %v", tc.value, err)
			continue
		}
		if c.IncludeBrowser != tc.want {
			t.Errorf("INCLUDE_BROWSER=%q: IncludeBrowser = %v, want %v", tc.value, c.IncludeBrowser, tc.want)
		}
	}
}
