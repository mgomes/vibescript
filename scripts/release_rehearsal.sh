#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: scripts/release_rehearsal.sh <version>" >&2
  echo "example: scripts/release_rehearsal.sh v0.19.0" >&2
  exit 2
fi

version="$1"
if [[ ! "${version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "invalid version '${version}': expected vMAJOR.MINOR.PATCH" >&2
  exit 2
fi

echo "[1/4] Checking known issues bug bar"
./scripts/check_known_issues.sh

echo "[2/4] Running full test suite"
go test ./...

echo "[3/4] Running release checklist"
./scripts/release_checklist.sh "${version}"

echo "[4/4] Running GoReleaser dry run (if installed)"
if command -v goreleaser >/dev/null 2>&1; then
  goreleaser release --snapshot --clean --skip=publish
else
  echo "goreleaser not found; skipping dry run"
fi

echo "release rehearsal passed for ${version}"
