#!/bin/sh
# SPEC R7.4b — EXPLAIN ANALYZE is prohibited in the codebase, enforced by a lint
# rule rather than a test, because Go cannot make a string literal fail to
# compile. EXPLAIN ANALYZE executes the statement it claims to be planning, so a
# single occurrence turns a dry run into a live one.
#
# The prohibition covers the enforcement path. Occurrences inside this script,
# and in prose explaining the rule, are exempt by construction: only .go files
# are searched.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
hits=$(grep -rniE 'EXPLAIN[[:space:]]+(\([^)]*\)[[:space:]]*)?ANALYZE' \
	--include='*.go' "$root" | grep -v '_test\.go:.*must not\|prohibited\|R7\.4b' || true)
if [ -n "$hits" ]; then
	echo "EXPLAIN ANALYZE is prohibited (SPEC R7.4b). Found:"
	echo "$hits"
	exit 1
fi
echo "lint-explain: clean"
