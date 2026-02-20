#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: scripts/release_checklist.sh <version>" >&2
  echo "example: scripts/release_checklist.sh v0.19.0" >&2
  exit 2
fi

version="$1"
if [[ ! "${version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "invalid version '${version}': expected vMAJOR.MINOR.PATCH" >&2
  exit 2
fi

fail() {
  echo "release checklist failed: $1" >&2
  exit 1
}

[[ -f CHANGELOG.md ]] || fail "missing CHANGELOG.md"
[[ -f ROADMAP.md ]] || fail "missing ROADMAP.md"
[[ -f cmd/vibes/repl.go ]] || fail "missing cmd/vibes/repl.go"

version_ere="${version//./\\.}"
grep -Eq "^## ${version_ere}( |$)" CHANGELOG.md || fail "CHANGELOG.md missing heading for ${version}"
grep -Eq "^## ${version_ere}( |$)" ROADMAP.md || fail "ROADMAP.md missing milestone heading for ${version}"
grep -Fq "version := mutedStyle.Render(\"${version}\")" cmd/vibes/repl.go || fail "REPL version label does not match ${version}"

is_tag_trigger=0
if [[ "${GITHUB_EVENT_NAME:-}" == "push" && "${GITHUB_REF_TYPE:-}" == "tag" && "${GITHUB_REF_NAME:-}" == "${version}" ]]; then
  is_tag_trigger=1
fi

if [[ "${is_tag_trigger}" -eq 0 ]] && git rev-parse -q --verify "refs/tags/${version}" >/dev/null 2>&1; then
  fail "git tag ${version} already exists locally"
fi

echo "release checklist passed for ${version}"
