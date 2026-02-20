#!/usr/bin/env bash
set -euo pipefail

if ! command -v gh >/dev/null 2>&1; then
  echo "ci check failed: GitHub CLI (gh) is required" >&2
  exit 1
fi

if ! row="$(gh run list --workflow CI --branch master --limit 1 --json status,conclusion,createdAt,displayTitle --jq 'if length == 0 then "" else [.[0].status, .[0].conclusion, .[0].createdAt, .[0].displayTitle] | @tsv end')"; then
  echo "ci check failed: unable to query GitHub API via gh" >&2
  exit 1
fi
if [[ -z "${row}" ]]; then
  echo "ci check failed: no CI runs found for master" >&2
  exit 1
fi

IFS=$'\t' read -r status conclusion created_at title <<< "${row}"

if [[ "${status}" != "completed" || "${conclusion}" != "success" ]]; then
  echo "ci check failed: latest master CI run is ${status}/${conclusion}" >&2
  echo "title: ${title}" >&2
  echo "createdAt: ${created_at}" >&2
  exit 1
fi

echo "ci check passed: latest master CI run succeeded"
echo "title: ${title}"
echo "createdAt: ${created_at}"
