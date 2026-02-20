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

## Automated release checklist

Before tagging, run the checklist automation:

```bash
./scripts/release_checklist.sh v0.1.0
```

The checklist verifies:

- `CHANGELOG.md` contains a `## vX.Y.Z` heading.
- `ROADMAP.md` contains a matching milestone heading.
- The REPL version label in `cmd/vibes/repl.go` matches the target version.
- The version tag does not already exist locally.

The same validation runs automatically on tag pushes via
`.github/workflows/release-checklist.yml` and can also be run manually via
GitHub Actions `workflow_dispatch`.

### Local dry run (optional)

If you want to test locally instead of waiting for CI:

```bash
goreleaser release --clean --skip=publish
```
