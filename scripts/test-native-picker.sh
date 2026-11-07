#!/bin/sh
# A hidden native component check; no AX/global mouse input or screen capture.
set -eu

if [ "$(uname -s)" != 'Darwin' ]; then
  printf '%s\n' 'SKIP: native picker smoke test requires macOS and a WindowServer session.'
  exit 0
fi
project_dir="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
probe_dir="$(mktemp -d "${TMPDIR:-/tmp}/cuevia-picker-test.XXXXXX")"
trap 'rm -rf "$probe_dir"' EXIT HUP INT TERM
/usr/bin/xcrun clang -fblocks -Wno-deprecated-declarations \
  -framework Cocoa -framework Carbon -framework QuartzCore \
  "$project_dir/source_picker_darwin.m" "$project_dir/tests/native_picker_smoke.m" \
  -o "$probe_dir/native-picker-smoke"
"$probe_dir/native-picker-smoke"
