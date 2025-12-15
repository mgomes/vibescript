# Releasing

This repository uses GoReleaser for changelog generation and GitHub releases. The project ships as a Go module only—no binaries or archives are built.

## Prerequisites

- GoReleaser installed locally.
- `GITHUB_TOKEN` with `repo` scope exported in your shell.
- A tagged version on the current commit (e.g., `git tag -a v0.1.0 -m "v0.1.0"` and push it).
- Tests passing (`just test` or `go test ./...`).

## Publish a release

1. Tag the release locally and push it (e.g., `git tag -a v0.1.0 -m "v0.1.0"` then `git push origin v0.1.0`).
2. GitHub Actions picks up `v*` tags via `.github/workflows/release.yml` and runs `goreleaser release --clean` with `GITHUB_TOKEN` injected.

### Local dry run (optional)

If you want to test locally instead of waiting for CI:

```bash
goreleaser release --clean --skip=publish
```
