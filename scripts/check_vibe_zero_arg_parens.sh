#!/usr/bin/env bash
set -euo pipefail

status=0

echo "checking .vibe style: no empty parentheses for zero-arg defs/autoinvoked calls"

if ! command -v rg >/dev/null 2>&1; then
  echo "error: ripgrep (rg) is required for style checks" >&2
  exit 1
fi

def_pattern='^[[:space:]]*((export|private)[[:space:]]+)*def[[:space:]]+([A-Za-z_][A-Za-z0-9_]*\.)?[A-Za-z_][A-Za-z0-9_!?]*[[:space:]]*\([[:space:]]*\)'
if rg -n --glob '*.vibe' "${def_pattern}" >/tmp/vibe_style_defs.txt; then
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
auto_builtin_call_pattern='(^|[^A-Za-z0-9_])(now|uuid)[[:space:]]*\([[:space:]]*\)'
if rg -n --glob '*.vibe' "${auto_builtin_call_pattern}" >/tmp/vibe_style_calls.txt; then
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

method_call_pattern='\.[A-Za-z_][A-Za-z0-9_!?]*[[:space:]]*\([[:space:]]*\)'
if rg -n --glob '*.vibe' "${method_call_pattern}" >/tmp/vibe_style_method_calls.txt; then
  echo "style violations: zero-arg method calls must omit parentheses" >&2
  cat /tmp/vibe_style_method_calls.txt >&2
  status=1
else
  rc=$?
  if [[ ${rc} -ne 1 ]]; then
    echo "error: failed while scanning method calls for style violations" >&2
    status=1
  fi
fi

if [[ ${status} -ne 0 ]]; then
  exit ${status}
fi

echo "vibe style check passed"
