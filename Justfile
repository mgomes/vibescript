set shell := ["bash", "-lc"]

test:
	go test ./...

test-race:
	go test -race ./...

fuzz fuzztime='10s':
	#!/usr/bin/env bash
	set -euo pipefail

	fuzztime="{{fuzztime}}"

	for target in \
		FuzzFormatVibeSource \
		FuzzCLIArgumentAndPathInputs \
		FuzzREPLInputFlow \
		FuzzLSPPayloadAndMessageHandling
	do
		go test ./cmd/vibes -run=^$ -fuzz="$target" -fuzztime="$fuzztime"
	done

	for target in \
		FuzzLexerTokenStreamTerminates \
		FuzzParserSuccessfulProgramsHaveCompleteAST \
		FuzzCompileScriptDoesNotPanic \
		FuzzGeneratedScriptSemantics \
		FuzzRuntimeEdgeCasesDoNotPanic \
		FuzzJSONValueRoundTripPreservesStructure \
		FuzzValueOperationsPreserveInvariants \
		FuzzModuleRequestNormalization \
		FuzzModuleAliasValidation \
		FuzzScalarInputParsersAndConversions \
		FuzzModulePolicyValidation \
		FuzzCapabilityInputValidation
	do
		go test ./vibes -run=^$ -fuzz="$target" -fuzztime="$fuzztime"
	done

bench:
	scripts/bench_runtime.sh

bench-profile pattern='^BenchmarkExecutionArrayPipeline$':
	scripts/bench_profile.sh --pattern "{{pattern}}"

lint:
	golangci-lint fmt --diff
	golangci-lint run --timeout=10m

lint-fix:
	golangci-lint fmt
	golangci-lint run --timeout=10m --fix

precommit-install:
	#!/usr/bin/env bash
	set -euo pipefail

	repo_root="$(git rev-parse --show-toplevel)"
	common_dir="$(git rev-parse --git-common-dir)"
	hook_path="$common_dir/hooks/pre-commit"
	source_path="$repo_root/scripts/pre-commit.sh"

	mkdir -p "$(dirname "$hook_path")"
	cp "$source_path" "$hook_path"
	chmod +x "$hook_path"

	echo "Installed pre-commit hook at $hook_path"

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
