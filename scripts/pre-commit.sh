#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

golangci-lint fmt --diff
go vet ./...
golangci-lint run --new-from-rev=HEAD --fast-only

echo "OK"
