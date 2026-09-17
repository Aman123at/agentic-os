#!/bin/sh
# Generate the command reference from the real aos command tree.
#
#   sh tools/spikes/docs-site/refgen/run.sh [outdir]
#
# Two things keep this spike out of the production build:
#
#  - The sources are *.go.txt, so `go vet ./...` (tools/ci lint) never sees a
#    program that calls cli.Root() before M6 adds it. They are copied to a temp
#    directory as .go and run from there.
#  - export_shim.go is mapped into package cli with `go run -overlay`, so the
#    tree gains an exported Root() for exactly one build and `git status` stays
#    clean. M6 does it honestly: rename rootCmd to Root at internal/cli/cli.go:40.
set -eu
root=$(cd "$(dirname "$0")/../../../.." && pwd)
spike="$root/tools/spikes/docs-site"
out=${1:-"$spike/src/content/docs/reference"}
# The temp directory must sit inside the module: Go refuses an internal/ import
# from a file outside the module tree. A dot-prefixed name keeps it out of ./...
tmp="$root/.refgen-spike"
trap 'rm -rf "$tmp"' EXIT
rm -rf "$tmp"
mkdir -p "$tmp"
cp "$spike/refgen/refgen.go.txt" "$tmp/refgen.go"
cp "$spike/refgen/export_shim.go.txt" "$tmp/export_shim.go"
cat > "$tmp/overlay.json" <<JSON
{"Replace": {"$root/internal/cli/zz_refgen_export.go": "$tmp/export_shim.go"}}
JSON
cd "$root"
go run -overlay "$tmp/overlay.json" "$tmp/refgen.go" "$out"
