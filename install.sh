#!/bin/sh
# Agentic OS installer — turns a fresh Ubuntu server into a Machine (ADR-0009, M6.17).
#
#   curl -fsSL https://raw.githubusercontent.com/Aman123at/agentic-os/main/install.sh | sudo sh
#
# or, to read it first (the form the docs put directly beneath the pipe):
#
#   curl -fsSL https://raw.githubusercontent.com/Aman123at/agentic-os/main/install.sh -o install.sh
#   less install.sh && sudo sh install.sh
#
# Rehearse the whole sequence, changing nothing, on any machine (including macOS):
#
#   DRY_RUN=1 sh install.sh
#
# Every mutating step goes through run() and every preflight refusal through
# fail(), so a dry run prints the complete plan — including the checks it would
# have refused on — from a machine that could never pass preflight. That is how
# this script is tested where the target cannot be: `tools/ci` runs exactly the
# command above and asserts it exits 0, and `lint` runs shellcheck over it.
#
# The release tarball this fetches (built by the `release` stage, M6.18) is
# agentic-os-linux-<arch>.tar.gz and contains two files: `aosd` (the one binary
# `aos` and the rm shim also link to) and `aos.service` (the systemd unit,
# byte-for-byte internal/daemon.Unit(), guarded by `tools/ci lint`). A sibling
# SHA256SUMS lists the tarball; verifying it verifies the unit inside it too.

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
PORT=7700               # aosd always binds :7700 on every interface (daemon_linux.go)
MIN_KERNEL_MAJOR=5
MIN_KERNEL_MINOR=13     # Landlock ABI 1 (ADR-0004)
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

	# The tarball carries aosd and aos.service; a verified tarball is a verified
	# unit, so neither is checksummed separately.
	run tar -xzf "${TMP}/${tarball}" -C "$TMP"
}

# ---------------------------------------------------------------- install

# refuse_if_busy keeps an upgrade from cancelling work. aosd interrupts every
# running Task when it stops, denies pending Approvals, and leaves them for
# `aos resume` — recoverable, but not something to do to someone silently. The
# signal is `aos tasks`: its STATE column's first word is queued, running or
# awaiting for the unfinished states, and the ID before it is always one token,
# so $2 is that word exactly. Best-effort: a spurious hit is cured by FORCE=1,
# and if aosd cannot answer the count is 0 and the upgrade proceeds.
refuse_if_busy() {
	[ "$UPGRADE" = 1 ] || return 0
	[ "${FORCE:-0}" = 1 ] && { say "   FORCE=1 — upgrading with work in flight"; return 0; }
	[ "$DRY_RUN" = 1 ] && { printf '   [dry-run] refuse if a Task is running, queued or awaiting (unless FORCE=1)\n'; return 0; }
	busy=$(aos tasks --limit 200 2>/dev/null | awk 'NR>1 && ($2=="running"||$2=="queued"||$2=="awaiting"){n++} END{print n+0}') || busy=0
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
	# aos and the Agent's rm shim are links to aosd, one binary for all three.
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
	# no_new_privs disables setuid (ADR-0004). This is passwordless root for
	# whoever signs in to the Desktop — the same fact `bind` on 0.0.0.0 forces,
	# and the docs and the report below say it plainly.
	step "Granting ${AOS_USER} sudo"
	say "   NOTE: this is passwordless root for anyone who reaches the Desktop."
	if [ "$DRY_RUN" = 1 ]; then
		printf '   [dry-run] write /etc/sudoers.d/aos: %s ALL=(ALL) NOPASSWD:ALL (validated with visudo)\n' "$AOS_USER"
	else
		umask 077
		printf '# User Sessions may use sudo. Agent Sessions cannot: no_new_privs disables setuid (ADR-0004).\n%s ALL=(ALL) NOPASSWD:ALL\n' "$AOS_USER" > /etc/sudoers.d/aos
		chmod 0440 /etc/sudoers.d/aos
		visudo -cf /etc/sudoers.d/aos >/dev/null || { rm -f /etc/sudoers.d/aos; die "the sudoers entry did not validate; removed it."; }
	fi
}

create_dirs() {
	step "Creating the state and configuration folders"
	# aosd (re)creates /var/lib/aos and its subtree at every start; creating it
	# here too is idempotent and keeps the modes explicit. /run/aos is aosd's.
	run install -d -m 0700 -o root -g root "$STATE_DIR"
	run install -d -m 0700 -o root -g root "$CONF_DIR"
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
	# This template is the product's first documentation surface. Every key here
	# is one aosd accepts: an unknown key or a bad value stops the service at
	# start, naming the line — it never falls back to a default. Secrets (the
	# OpenAI key, the JWT signing key) are NOT config: they live under
	# /var/lib/aos and the OpenAI key is set in the Desktop's System Settings.
	cat > "$CONF" <<-'YAML'
	# Agentic OS configuration (ADR-0010).
	# The single source of truth for every setting that is not a secret.
	#
	# What you change in the Desktop or with `aos config set` is written back
	# here, so a restart never reverts a setting; comments and key order survive
	# that write-back. An unknown key or a malformed value stops the service at
	# start, naming the line — it never falls back to a default.
	#
	#   aos config list           show every setting and where it came from
	#   aos config set model=...  change one, from the shell
	#   aos config edit           open this file, validated before it saves
	#
	# Secrets are not here. The OpenAI API key and the JWT signing key live under
	# /var/lib/aos; set the OpenAI key in the Desktop's System Settings.

	# --- The Agent -----------------------------------------------------------
	# The model every Task runs on, such as gpt-5.6-terra (see /var/lib/aos/models.yaml).
	model: gpt-5.6-terra
	# How hard the model thinks; empty means the model's own default. The values a
	# model accepts differ (see models.yaml): none, minimal, low, medium, high,
	# xhigh or max.
	reasoning_effort: ""
	# How bold Agents are: auto, confirm-risky or confirm-all.
	autonomy: confirm-risky
	# How many Tasks run at once (1-16).
	max_tasks: 3
	# How many times a model call is retried (0-20).
	max_retries: 3

	# --- Cost limits in USD; 0 means none ------------------------------------
	task_cost_limit_usd: 0
	daily_cost_limit_usd: 0

	# --- Trash ---------------------------------------------------------------
	# How long the Trash keeps an item, in days (1-3650).
	trash_retention_days: 30
	# Trash size cap in GB (1-1024).
	trash_max_gb: 5

	# --- Reach and platform (restart to apply) -------------------------------
	# The Machine's Mode: ui (the Desktop, reached over the network behind a
	# password) or cli (the control socket only, no web UI). aosd listens on
	# :7700 on every interface in ui Mode — reachable from the network, which is
	# why the Desktop password is effectively root on this server.
	mode: ui
	# Where Agents may write: host (all of / minus the Protected list, the native
	# install, ADR-0004) or home (only /home/aos, the Compose default). This key
	# is also the native marker: `host` is what tells aosd this is a native box.
	filesystem: host
	# OpenAI-compatible API base URL; empty uses OpenAI's own. Decides where the
	# API key is sent.
	base_url: ""
	# Refuse to start when the kernel has no Landlock, rather than run Agents
	# under policy checks only.
	require_landlock: false
	# Whether the Browser app is available (ui Mode only). `sudo aos browser
	# install` fetches Chromium's headless shell and sets this; `aos browser
	# remove` clears it (M6.11, ADR-0008).
	include_browser: false
	YAML
	chmod 0600 "$CONF"
	say "   written (0600, root)"
}

install_unit() {
	step "Installing ${UNIT}"
	# The unit ships in the tarball as aos.service — byte-for-byte daemon.Unit(),
	# which `tools/ci lint` guards — so the installer never embeds its own copy
	# that could drift. A verified tarball is a verified unit.
	run install -m 0644 -o root -g root "${TMP}/aos.service" "$UNIT"
	run systemctl daemon-reload
}

start_service() {
	step "Starting the service"
	# Type=notify: this returns only once the listener is actually up, so the
	# URL below is safe to print immediately.
	run systemctl enable --now aos
}

create_account() {
	step "Creating the Desktop account"
	# `aos mode ui` creates the one account over the local control socket and
	# prints a single-use password once — the account is deliberately never
	# creatable over the public bind (server.go), so a stranger who reaches a
	# fresh Machine cannot seize it. On a re-run it reports the account already
	# exists and changes nothing. It restarts the unit to apply ui Mode.
	if [ "$DRY_RUN" = 1 ]; then
		printf '   [dry-run] aos mode ui --user admin  (creates the account, prints the one-time password)\n'
		return 0
	fi
	aos mode ui --user admin
}

# ---------------------------------------------------------------- report

report() {
	ip=$(hostname -I 2>/dev/null | awk '{print $1}')
	[ -n "${ip:-}" ] || ip="<this-server>"
	[ "$DRY_RUN" = 1 ] && verb="would be" || verb="is"

	cat <<-EOF

	Agentic OS ${verb} installed and running.

	  Desktop      http://${ip}:${PORT}
	  Sign in as   admin  (the one-time password was printed just above)
	  Config       ${CONF}
	  Logs         sudo aos daemon logs -f

	Next:

	  1. Open the Desktop and sign in; it will make you set your own password.
	  2. In System Settings, add your OpenAI API key (it is a secret, not config).
	  3. Drive your first Task.

	The Desktop password is root on this server: it can sudo without a password,
	and the Machine listens on every interface. Put it behind a firewall or a
	reverse proxy if that is not what you want.
	EOF
}

main() {
	say "Agentic OS installer"
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
	create_account
	report
}

# Called on the last line: a truncated download of this script (a dropped
# connection mid-pipe) defines functions and then does nothing at all.
main "$@"
