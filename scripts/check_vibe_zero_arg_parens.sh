#!/usr/bin/env bash
set -euo pipefail

status=0

echo "checking .vibe style: no empty parentheses for zero-arg defs/calls"

if rg -n --glob '*.vibe' '^\s*(export\s+)?def\s+[A-Za-z_][A-Za-z0-9_!?]*\s*\(\s*\)' >/tmp/vibe_style_defs.txt; then
  echo "style violations: zero-arg method definitions must omit parentheses" >&2
  cat /tmp/vibe_style_defs.txt >&2
  status=1
fi

if rg -n --glob '*.vibe' '\b[A-Za-z_][A-Za-z0-9_!?]*\s*\(\s*\)' >/tmp/vibe_style_calls.txt; then
  echo "style violations: zero-arg method calls must omit parentheses" >&2
  cat /tmp/vibe_style_calls.txt >&2
  status=1
fi

if [[ ${status} -ne 0 ]]; then
  exit ${status}
fi

echo "vibe style check passed"
