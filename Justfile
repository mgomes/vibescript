set shell := ["bash", "-lc"]

test:
	go test ./...

bench:
	go test ./vibes -run '^$' -bench '^BenchmarkExecution' -benchmem

lint:
	gofmt -l . | (! read)
	golangci-lint run --timeout=10m

repl:
	go build -o vibes-cli ./cmd/vibes && ./vibes-cli repl
