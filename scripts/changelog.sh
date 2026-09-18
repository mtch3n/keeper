#!/usr/bin/env bash
# Splice a generated release section into CHANGELOG.md, under the preamble.
#
# git-cliff's own --prepend writes to the very top of the file, which is above
# the preamble rather than under it, and it re-emits its configured header on
# every run. 0.0.3 shipped with two "# Changelog" headings because of the
# second problem and the duplicate was removed by hand. `header` is empty in
# cliff.toml now, and this handles the first: the preamble stays in
# CHANGELOG.md where a reader finds it, and each new section lands immediately
# above the newest existing one.
#
#   scripts/changelog.sh v0.0.4
set -euo pipefail

tag="${1:-}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
file="$root/CHANGELOG.md"

section="$(mktemp)"
trap 'rm -f "$section"' EXIT

if [ -n "$tag" ]; then
  uvx git-cliff@2.10.1 --unreleased --tag "$tag" > "$section"
else
  uvx git-cliff@2.10.1 --unreleased > "$section"
fi

if [ "$(tr -d '[:space:]' < "$section" | wc -c)" -lt 20 ]; then
  echo "changelog: git-cliff produced nothing; is there anything since the last tag?" >&2
  exit 1
fi

# Insert before the first release heading, so the preamble above it survives.
awk -v sect="$section" '
  !done && /^## / {
    while ((getline line < sect) > 0) print line
    close(sect)
    done = 1
  }
  { print }
' "$file" > "$file.new"

mv "$file.new" "$file"
echo "changelog: ${tag:-unreleased} section added"
