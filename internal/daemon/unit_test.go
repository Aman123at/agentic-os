package daemon

import (
	"strings"
	"testing"
)

// The unit is Type=notify with a start-limit and a drain window, and carries
// none of systemd's hardening directives — Agents are confined by Landlock and
// uid separation instead (ADR-0004/0009, M6.2).
func TestUnitHasTheRightDirectivesAndNoHardening(t *testing.T) {
	u := Unit()
	for _, want := range []string{"Type=notify", "Restart=always", "StartLimitBurst=5",
		"KillMode=mixed", "TimeoutStopSec=60s", "ExecStart=" + unitBinary} {
		if !strings.Contains(u, want) {
			t.Errorf("the unit is missing %q", want)
		}
	}
	// systemd hardening would confine aosd and every Agent indiscriminately.
	for _, forbidden := range []string{"ProtectSystem", "NoNewPrivileges=", "PrivateTmp",
		"ProtectHome", "CapabilityBoundingSet", "SystemCallFilter"} {
		for _, line := range strings.Split(u, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue // the comment argues against these by name
			}
			if strings.Contains(trimmed, forbidden) {
				t.Errorf("the unit carries a hardening directive %q: %s", forbidden, trimmed)
			}
		}
	}
}
