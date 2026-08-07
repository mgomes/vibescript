package parser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

// packedTernaryLabels and spacedTernaryLabels build the two shapes that reach
// the speculative label-colon scan: keyword-call labels written against their
// value (k0:0) and labels whose colon is spaced off the name and opens a quoted
// value (a0 :"x"). Both sit in the consequent of a ternary whose separator is
// the last colon on the line, so every label colon has to look ahead past every
// later one to find it.
func packedTernaryLabels(n int) string {
	var sb strings.Builder
	sb.WriteString("flag ? emit ")
	for i := range n {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "k%d:%d", i, i)
	}
	sb.WriteString(` :"x"`)
	return sb.String()
}

func spacedTernaryLabels(n int) string {
	var sb strings.Builder
	sb.WriteString("flag ? emit ")
	for i := range n {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, `a%d :"x"`, i)
	}
	sb.WriteString(` :"no"`)
	return sb.String()
}

// Adding one more label to a line must not double the scanning the line
// provokes. The scan that decides whether a label colon precedes the ternary
// separator re-runs the whole colon decision over the rest of the source, and
// that decision starts the same scan again, so a scan launched at one label
// colon launched another at every later label colon. 22 spaced labels, 226
// bytes in all, took 877ms to lex and 26 labels took over a minute, all of it
// during compile where no step or memory quota reaches it (#31, #32).
//
// This counts the input bytes those scans walk rather than timing them:
// elapsed time would fold in scheduling, GC, and the race and coverage
// instrumentation this repository runs across three operating systems, and the
// clock is too coarse on Windows to compare runs this short. It does not call
// t.Parallel because ternaryScanCounting and ternaryScanBytes are process-wide.
func TestTernaryLabelScanDoesNotCompoundWithLabelCount(t *testing.T) {
	measure := func(t *testing.T, src string) uint64 {
		t.Helper()

		ternaryScanBytes.Store(0)
		ternaryScanCounting.Store(true)
		defer ternaryScanCounting.Store(false)
		if _, errs := Parse(src); len(errs) != 0 {
			t.Fatalf("the source no longer parses cleanly, so it no longer exercises the scan: %v", errs[0])
		}
		return ternaryScanBytes.Load()
	}

	for _, tc := range []struct {
		name string
		gen  func(int) string
	}{
		{"packed labels", packedTernaryLabels},
		{"spaced labels", spacedTernaryLabels},
	} {
		t.Run(tc.name, func(t *testing.T) {
			small := measure(t, tc.gen(12))
			large := measure(t, tc.gen(24))

			// Measured 488 then 2,228 bytes for the packed labels and 687 then
			// 2,859 for the spaced ones, so doubling the labels roughly
			// quadruples the walk. Before, the same pairs walked 50,090 then
			// 218,100,554 bytes, and 64,387 then 268,434,187: over 4,000x for
			// twice the labels. The assertion allows up to 8x so it states that
			// the walk no longer compounds rather than pinning counts that
			// ordinary lexer changes would shift.
			if large > small*8 {
				t.Fatalf("doubling the labels walked %d scan bytes against %d -- over 8x, so deciding whether a"+
					" label colon precedes the ternary separator compounds with the label count again", large, small)
			}
		})
	}
}

// Answering the same colon from a record instead of re-scanning has to give the
// reading the scan gave: every label colon stays a label and only the last
// colon on the line separates the ternary, which is what keeps the value after
// it a string rather than a quoted symbol.
func TestTernaryLabelScanKeepsTheLastColonTheSeparator(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		src  string
		want []ast.TokenType
	}{
		{
			name: "packed labels",
			src:  `flag ? emit k0:0, k1:1 :"x"`,
			want: []ast.TokenType{
				ast.TokenIdent, ast.TokenQuestion, ast.TokenIdent,
				ast.TokenIdent, ast.TokenColon, ast.TokenInt, ast.TokenComma,
				ast.TokenIdent, ast.TokenColon, ast.TokenInt,
				ast.TokenColon, ast.TokenString,
			},
		},
		{
			name: "spaced labels",
			src:  `flag ? emit a0 :"x", a1 :"x" :"no"`,
			want: []ast.TokenType{
				ast.TokenIdent, ast.TokenQuestion, ast.TokenIdent,
				ast.TokenIdent, ast.TokenColon, ast.TokenString, ast.TokenComma,
				ast.TokenIdent, ast.TokenColon, ast.TokenString,
				ast.TokenColon, ast.TokenString,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lexer := newLexer(tc.src)
			var got []ast.TokenType
			for {
				tok := lexer.NextToken()
				if tok.Type == ast.TokenEOF {
					break
				}
				got = append(got, tok.Type)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("lexed %v, want %v", got, tc.want)
			}
			for i, want := range tc.want {
				if got[i] != want {
					t.Fatalf("token %d is %v, want %v (whole stream %v)", i, got[i], want, got)
				}
			}
		})
	}
}
