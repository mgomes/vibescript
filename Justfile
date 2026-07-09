set shell := ["bash", "-lc"]

test:
	go test ./...

test-race:
	go test -race ./...

leakcheck:
	GOEXPERIMENT=goroutineleakprofile go test ./internal/runtime

# Iteration-based (Nx) rather than time-based: a duration makes Go's fuzz
# coordinator set a context deadline whose teardown races, intermittently
# failing the nightly with "context deadline exceeded" (golang/go#48591).
#
# scope selects which targets run: 'all' (default) fuzzes both the ./cmd/vibes
# tooling targets and the ./internal/runtime targets; 'runtime' fuzzes only the
# runtime targets. Use 'runtime' when running under VIBES_ESTIMATOR_VERIFY /
# VIBES_ENV_RECYCLE_VERIFY: those toggles are read only by the runtime package's
# test TestMain, so the ./cmd/vibes targets (a separate test binary) would run
# with the oracle off — duplicate coverage rather than verification.
fuzz fuzztime='25000x' scope='all':
	#!/usr/bin/env bash
	set -uo pipefail

	fuzztime="{{fuzztime}}"
	scope="{{scope}}"
	failed=()

	run_target() {
		local pkg="$1" target="$2"
		if ! go test "$pkg" -run=^$ -fuzz="$target" -fuzztime="$fuzztime"; then
			failed+=("$target")
		fi
	}

	if [ "$scope" = "all" ]; then
		for target in \
			FuzzFormatVibeSource \
			FuzzCLIArgumentAndPathInputs \
			FuzzREPLInputFlow \
			FuzzLSPPayloadAndMessageHandling
		do
			run_target ./cmd/vibes "$target"
		done
	fi

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
		run_target ./internal/runtime "$target"
	done

	if [ "${#failed[@]}" -gt 0 ]; then
		echo "fuzz targets with new failing inputs: ${failed[*]}" >&2
		exit 1
	fi

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
