#!/usr/bin/env bash
# Makes a FigCraft source tree's web build embed the families named in FIGCRAFT_WEB_FONTS.
#
# Usage: patch-web-fonts.sh <figcraft source dir>
#
# Newer FigCraft (main after 0.16.0) reads FIGCRAFT_WEB_FONTS in crates/text/build.rs itself;
# then nothing is changed. Releases before it (0.16.0) keep the web build to a fixed list
# (Inter, Noto Sans Georgian, Archivo SemiBold) and ignore the variable silently, so this adds
# the variable's families to that one condition. The line must match exactly: if a release
# changes it, this fails rather than building a web app without the fonts.
set -euo pipefail

build_rs="$1/crates/text/build.rs"
[[ -f "$build_rs" ]] || { echo "$build_rs missing" >&2; exit 1; }

if grep -q 'FIGCRAFT_WEB_FONTS' "$build_rs"; then
  echo "crates/text/build.rs reads FIGCRAFT_WEB_FONTS itself: not patched"
  exit 0
fi

old='        if wasm && !matches!(*family, "Inter" | "Noto Sans Georgian") && !wordmark && !cjk {'
new='        if wasm && !matches!(*family, "Inter" | "Noto Sans Georgian") && !wordmark && !cjk && !std::env::var("FIGCRAFT_WEB_FONTS").unwrap_or_default().split('"','"').any(|f| f.trim() == *family) {'

count=$(grep -cxF -- "$old" "$build_rs" || true)
[[ "$count" == 1 ]] || {
  echo "crates/text/build.rs: expected the web font condition once, found it $count times; update this patch" >&2
  exit 1
}

tmp="$(mktemp)"
OLD="$old" NEW="$new" awk '$0 == ENVIRON["OLD"] { print ENVIRON["NEW"]; next } { print }' "$build_rs" >"$tmp"
cat "$tmp" >"$build_rs"
rm -f "$tmp"
grep -qxF -- "$new" "$build_rs" || { echo "patch didn't apply" >&2; exit 1; }
echo "crates/text/build.rs: web build now also embeds FIGCRAFT_WEB_FONTS families"
