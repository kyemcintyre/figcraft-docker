#!/usr/bin/env bash
# Adds the fonts listed in web-fonts.txt to a craft-fonts checkout, so FigCraft's web build can
# embed them (crates/text/build.rs reads fonts/latin-manifest.txt and embeds the families named in
# FIGCRAFT_WEB_FONTS).
#
# Usage: add-fonts.sh <web-fonts.txt> <craft-fonts dir> <google/fonts commit>
#
# Each file is downloaded from google/fonts at that commit and checked against its sha256, then
# copied to fonts/<family-slug>/ with a row in fonts/latin-manifest.txt. Every family must have
# its OFL.txt ("Licence" row). Prints the families, comma-separated, for FIGCRAFT_WEB_FONTS.
set -euo pipefail

list="$1"
fonts="$2"
ref="$3"

[[ "$ref" =~ ^[0-9a-f]{40}$ ]] || { echo "google/fonts ref must be a full commit SHA: $ref" >&2; exit 1; }
manifest="$fonts/fonts/latin-manifest.txt"
[[ -f "$manifest" ]] || { echo "$manifest missing: not a craft-fonts checkout?" >&2; exit 1; }

families=()
declare -A licensed=()
declare -A has_face=()
rows=""

while IFS= read -r line || [[ -n "$line" ]]; do
  line="${line%%$'\r'}"
  [[ -z "${line// }" || "$line" == \#* ]] && continue
  IFS='|' read -r family style path sha extra <<<"$line"
  family="$(echo "$family" | xargs)"; style="$(echo "$style" | xargs)"
  path="$(echo "$path" | xargs)"; sha="$(echo "$sha" | xargs)"
  [[ -z "${extra:-}" && -n "$family" && -n "$style" && -n "$path" && -n "$sha" ]] \
    || { echo "malformed row: $line" >&2; exit 1; }
  [[ "$family" =~ ^[A-Za-z0-9\ ]+$ ]] || { echo "family must be letters, digits and spaces: $family" >&2; exit 1; }
  [[ "$style" =~ ^[A-Za-z\ ]+$ ]] || { echo "bad style: $style" >&2; exit 1; }
  # "]" first in the bracket expression: variable fonts are named like Jost[wght].ttf.
  path_re='^[a-z0-9]+/[][A-Za-z0-9._,-]+$'
  [[ "$path" =~ $path_re && "$path" != *..* ]] || { echo "path must be <dir>/<file> under ofl/: $path" >&2; exit 1; }
  [[ "$sha" =~ ^[0-9a-f]{64}$ ]] || { echo "bad sha256 for $path" >&2; exit 1; }

  slug="$(echo "$family" | tr 'A-Z' 'a-z' | tr ' ' '-')"
  dir="$fonts/fonts/$slug"
  mkdir -p "$dir"
  url_path="${path//[/%5B}"; url_path="${url_path//]/%5D}"
  tmp="$(mktemp)"
  curl --proto '=https' -fsSL -o "$tmp" "https://raw.githubusercontent.com/google/fonts/$ref/ofl/$url_path"
  echo "$sha  $tmp" | sha256sum -c --quiet - || { echo "sha256 mismatch: $path" >&2; exit 1; }

  if [[ "$style" == "Licence" ]]; then
    mv "$tmp" "$dir/OFL.txt"
    licensed[$family]=1
    continue
  fi
  # Brackets in variable font names ([wght]) stay out of the copied name.
  name="$(basename "$path" | sed 's/\[[^]]*\]//g')"
  mv "$tmp" "$dir/$name"
  rows+="$family | $style | fonts/$slug/$name | Latn | OFL-1.1 | fonts/$slug/OFL.txt | $sha | https://github.com/google/fonts/tree/$ref/ofl/$(dirname "$path")"$'\n'
  if [[ -z "${has_face[$family]:-}" ]]; then
    has_face[$family]=1
    families+=("$family")
  fi
done <"$list"

for family in "${families[@]}"; do
  [[ -n "${licensed[$family]:-}" ]] || { echo "$family has no Licence row (its OFL.txt)" >&2; exit 1; }
  if grep -q "^$family |" "$manifest"; then
    echo "$family is already in craft-fonts: drop it from web-fonts.txt" >&2
    exit 1
  fi
done
[[ ${#families[@]} -gt 0 ]] || { echo "no fonts listed in $list" >&2; exit 1; }

{
  echo
  echo "# Added by figcraft-docker (web-fonts.txt) from google/fonts $ref"
  printf '%s' "$rows"
} >>"$manifest"

(IFS=,; echo "${families[*]}")
