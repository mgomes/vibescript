set shell := ["bash", "-lc"]

test:
	GOCACHE="${PWD}/.gocache" go test ./...
