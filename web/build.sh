#!/usr/bin/env bash
# Builds web/dist: ikemen.wasm, the page, engine.zip and content.zip.
# Usage: web/build.sh [path-to-screenpack-checkout]
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
root="$here/.."
pack="${1:-$root/../Ikemen_GO-Screenpack}"
out="$here/dist"
rm -rf "$out"; mkdir -p "$out"
( cd "$root" && GOOS=js GOARCH=wasm go build -ldflags "-s -w" -o "$out/ikemen.wasm" ./src )
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$out/" 2>/dev/null || cp "$(go env GOROOT)/misc/wasm/wasm_exec.js" "$out/"
cp "$here"/{index.html,main.js,zip.js,fs.js} "$out/"
# Engine files
stage="$(mktemp -d)"; trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/e/external" "$stage/e/data"
cp -r "$root/data/." "$stage/e/data/"
cp -r "$root/font" "$stage/e/font"
cp -r "$root/external/script" "$root/external/mods" "$stage/e/external/" 2>/dev/null || true
cp "$root/src/resources/defaultMotif.ini" "$stage/e/data/system.base.def"
( cd "$stage/e" && zip -qr "$out/engine.zip" . )
# Content (screenpack): everything except work files and video
mkdir -p "$stage/c"
for d in chars data font sound stages; do [ -d "$pack/$d" ] && cp -r "$pack/$d" "$stage/c/$d"; done
( cd "$stage/c" && zip -qr "$out/content.zip" . )
ls -la "$out"
