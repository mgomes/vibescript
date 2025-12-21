set shell := ["bash", "-lc"]

test:
	go test ./...

lint:
	gofmt -l . | (! read)
	golangci-lint run --timeout=10m

repl:
	go build -o vibes-cli ./cmd/vibes && ./vibes-cli repl
