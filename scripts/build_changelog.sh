#!/usr/bin/env bash
#
# build_changelog.sh — compile changelog.d fragments into CHANGELOG.md.
#
# Usage: scripts/build_changelog.sh <version> [release-date]
#
# Moves every changelog.d/*.md fragment (sorted by filename, README.md
# excluded) into a new "## [<version>] - <date>" section inserted directly
# below the "## Unreleased" heading of CHANGELOG.md, then deletes the
# fragments. The release date defaults to today (UTC, YYYY-MM-DD).
set -euo pipefail

if [[ $# -lt 1 || $# -gt 2 ]]; then
	echo "usage: $0 <version> [release-date]" >&2
	exit 2
fi

version="$1"
release_date="${2:-$(date -u +%Y-%m-%d)}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
changelog="$repo_root/CHANGELOG.md"
fragment_dir="$repo_root/changelog.d"

if [[ ! -f "$changelog" ]]; then
	echo "error: $changelog not found" >&2
	exit 1
fi

# Collect fragments in sorted order, excluding the README.
shopt -s nullglob
fragments=()
for f in "$fragment_dir"/*.md; do
	[[ "$(basename "$f")" == "README.md" ]] && continue
	fragments+=("$f")
done
shopt -u nullglob

if [[ ${#fragments[@]} -eq 0 ]]; then
	echo "error: no changelog fragments found in $fragment_dir" >&2
	exit 1
fi

if ! grep -q '^## Unreleased$' "$changelog"; then
	echo "error: '## Unreleased' heading not found in $changelog" >&2
	exit 1
fi

# Build the new version section body from the fragments.
section_body="$(mktemp)"
trap 'rm -f "$section_body"' EXIT
for f in "${fragments[@]}"; do
	cat "$f" >>"$section_body"
	# Guarantee a newline boundary between fragments.
	[[ -n "$(tail -c1 "$f")" ]] && echo >>"$section_body"
done

# Insert the new version section after the entire "## Unreleased" section (so the
# Unreleased heading and its standing notes are preserved), i.e. just before the
# next "## " heading, or at end of file when Unreleased is the last section.
awk -v version="$version" -v reldate="$release_date" -v body_file="$section_body" '
	function emit_section() {
		print "## " version " - " reldate
		print ""
		while ((getline line < body_file) > 0) {
			print line
		}
		close(body_file)
		print ""
		inserted = 1
	}
	BEGIN { in_unreleased = 0; inserted = 0 }
	/^## Unreleased$/ { in_unreleased = 1; print; next }
	/^## / && in_unreleased && !inserted {
		# Reached the next section: emit the new version section ahead of it.
		emit_section()
		in_unreleased = 0
		print
		next
	}
	{ print }
	END {
		if (in_unreleased && !inserted) {
			# Unreleased was the final section; append at end of file.
			print ""
			emit_section()
		}
		if (!inserted) {
			print "error: insertion point not reached" > "/dev/stderr"
			exit 1
		}
	}
' "$changelog" >"$changelog.tmp"

mv "$changelog.tmp" "$changelog"

# Remove the consumed fragments.
for f in "${fragments[@]}"; do
	rm -f "$f"
done

echo "Compiled ${#fragments[@]} fragment(s) into ## [$version] - $release_date"
