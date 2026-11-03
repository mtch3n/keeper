#!/bin/sh
# Fails when anything under plugin/ changed since the merge base without a
# version bump in plugin/.claude-plugin/plugin.json, or when the two manifests
# disagree on the version. An installed plugin is cached by version, so a change
# that keeps the version never reaches a machine that has it.
#
# usage: check-plugin-version.sh [base-ref]   (default: origin/main, else main)
set -eu

version() { sed -n 's/^.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1; }

claude="$(version < plugin/.claude-plugin/plugin.json)"
codex="$(version < plugin/.codex-plugin/plugin.json)"
if [ "$claude" != "$codex" ]; then
    echo "plugin versions differ: .claude-plugin says $claude, .codex-plugin says $codex" >&2
    exit 1
fi

base="${1:-}"
if [ -z "$base" ]; then
    for ref in origin/main main; do
        if git rev-parse --verify -q "$ref^{commit}" >/dev/null; then
            base="$ref"
            break
        fi
    done
fi
if [ -z "$base" ]; then
    echo "check-plugin-version: no base ref to compare with, skipped" >&2
    exit 0
fi

mb="$(git merge-base HEAD "$base")"
if [ -z "$(git diff --name-only "$mb" -- plugin)" ]; then
    exit 0
fi

was="$(git show "$mb:plugin/.claude-plugin/plugin.json" 2>/dev/null | version || true)"
if [ "$was" = "$claude" ]; then
    echo "plugin/ changed since $base but the version is still $claude: bump it in plugin/.claude-plugin/plugin.json and plugin/.codex-plugin/plugin.json" >&2
    exit 1
fi
