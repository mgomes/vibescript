package parser

import (
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

// The work a source provokes outside the lexer's single forward pass has to
// grow with the source, not with its square. Both shapes below reach that work
// through the same door: "/" after a callee that is not a declared local may
// open a command-argument regex, so the parser resolves the slash's position to
// tell the regex from division, and the regex reading then repositions the
// lexer at the slash to re-read it. Resolving a position walked the input from
// byte 0 and repositioning re-lexed it from byte 0, so 20,000 `f / 2` lines
// took 1.26s and 5,000 `f /a/` lines 3.2s, all of it during compile, before any
// script step quota can meter it (#21).
//
// This counts the input bytes those re-walks cover rather than timing them:
// elapsed time would fold in scheduling, GC, and the race and coverage
// instrumentation this repository runs across three operating systems, and the
// clock is too coarse on Windows to compare runs this short. It does not call
// t.Parallel because sourceReplayCounting and sourceReplayBytes are
// process-wide.
func TestParenlessSlashReplayStaysLinear(t *testing.T) {
	measure := func(t *testing.T, src string) uint64 {
		t.Helper()

		sourceReplayBytes.Store(0)
		sourceReplayCounting.Store(true)
		defer sourceReplayCounting.Store(false)
		if _, errs := Parse(src); len(errs) != 0 {
			t.Fatalf("the source no longer parses cleanly, so it no longer exercises the walk: %v", errs[0])
		}
		return sourceReplayBytes.Load()
	}

	for _, tc := range []struct {
		name string
		line string
	}{
		{"division", "f / 2\n"},
		{"regex argument", "f /a/\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			small := measure(t, strings.Repeat(tc.line, 1000))
			large := measure(t, strings.Repeat(tc.line, 2000))

			// Measured 6,000 then 12,000 bytes for the divisions and 11,996
			// then 23,996 for the regex arguments: a 2.00x step for a doubled
			// source either way. Before, the same pairs walked 3.0M and 12.0M
			// bytes, and 12.0M and 48.0M, a 4.00x step both times. The
			// assertion allows up to 3x so it states the complexity rather
			// than pinning counts that ordinary lexer changes would shift.
			if large > small*3 {
				t.Fatalf("doubling the source re-walked %d bytes against %d -- over 3x, so telling a"+
					" command-argument regex from division is superlinear in the source size again", large, small)
			}
		})
	}
}

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
