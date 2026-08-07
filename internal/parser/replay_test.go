package parser

import (
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

// walkToPosition and walkToOffset are the definition lineIndex has to match:
// the straight walk over the input that resolving a position used to do.
func walkToPosition(input string, pos ast.Position) (int, bool) {
	line, column := 1, 1
	for idx, r := range input {
		if line == pos.Line && column == pos.Column {
			return idx, true
		}
		if r == '\n' {
			line++
			column = 1
		} else {
			column++
		}
	}
	if line == pos.Line && column == pos.Column {
		return len(input), true
	}
	return 0, false
}

func walkToOffset(input string, offset int) ast.Position {
	line, column := 1, 1
	for idx, r := range input {
		if idx >= offset {
			return ast.Position{Line: line, Column: column}
		}
		if r == '\n' {
			line++
			column = 1
		} else {
			column++
		}
	}
	return ast.Position{Line: line, Column: column}
}

// Indexing the source is only worth anything if it answers exactly what the
// walk answered, down to the positions that do not exist: a position the index
// resolved where the walk did not would send the lexer to a byte the parser
// never meant, so the two are compared over every position and offset of
// sources chosen for the boundaries -- empty input, a trailing newline (which
// opens a final empty line), blank lines, and multi-byte runes both before and
// after the column being asked about.
func TestLineIndexMatchesAStraightWalk(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"",
		"\n",
		"f / 2",
		"f / 2\n",
		"a\n\nbb\nccc\n",
		"x = \"é\"; f /a/\n",
		"héllo /wörld/\n\nfin",
		"emoji 🙂 mid\nsecond ✓ line\n",
		"trailing\r\nwindows\r\n",
	} {
		t.Run(strings.ReplaceAll(input, "\n", "\\n"), func(t *testing.T) {
			t.Parallel()

			index := &lineIndex{input: input}
			for line := range strings.Count(input, "\n") + 3 {
				for column := range len(input) + 3 {
					pos := ast.Position{Line: line, Column: column}
					wantOffset, wantOK := walkToPosition(input, pos)
					gotOffset, gotOK := index.offsetForPosition(pos)
					if gotOffset != wantOffset || gotOK != wantOK {
						t.Fatalf("offsetForPosition(%+v) = %d, %v; the walk says %d, %v",
							pos, gotOffset, gotOK, wantOffset, wantOK)
					}
				}
			}

			for offset := range len(input) + 2 {
				want := walkToOffset(input, offset)
				gotOffset, got := index.runeAt(offset)
				if got != want {
					t.Fatalf("runeAt(%d) reports position %+v; the walk says %+v", offset, got, want)
				}
				// The offset reported alongside is the start of the rune that
				// position names, so seeking to it lands the lexer on a rune
				// boundary even when the caller pointed into the middle of one.
				if boundary, ok := index.offsetForPosition(got); !ok || boundary != gotOffset {
					t.Fatalf("runeAt(%d) reports offset %d for position %+v, which resolves to %d, %v",
						offset, gotOffset, got, boundary, ok)
				}
			}
		})
	}
}
