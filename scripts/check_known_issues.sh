#!/usr/bin/env bash
set -euo pipefail

file="docs/known_issues.md"

if [[ ! -f "${file}" ]]; then
  echo "known-issues check failed: missing ${file}" >&2
  exit 1
fi

line_p0="$(grep -E '^\| `P0` \|' "${file}" || true)"
line_p1="$(grep -E '^\| `P1` \|' "${file}" || true)"

if [[ -z "${line_p0}" || -z "${line_p1}" ]]; then
  echo "known-issues check failed: missing P0/P1 rows in ${file}" >&2
  exit 1
fi

if [[ "${line_p0}" != *"| None |"* ]]; then
  echo "known-issues check failed: P0 status is not None" >&2
  exit 1
fi

if [[ "${line_p1}" != *"| None |"* ]]; then
  echo "known-issues check failed: P1 status is not None" >&2
  exit 1
fi

echo "known-issues check passed: no open P0/P1 issues"
