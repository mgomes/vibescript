#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

stash_created=0
if ! git diff --quiet || [ -n "$(git ls-files --others --exclude-standard)" ]; then
    if git stash push --keep-index --include-untracked --quiet --message "pre-commit-hook-stash"; then
        if git stash list | grep -q "pre-commit-hook-stash"; then
            stash_created=1
        fi
    fi
fi

restore() {
    if [ "$stash_created" -eq 1 ]; then
        if ! git stash pop --quiet; then
            echo "ERROR: failed to restore stashed changes; resolve conflicts manually (see 'git stash list')." >&2
        fi
    fi
}
trap restore EXIT

golangci-lint fmt --diff
go vet ./...
golangci-lint run --new-from-rev=HEAD --fast-only

echo "OK"
