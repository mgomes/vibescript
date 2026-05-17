// Package source contains stable source-location types shared between
// the AST (internal) and the public error surface.
package source

// Position represents a source-code position. Line and Column are
// 1-indexed; Offset is 0-indexed and may be zero if the producer does
// not track byte offsets.
type Position struct {
	Line   int
	Column int
	Offset int
}
