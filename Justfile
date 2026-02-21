set shell := ["bash", "-lc"]

test:
	go test ./...

bench:
	scripts/bench_runtime.sh

bench-profile pattern='^BenchmarkExecutionArrayPipeline$':
	scripts/bench_profile.sh --pattern "{{pattern}}"

lint:
	gofmt -l . | (! read)
	golangci-lint run --timeout=10m

repl:
	go build -o vibes-cli ./cmd/vibes && ./vibes-cli repl
