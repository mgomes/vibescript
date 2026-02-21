set shell := ["bash", "-lc"]

test:
	go test ./...

bench:
	scripts/bench_runtime.sh

lint:
	gofmt -l . | (! read)
	golangci-lint run --timeout=10m

repl:
	go build -o vibes-cli ./cmd/vibes && ./vibes-cli repl
