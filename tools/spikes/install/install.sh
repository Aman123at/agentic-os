#!/bin/sh
# PROTOTYPE — throwaway, written for .scratch/native-linux-release/issues/11-install-script.md
# so the shape of the real installer can be argued over concretely. It is NOT
# the shipped script: the real one is written during M6 implementation.
#
# Read it:     less tools/spikes/install/install.sh
# Rehearse it: DRY_RUN=1 sh tools/spikes/install/install.sh      (safe anywhere, incl. macOS)
#
# Every mutating step goes through run(), so DRY_RUN=1 prints the complete
# sequence of changes without touching the machine. That is the whole point of
# the prototype: the plan is reviewable before anyone runs it on a VPS.

set -eu

REPO="Aman123at/agentic-os"
BASE="https://github.com/${REPO}/releases/latest/download"
BIN_DIR="/usr/local/bin"
LIB_DIR="/usr/local/lib/aos"
STATE_DIR="/var/lib/aos"
CONF_DIR="/etc/aos"
CONF="${CONF_DIR}/config.yml"
UNIT="/etc/systemd/system/aos.service"
AOS_USER="aos"
AOS_HOME="/home/aos"
MIN_KERNEL_MAJOR=5
MIN_KERNEL_MINOR=13   # Landlock ABI 1 (ADR-0004)
NEED_MB=250

DRY_RUN="${DRY_RUN:-0}"

say()  { printf '%s\n' "$*"; }
step() { printf '\n==> %s\n' "$*"; }
die()  { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

# fail stops a real run, but under DRY_RUN reports and carries on, so the whole
# plan can be rehearsed from a machine that could never pass preflight.
fail() {
	if [ "$DRY_RUN" = 1 ]; then
		printf '   WOULD REFUSE: %s\n' "$*"
		return 0
	fi
	die "$*"
}

# run executes a mutating command, or prints it under DRY_RUN.
run() {
	if [ "$DRY_RUN" = 1 ]; then
		printf '   [dry-run] %s\n' "$*"
		return 0
	fi
	"$@"
}

# ---------------------------------------------------------------- preflight

preflight() {
	step "Checking this machine"

	[ "$(uname -s)" = Linux ] || fail "Agentic OS runs on Linux only; this is $(uname -s)."

	case "$(uname -m)" in
		x86_64|amd64) ARCH=amd64 ;;
		aarch64|arm64) ARCH=arm64 ;;
		*) ARCH=amd64; fail "unsupported architecture $(uname -m); only amd64 and arm64 are released." ;;
	esac
	say "   architecture: ${ARCH}"

	[ "$(id -u)" = 0 ] || fail "run as root: curl -fsSL <url> | sudo sh"

	[ -d /run/systemd/system ] || fail "this machine does not run systemd.
Agentic OS installs as a systemd service. On a machine without systemd, run it
with Docker Compose instead: https://github.com/${REPO}#docker-compose"

	# Landlock is the sandbox (ADR-0004). Warn rather than refuse: aosd starts
	# without it unless require_landlock is set, and says so loudly.
	kver=$(uname -r)
	kmaj=${kver%%.*}; krest=${kver#*.}; kmin=${krest%%.*}
	case "$kmaj$kmin" in *[!0-9]*) kmaj=0; kmin=0 ;; esac
	if [ "$kmaj" -lt "$MIN_KERNEL_MAJOR" ] ||
	   { [ "$kmaj" -eq "$MIN_KERNEL_MAJOR" ] && [ "$kmin" -lt "$MIN_KERNEL_MINOR" ]; }; then
		say "   WARNING: kernel ${kver} has no Landlock (needs ${MIN_KERNEL_MAJOR}.${MIN_KERNEL_MINOR}+)."
		say "            Agents will run without filesystem confinement."
	elif [ -r /sys/kernel/security/lsm ] && ! grep -q landlock /sys/kernel/security/lsm; then
		say "   WARNING: kernel ${kver} supports Landlock but it is not enabled"
		say "            (add lsm=...,landlock to the kernel command line)."
	else
		say "   kernel ${kver}: Landlock available"
	fi

	for t in curl tar sha256sum install useradd; do
		command -v "$t" >/dev/null 2>&1 || fail "$t is required but not installed."
	done

	free_mb=$(df -Pm /usr/local 2>/dev/null | awk 'NR==2 {print $4}' || echo 0)
	[ "${free_mb:-0}" -ge "$NEED_MB" ] || fail "needs ${NEED_MB} MB free on /usr/local; ${free_mb:-0} MB available."
	say "   disk: ${free_mb} MB free on /usr/local"

	if [ -x "${BIN_DIR}/aosd" ]; then
		UPGRADE=1
		say "   found an existing install — this run upgrades it in place"
	else
		UPGRADE=0
	fi
}

# ---------------------------------------------------------------- download

fetch() {
	step "Downloading the latest release"
	TMP=$(mktemp -d)
	# The temp dir is removed on every exit path, so a failed install leaves
	# nothing behind before the first mutating step.
	trap 'rm -rf "$TMP"' EXIT INT TERM

	tarball="agentic-os-linux-${ARCH}.tar.gz"
	# Version-less asset names: /releases/latest/download/<asset> resolves
	# without touching the GitHub API, whose 60/hour budget is per IP.
	say "   ${BASE}/${tarball}"
	run curl -fsSL --proto '=https' --tlsv1.2 -o "${TMP}/${tarball}" "${BASE}/${tarball}"
	run curl -fsSL --proto '=https' --tlsv1.2 -o "${TMP}/SHA256SUMS" "${BASE}/SHA256SUMS"

	step "Verifying the checksum"
	# grep-then-verify, never `sha256sum --ignore-missing -c`, which succeeds
	# vacuously when the file it should check is absent from the list.
	if [ "$DRY_RUN" != 1 ]; then
		line=$(grep " \*\{0,1\}${tarball}$" "${TMP}/SHA256SUMS") ||
			die "${tarball} is not listed in SHA256SUMS; refusing to install."
		printf '%s\n' "$line" > "${TMP}/want"
		( cd "$TMP" && sha256sum -c want >/dev/null ) ||
			die "checksum mismatch for ${tarball}; refusing to install."
	fi
	say "   checksum OK"

	run tar -xzf "${TMP}/${tarball}" -C "$TMP"
}

# ---------------------------------------------------------------- install

# refuse_if_busy keeps an upgrade from cancelling work. aosd interrupts every
# running Task when it stops, denies pending Approvals, and leaves them for
# `aos resume` — recoverable, but not something to do to someone silently.
refuse_if_busy() {
	[ "$UPGRADE" = 1 ] || return 0
	[ "${FORCE:-0}" = 1 ] && { say "   FORCE=1 — upgrading with work in flight"; return 0; }
	busy=$(aos status --json 2>/dev/null | awk -F'[:,]' '/"tasks_active"/ {print $2+0}') || busy=0
	[ "${busy:-0}" -eq 0 ] 2>/dev/null || die "${busy} Task(s) are still running.
Wait for them, or re-run with FORCE=1 to upgrade anyway — they become Interrupted
and continue with \`aos resume <id>\`."
}

install_binary() {
	step "Installing aosd"
	# install(1) unlinks first, so replacing a *running* binary is safe;
	# writing over it in place would fail with ETXTBSY.
	run install -m 0755 -o root -g root "${TMP}/aosd" "${BIN_DIR}/aosd.new"
	run mv -f "${BIN_DIR}/aosd.new" "${BIN_DIR}/aosd"
	run ln -sf aosd "${BIN_DIR}/aos"
	run mkdir -p "${LIB_DIR}/agent-bin"
	run ln -sf "${BIN_DIR}/aos" "${LIB_DIR}/agent-bin/rm"
	say "   ${BIN_DIR}/aosd, ${BIN_DIR}/aos, ${LIB_DIR}/agent-bin/rm"
}

create_user() {
	step "Creating the ${AOS_USER} user"
	if id "$AOS_USER" >/dev/null 2>&1; then
		say "   ${AOS_USER} already exists — left alone"
	else
		# No --uid: uid 1000 is taken by the admin's own account on every
		# stock Ubuntu image. aosd looks the user up by name.
		run useradd --create-home --home-dir "$AOS_HOME" --shell /bin/bash "$AOS_USER"
		run install -d -o "$AOS_USER" -g "$AOS_USER" "${AOS_HOME}/Downloads"
		say "   ${AOS_USER}, home ${AOS_HOME}"
	fi

	# PrepareHome relocates dotfiles and must never run against a home AOS did
	# not create. The marker is what tells aosd this home is its own.
	run touch "${AOS_HOME}/.aos-home"
	run chown "${AOS_USER}:${AOS_USER}" "${AOS_HOME}/.aos-home"

	# Desktop Terminal sessions run as aos and may sudo. Agent sessions cannot:
	# no_new_privs disables setuid (ADR-0004).
	step "Granting ${AOS_USER} sudo"
	say "   NOTE: this is passwordless root for anyone who reaches the Desktop."
	if [ "$DRY_RUN" = 1 ]; then
		printf '   [dry-run] write /etc/sudoers.d/aos: %s ALL=(ALL) NOPASSWD:ALL\n' "$AOS_USER"
	else
		umask 077
		printf '# User Sessions may use sudo. Agent Sessions cannot: no_new_privs disables setuid (ADR-0004).\n%s ALL=(ALL) NOPASSWD:ALL\n' "$AOS_USER" > /etc/sudoers.d/aos
		chmod 0440 /etc/sudoers.d/aos
		visudo -cf /etc/sudoers.d/aos >/dev/null || { rm -f /etc/sudoers.d/aos; die "the sudoers entry did not validate; removed it."; }
	fi
}

create_dirs() {
	step "Creating the state and configuration folders"
	run install -d -m 0700 -o root -g root "$STATE_DIR" "${STATE_DIR}/keys"
	run install -d -m 0700 -o root -g root "$CONF_DIR"
	# /run/aos is created by aosd at every start; systemd does not manage it.
	say "   ${STATE_DIR} (0700), ${CONF_DIR} (0700)"
}

write_config() {
	step "Writing ${CONF}"
	if [ -f "$CONF" ]; then
		say "   already exists — left untouched (an upgrade never rewrites your config)"
		return 0
	fi
	if [ "$DRY_RUN" = 1 ]; then
		printf '   [dry-run] write %s (0600, root) with the commented default template\n' "$CONF"
		return 0
	fi
	umask 077
	cat > "$CONF" <<-'YAML'
	# Agentic OS configuration. Root-owned, mode 0600.
	#
	# This file is the single source of truth. What you change in the Desktop or
	# with `aos config set` is written back here, so a restart never reverts a
	# setting. Comments and key order survive that write-back.
	#
	#   aos config get            show every setting and where it came from
	#   aos config set model=...  change one, from the shell
	#   aos config edit           open this file, validated before it saves
	#
	# An unknown key or a malformed value stops the service at start, naming the
	# line. It never falls back to a default.

	# --- Sign-in -------------------------------------------------------------
	# The account for the Desktop at http://<this-server>:7700.
	# Leave `password` empty and one is generated on first start; `aos status`
	# prints it. Either way the first sign-in forces a password change, after
	# which this key is blanked automatically.
	username: admin
	password: ""

	# --- Reach (restart to apply) --------------------------------------------
	mode: ui           # ui = Desktop + CLI, cli = CLI only, nothing listening
	bind: 0.0.0.0      # an IP literal; 127.0.0.1 to reach it only through a proxy
	port: 7700         # taken? the next free port is chosen and written back here

	# --- The Agent -----------------------------------------------------------
	model: gpt-5.6-terra
	reasoning_effort: medium
	autonomy: confirm-risky   # auto | confirm-risky | confirm-all
	max_tasks: 4
	max_retries: 3

	# --- Cost limits in USD; 0 means none ------------------------------------
	task_cost_limit_usd: 0
	daily_cost_limit_usd: 0

	# --- Trash ---------------------------------------------------------------
	trash_retention_days: 30
	trash_max_gb: 5

	# --- Optional (restart to apply) -----------------------------------------
	include_browser: false   # true fetches Chromium's headless shell at next start
	require_landlock: false  # true refuses to start without the sandbox
	YAML
	chmod 0600 "$CONF"
	say "   written (0600, root) — the API key goes in separately, see below"
}

install_unit() {
	step "Installing ${UNIT}"
	if [ "$DRY_RUN" = 1 ]; then
		printf '   [dry-run] write %s\n' "$UNIT"
	else
		cat > "$UNIT" <<-'INI'
		[Unit]
		Description=Agentic OS
		Documentation=https://amantiwari.co.in/agentic-os/
		Wants=network-online.target
		After=network-online.target

		[Service]
		Type=notify
		ExecStart=/usr/local/bin/aosd
		Restart=always
		RestartSec=2s
		StartLimitIntervalSec=60s
		StartLimitBurst=5
		# Stopping drains in-flight Agent work: the HTTP shutdown alone is 20s,
		# then Tasks are cancelled, then Services are stopped.
		TimeoutStopSec=60s
		# SIGTERM to aosd alone, so it stops the Services it supervises itself.
		KillMode=mixed
		# A heavy Agent build being OOM-killed must not take the daemon with it.
		OOMPolicy=continue
		OOMScoreAdjust=-500
		LimitNOFILE=65536
		TasksMax=infinity

		# --- On the absent hardening directives ---------------------------------
		# ProtectSystem=, ProtectHome=, PrivateTmp=, NoNewPrivileges=,
		# MemoryDenyWriteExecute=, SystemCallFilter= and RestrictNamespaces= are
		# deliberately not set here.
		#
		# They are the wrong layer, not merely inconvenient. Each applies to aosd
		# AND every descendant indiscriminately — and this process tree is half
		# privileged daemon, half deliberately-confined Agent. ProtectHome and
		# ProtectSystem contradict the product (Agents reach the whole filesystem);
		# PrivateTmp would hide /tmp from the user's own shell; NoNewPrivileges
		# inherits into every Service and apt hook; MemoryDenyWriteExecute breaks
		# node's JIT.
		#
		# Confinement here is Landlock plus uid separation, applied per Agent
		# process (internal/sandbox, ADR-0004) — strictly tighter than anything
		# above, and absent on the daemon on purpose.

		[Install]
		WantedBy=multi-user.target
		INI
	fi
	run systemctl daemon-reload
}

start_service() {
	step "Starting the service"
	# Type=notify: this returns only once the listener is actually up, so the
	# URL below is safe to print immediately.
	run systemctl enable --now aos
}

# ---------------------------------------------------------------- report

report() {
	port=7700
	[ -f "$CONF" ] && port=$(awk '/^port:/ {print $2}' "$CONF" 2>/dev/null || echo 7700)
	ip=$(hostname -I 2>/dev/null | awk '{print $1}')
	[ -n "${ip:-}" ] || ip="<this-server>"
	[ "$DRY_RUN" = 1 ] && verb="would be" || verb="is"

	cat <<-EOF

	Agentic OS ${verb} installed and running.

	  Desktop      http://${ip}:${port}
	  Sign in as   admin  (run \`sudo aos status\` for the generated password)
	  Config       ${CONF}
	  Logs         sudo aos daemon logs -f

	Next:

	  sudo aos config set api_key        # prompts; nothing lands in shell history
	  sudo aos config edit               # the model, cost limits, autonomy
	  sudo aos daemon restart

	The first sign-in will ask you to change the password. It is not optional.
	EOF
}

main() {
	say "Agentic OS installer  [PROTOTYPE]"
	[ "$DRY_RUN" = 1 ] && say "DRY RUN — nothing on this machine is changed."
	preflight
	refuse_if_busy
	fetch
	install_binary
	create_user
	create_dirs
	write_config
	install_unit
	start_service
	report
}

# Called on the last line: a truncated download of this script (a dropped
# connection mid-pipe) defines functions and then does nothing at all.
main "$@"
