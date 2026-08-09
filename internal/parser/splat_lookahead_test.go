package parser

import (
	"strings"
	"testing"
)

// multiplicationContinuationLines builds a body whose every line after the
// first begins with "*". Each of them asks whether it opens a destructuring
// assignment or continues the line before it as a multiplication, which is the
// question the lookahead answers.
func multiplicationContinuationLines(lines int) string {
	var sb strings.Builder
	sb.WriteString("def run\n  a\n")
	for range lines {
		sb.WriteString("  * a\n")
	}
	sb.WriteString("end\n")
	return sb.String()
}

// Asking whether a line-leading "*" opens a destructuring assignment must not
// re-read the source before it. The lookahead resolved the "*" position by
// counting lines from byte 0 and then rebuilt its lexer state by re-lexing from
// byte 0 as well, both because it opened a fresh source replay instead of
// sharing the one for the source being parsed. 8,000 such lines, 48 KB, took
// 10.5s and allocated 2.1 GB, all of it during compile where no step or memory
// quota reaches it (#38).
//
// This counts the input bytes the re-walks cover rather than timing them:
// elapsed time would fold in scheduling, GC, and the race and coverage
// instrumentation this repository runs across three operating systems. It does
// not call t.Parallel because sourceReplayCounting and sourceReplayBytes are
// process-wide.
func TestSplatLookaheadDoesNotRewalkTheSourceBeforeIt(t *testing.T) {
	measure := func(t *testing.T, src string) uint64 {
		t.Helper()

		sourceReplayBytes.Store(0)
		sourceReplayCounting.Store(true)
		defer sourceReplayCounting.Store(false)
		if _, errs := Parse(src); len(errs) != 0 {
			t.Fatalf("the source no longer parses cleanly, so it no longer exercises the lookahead: %v", errs[0])
		}
		return sourceReplayBytes.Load()
	}

	small := measure(t, multiplicationContinuationLines(500))
	large := measure(t, multiplicationContinuationLines(2000))

	// Measured 6,020 then 24,020 bytes, so four times the lines re-walk four
	// times the source. Before, the same pair re-walked 3,024,984 then
	// 48,099,984: sixteen times for four times the lines. The assertion allows
	// 8x so it states that the re-walk no longer compounds rather than pinning
	// counts that ordinary lexer changes would shift.
	if large > small*8 {
		t.Fatalf("four times the continuation lines re-walked %d source bytes against %d -- over 8x, so every"+
			" line-leading \"*\" reads the source before it again", large, small)
	}
}
