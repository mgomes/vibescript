#!/usr/bin/env bash
set -euo pipefail

status=0

echo "checking .vibe style: no empty parentheses for zero-arg defs/autoinvoked calls"

if ! command -v rg >/dev/null 2>&1; then
  echo "error: ripgrep (rg) is required for style checks" >&2
  exit 1
fi

if rg -n --glob '*.vibe' '^\s*(export\s+)?def\s+[A-Za-z_][A-Za-z0-9_!?]*\s*\(\s*\)' >/tmp/vibe_style_defs.txt; then
  echo "style violations: zero-arg method definitions must omit parentheses" >&2
  cat /tmp/vibe_style_defs.txt >&2
  status=1
else
  rc=$?
  if [[ ${rc} -ne 1 ]]; then
    echo "error: failed while scanning definitions for style violations" >&2
    status=1
  fi
fi

# Only check builtins that are auto-invoked without `()`.
if rg -n --glob '*.vibe' '\b(now|uuid)\s*\(\s*\)' >/tmp/vibe_style_calls.txt; then
  echo "style violations: auto-invoked zero-arg calls must omit parentheses" >&2
  cat /tmp/vibe_style_calls.txt >&2
  status=1
else
  rc=$?
  if [[ ${rc} -ne 1 ]]; then
    echo "error: failed while scanning calls for style violations" >&2
    status=1
  fi
fi

if [[ ${status} -ne 0 ]]; then
  exit ${status}
fi

echo "vibe style check passed"
