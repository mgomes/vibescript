#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

mapfile -t staged < <(git diff --name-only --cached --diff-filter=ACMR)
partial=()
for f in "${staged[@]:-}"; do
    if [ -n "$f" ] && ! git diff --quiet -- "$f"; then
        partial+=("$f")
    fi
done

if [ "${#partial[@]}" -gt 0 ]; then
    {
        echo "pre-commit: refusing to lint while these files are partially staged:"
        printf '  %s\n' "${partial[@]}"
        echo
        echo "Stage the remaining changes, commit the partial portion separately,"
        echo "or bypass the hook with: git commit --no-verify"
    } >&2
    exit 1
fi

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
            echo "pre-commit: stash pop failed; your changes remain in 'git stash list'" >&2
        fi
    fi
}
trap restore EXIT

golangci-lint fmt --diff
go vet ./...
golangci-lint run --new-from-rev=HEAD --fast-only

echo "OK"
