#!/usr/bin/env bash
# Stops the FigCraft web app crashing when someone posts a comment.
#
# Usage: patch-web-comments.sh <figcraft source dir>
#
# The engine's event log read the clock through std::time, which panics in the browser
# (wasm32-unknown-unknown: "time not implemented on this platform"), and every comment command
# logs an event, so the browser app stopped with "FigCraft stopped because of an internal error"
# right after a comment synced. Fixed upstream on main by storytold/figcraft#1766 (issue #1764,
# commit 7bee269c), after 0.16.0 was released without it. This applies the same change: take
# Instant, SystemTime and UNIX_EPOCH from web_time (std::time on native, the JavaScript clock in
# the browser; the engine already depends on web-time). Source that already has the fix is left
# alone; source where the line has changed fails, rather than building the bug back in.
set -euo pipefail

events_rs="$1/crates/engine/src/events.rs"
cargo_toml="$1/crates/engine/Cargo.toml"
[[ -f "$events_rs" ]] || { echo "$events_rs missing" >&2; exit 1; }

if grep -q '^use web_time::{Instant, SystemTime, UNIX_EPOCH};$' "$events_rs"; then
  echo "crates/engine/src/events.rs already reads the clock through web_time: not patched"
  exit 0
fi

grep -q '^web-time = ' "$cargo_toml" || { echo "$cargo_toml has no web-time dependency; update this patch" >&2; exit 1; }

old='use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};'
count=$(grep -cxF -- "$old" "$events_rs" || true)
[[ "$count" == 1 ]] || {
  echo "crates/engine/src/events.rs: expected the std::time import once, found it $count times; update this patch" >&2
  exit 1
}

tmp="$(mktemp)"
OLD="$old" awk '
  $0 == ENVIRON["OLD"] {
    print "use std::time::Duration;"
    print "// Patched by figcraft-docker (storytold/figcraft#1766): std::time panics in the browser."
    print "use web_time::{Instant, SystemTime, UNIX_EPOCH};"
    next
  }
  { print }
' "$events_rs" >"$tmp"
cat "$tmp" >"$events_rs"
rm -f "$tmp"

grep -qxF 'use web_time::{Instant, SystemTime, UNIX_EPOCH};' "$events_rs" || { echo "patch didn't apply" >&2; exit 1; }
if grep -q 'std::time::\(SystemTime\|Instant\)' "$events_rs"; then
  echo "crates/engine/src/events.rs still uses std::time's clock elsewhere; update this patch" >&2
  exit 1
fi
echo "crates/engine/src/events.rs: the event log now reads the clock through web_time"
