set shell := ["bash", "-lc"]

test:
	go test ./...

lint:
	gofmt -l . | (! read)
	golangci-lint run --timeout=5m --out-format colored-line-number
