// Package source contains stable source-location types shared between
// the AST (internal) and the public error surface.
package source

// Position represents a source-code position. Line and Column are
// 1-indexed. The layout is intentionally preserved across the move
// from vibes.Position so positional struct literals in embedder code
// keep compiling.
type Position struct {
	Line   int
	Column int
}
