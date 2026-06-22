# Changelog fragments

Unreleased changelog entries live here as individual files so that pull
requests never edit `CHANGELOG.md` directly and therefore never conflict with
each other on it.

## Adding an entry

Create one file per change, named `<issue-or-pr>-<short-slug>.md`, for example
`501-array-transpose.md`. The file's contents are the Markdown bullet(s) exactly
as they should appear under `## Unreleased`, e.g.:

```markdown
- **Added: Ruby-style `Array#transpose`.** `transpose` swaps the rows and
  columns of a matrix of equal-length rows.
```

Use a top-level `- ` bullet; continuation lines are indented two spaces. A
single file may contain more than one bullet when a change warrants it.

## Releasing

`scripts/build_changelog.sh <version>` moves every fragment here into a new
`## <version> - <date>` section below the `## Unreleased` section of `CHANGELOG.md`, then deletes the
fragments. Fragments are emitted in sorted-filename order. This file
(`README.md`) is ignored.
