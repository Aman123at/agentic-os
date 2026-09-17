#!/bin/sh
# PROTOTYPE — the shape of `aos uninstall`, written as shell so it can be read
# and argued over. The real one is a Go subcommand of the aos binary, not a
# script: it has to answer `aos status` questions (is a Task running?) and it
# must not depend on a file that uninstalling deletes.
#
# Rehearse: sh tools/spikes/install/uninstall-sketch.sh --purge

set -eu

PURGE=0
[ "${1:-}" = "--purge" ] && PURGE=1

KEEP="/home/aos                  the Agent's home: files, Sessions, anything it made
  /var/lib/aos               the database, the Install Ledger, the API key, Trash
  /etc/aos/config.yml        your settings"

REMOVE="/usr/local/bin/aosd, /usr/local/bin/aos
  /usr/local/lib/aos/        the Agent's rm shim
  /etc/systemd/system/aos.service  (stopped and disabled first)
  /etc/sudoers.d/aos
  /run/aos/"

cat <<EOF
This will remove:

  $REMOVE
EOF

if [ "$PURGE" = 1 ]; then
	cat <<-EOF

	--purge also removes, permanently:

	  $KEEP
	  the aos user and group

	EOF
else
	cat <<-EOF

	It will KEEP:

	  $KEEP
	  the aos user and group

	Re-running install.sh later picks all of it back up exactly as it was.
	Add --purge to delete it instead.

	EOF
fi

# Everything below is what the Go verb does, in order.
#
#   1. Refuse if a Task is running, unless --force. Same rule as an upgrade.
#   2. Print the list above and require an explicit confirmation — uninstall is
#      the one place in this product where a prompt is right, because it is
#      irreversible and cannot be non-interactive by default. `--yes` skips it
#      for scripts.
#   3. systemctl disable --now aos; systemctl daemon-reload
#   4. Remove the files listed above.
#   5. With --purge: userdel -r aos, then rm -rf /var/lib/aos /etc/aos.
#      WITHOUT --purge the user survives, because /home/aos survives and an
#      orphaned home owned by a recycled uid is how files end up readable by
#      the next account the system creates.
#   6. Print what was kept and where, so nothing is a surprise later.
#
# Not done, deliberately: nothing touches packages the Agent installed. The
# Install Ledger recorded them, but uninstalling AOS is not a reason to
# uninstall nginx. `aos checkpoint restore` is the verb for that, before this.
