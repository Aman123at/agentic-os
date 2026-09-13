#!/bin/bash
# Container B/C (offline, fresh image): Replay the Ledger from the cache.
set -uo pipefail
L=/var/cache/aos/ledger
mode=$1
getent hosts archive.ubuntu.com >/dev/null && echo "WARNING: network available" || echo "network: none"
case $mode in
  lists)
    # Strategy 1: exact versions resolved through the package lists kept in the volume.
    time DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-download --no-install-recommends $(cat $L/explicit) >/tmp/log 2>&1; echo "apt exit $?"; tail -3 /tmp/log ;;
  debs)
    # Strategy 2: no package lists at all; install the exact cached .deb files.
    files=()
    while IFS= read -r line; do
      pa=${line%%=*}; ver=${line#*=}; name=${pa%%:*}; arch=${pa#*:}
      f=/var/cache/aos/apt/archives/${name}_${ver//:/%3a}_${arch}.deb
      [ -f "$f" ] || { echo "missing $f"; continue; }
      files+=("$f")
    done < $L/all
    time DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-download -o Dir::State::lists=/tmp/nolists -o Dir::State::Lists=/tmp/nolists "${files[@]}" >/tmp/log 2>&1; echo "apt exit $?"; tail -3 /tmp/log
    # Everything installed from files is marked manual; restore the auto flags from the Ledger.
    comm -23 <(cut -d= -f1 $L/all | cut -d: -f1 | sort) <(cut -d= -f1 $L/explicit | sort) | xargs -r apt-mark auto >/dev/null ;;
esac
bad=0
while IFS= read -r line; do
  pa=${line%%=*}; want=${line#*=}
  got=$(dpkg-query -W -f='${Version}' "$pa" 2>/dev/null || echo missing)
  [ "$got" = "$want" ] || { echo "MISMATCH $pa want $want got $got"; bad=$((bad+1)); }
done < $L/all
echo "verified $(wc -l < $L/all) packages at exact versions, $bad mismatches"
nginx -v 2>&1; rg --version | head -1; figlet ok
