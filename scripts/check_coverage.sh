#!/usr/bin/env bash
set -euo pipefail

# Gates total statement coverage from a Go cover profile.
# Usage: check_coverage.sh <profile> [min_percent]
#
# The minimum is intentionally a few points below the current total so
# routine variance does not flake the gate, while genuine coverage
# regressions still fail CI.

profile="${1:?usage: check_coverage.sh <profile> [min_percent]}"
min_percent="${2:-75.0}"

if [[ ! -f "$profile" ]]; then
  echo "coverage profile not found: $profile" >&2
  exit 2
fi

total="$(go tool cover -func="$profile" | awk '/^total:/ { sub(/%$/, "", $NF); print $NF }')"

if [[ -z "$total" ]]; then
  echo "could not extract total coverage from $profile" >&2
  exit 2
fi

echo "total statement coverage: ${total}% (minimum: ${min_percent}%)"

if ! awk -v total="$total" -v min="$min_percent" 'BEGIN { exit (total + 0 >= min + 0) ? 0 : 1 }'; then
  echo "coverage gate failed: ${total}% is below the ${min_percent}% minimum" >&2
  exit 1
fi
