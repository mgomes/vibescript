# Go Compatibility Matrix

VibeScript currently targets the Go toolchain version declared in `go.mod`.

| Go Version | Status | CI Coverage | Notes |
| --- | --- | --- | --- |
| `1.26.x` | Supported | `ubuntu-latest`, `macos-latest`, `windows-latest` | Primary development and release target. |
| `<1.26` | Unsupported | None | May fail to build or run correctly. |
| `>1.26` | Best effort | None | Expected to work, but not yet part of the pinned CI matrix. |

## Policy Notes

- The minimum supported Go version is the `go` directive in `go.mod`.
- Before `v1.0`, minimum-version bumps may happen in minor releases.
- Compatibility changes are tracked in `ROADMAP.md` and release notes.

## Verification

- CI matrix is defined in `.github/workflows/test.yml` for
  `ubuntu-latest`, `macos-latest`, and `windows-latest`.
- Latest `master` CI status can be checked with `./scripts/check_ci_green.sh`.
