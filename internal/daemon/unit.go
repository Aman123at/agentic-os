// The systemd unit for aosd on a native Linux install (ADR-0009, M6.2). This
// file has no build constraint on purpose: `tools/ci lint` (which runs on the
// macOS Host) compares Unit() against the committed deploy/systemd/aos.service
// golden, so a drift between the two fails the build. install.sh (M6.17) installs
// the golden file from the release tarball (built by the release stage, M6.18).
package daemon

// unitBinary is where the release tarball puts aosd (M6.18).
const unitBinary = "/usr/local/bin/aosd"

// Unit is the contents of /etc/systemd/system/aos.service.
//
// systemd owns the Daemon, not the Services an Agent creates (ADR-0005/0009):
// Type=notify so a caller — install.sh above all — cannot race the listener,
// Restart=always with a start-limit so a bad config fails the unit loudly
// instead of looping, KillMode=mixed and TimeoutStopSec=60s so aosd gets its
// full drain window before any straggler is killed.
func Unit() string {
	return `# Agentic OS daemon (aosd), run by systemd on a native Linux install (ADR-0009).
#
# There are deliberately NO systemd hardening directives here — no
# ProtectSystem, NoNewPrivileges, PrivateTmp, ProtectHome, capability bounding
# or SystemCallFilter. systemd would confine aosd and every Agent it launches
# indiscriminately, but Agents are confined precisely, per process, by Landlock
# plus uid separation (ADR-0004), and aosd itself must keep the privilege to
# create the aos user's Sessions, build their rulesets and supervise Services.
# Hardening here would duplicate and coarsen that model and break each Agent's
# own boundary. Do not "harden" this unit; the confinement lives in aosd.

[Unit]
Description=Agentic OS daemon (aosd)
Documentation=https://github.com/Aman123at/agentic-os
After=network-online.target
Wants=network-online.target
# A bad config exits non-zero on every start, so cap restarts: after 5 failures
# in 60s the unit fails and stays failed instead of looping forever.
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=notify
ExecStart=` + unitBinary + `
# Mode is a config.yml key (M6.10): install.sh writes mode: ui, and the built-in
# default is ui too, so the native install runs the Desktop with no env here.
Restart=always
RestartSec=2
# SIGTERM to aosd, which drains Tasks and then closes the control socket;
# SIGKILL to any straggler left in the cgroup once the timeout passes.
KillMode=mixed
TimeoutStopSec=60s

[Install]
WantedBy=multi-user.target
`
}
