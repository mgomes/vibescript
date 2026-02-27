set shell := ["bash", "-lc"]

test:
	go test ./...

test-race:
	go test -race ./...

bench:
	scripts/bench_runtime.sh

bench-profile pattern='^BenchmarkExecutionArrayPipeline$':
	scripts/bench_profile.sh --pattern "{{pattern}}"

lint:
	gofmt -l . | (! read)
	golangci-lint run --timeout=10m

repl:
	go build -o vibes-cli ./cmd/vibes && ./vibes-cli repl

install dest='':
	#!/usr/bin/env bash
	set -euo pipefail

	dest="{{dest}}"
	if [[ -z "$dest" ]]; then
		dest="$(go env GOBIN)"
	fi
	if [[ -z "$dest" ]]; then
		dest="$(go env GOPATH)/bin"
	fi

	mkdir -p "$dest"
	GOBIN="$dest" go install ./cmd/vibes

	echo "Installed vibes to $dest/vibes"
	if [[ ":$PATH:" != *":$dest:"* ]]; then
		echo "PATH does not include $dest"
		echo "Add it with: export PATH=\"$dest:\$PATH\""
	fi
