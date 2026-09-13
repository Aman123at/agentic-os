#!/bin/bash
# Container A (online): install 10 packages and record an Install Ledger.
set -euo pipefail
PKGS="nginx sqlite3 ripgrep fd-find bat zstd ncdu tmux figlet cowsay"
dpkg-query -W -f='${Package}:${Architecture}\n' | sort > /tmp/before
time apt-get update -qq
time DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends $PKGS >/dev/null
dpkg-query -W -f='${Package}:${Architecture}\n' | sort > /tmp/after
L=/var/cache/aos/ledger
mkdir -p $L
: > $L/explicit; : > $L/all
for p in $PKGS; do dpkg-query -W -f='${Package}=${Version}\n' "$p" >> $L/explicit; done
comm -13 /tmp/before /tmp/after | while read -r pa; do dpkg-query -W -f='${Package}:${Architecture}=${Version}\n' "$pa"; done > $L/all
echo "explicit: $(wc -l < $L/explicit), with dependencies: $(wc -l < $L/all)"
ls /var/cache/aos/apt/archives/*.deb | wc -l | xargs echo "cached .debs:"
du -sh /var/cache/aos/apt/archives /var/cache/aos/apt/lists
