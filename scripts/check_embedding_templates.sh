#!/usr/bin/env bash
#
# check_embedding_templates.sh — compile every embedding starter template.
#
# The templates are named main.go.tmpl so `go build ./...` and `go vet ./...`
# skip them, which let all three drift out of sync with the embedding API and
# stop compiling (see issue #1064). This materializes each one into a throwaway
# module that points back at the repo and builds it, so a template that no
# longer compiles fails CI instead of reaching a new embedder.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
templates_dir="$repo_root/templates/embedding"

if [[ ! -d "$templates_dir" ]]; then
	echo "error: $templates_dir not found" >&2
	exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

failed=()
checked=0

for tmpl in "$templates_dir"/*/main.go.tmpl; do
	[[ -e "$tmpl" ]] || continue
	name="$(basename "$(dirname "$tmpl")")"
	dir="$work/$name"
	mkdir -p "$dir"
	cp "$tmpl" "$dir/main.go"

	# Copy any sibling files the template needs at runtime (module sources and
	# similar), preserving their relative layout.
	(cd "$(dirname "$tmpl")" && find . -type f ! -name 'main.go.tmpl' -exec cp --parents {} "$dir/" \; 2>/dev/null) || \
		(cd "$(dirname "$tmpl")" && tar cf - --exclude='main.go.tmpl' . 2>/dev/null | tar xf - -C "$dir" 2>/dev/null) || true

	cat > "$dir/go.mod" <<EOF
module embeddingtemplate/$name

go 1.26

require github.com/mgomes/vibescript v0.0.0

replace github.com/mgomes/vibescript => $repo_root
EOF

	echo "building template: $name"
	if ! (cd "$dir" && GOFLAGS=-mod=mod go build ./... 2>&1); then
		failed+=("$name")
	fi
	checked=$((checked + 1))
done

if [[ $checked -eq 0 ]]; then
	echo "error: no embedding templates found to check" >&2
	exit 1
fi

if [[ ${#failed[@]} -gt 0 ]]; then
	echo "embedding templates failed to compile: ${failed[*]}" >&2
	exit 1
fi

echo "all $checked embedding template(s) compile"
