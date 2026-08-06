package parser

import (
	"fmt"
	"strings"
	"testing"
)

// deadPercentCandidates builds one string interpolation holding n percent-array
// candidates that can never close: the "[" delimiters only ever nest deeper, so
// each candidate is a scan that walks to the end of the input and reports
// nothing, after which the lexer moves on a single byte to the next one.
func deadPercentCandidates(n int) string {
	var sb strings.Builder
	sb.WriteString(`x = "#{a`)
	for range n {
		sb.WriteString(" %w[b")
	}
	sb.WriteString(`}"`)
	return sb.String()
}

// The scanning a source provokes has to grow with the source, not with its
// square. Without percentScanBudget every dead candidate re-scans the whole
// remaining input while the caller advances one byte, and each such scan
// re-enters findStringInterpolationEnd for every "#{" it crosses, so a source
// that merely repeats " %w[" turns a bounded source size into unbounded parse
// work before any runtime quota can apply.
//
// This counts the input bytes the scans walk rather than timing them: elapsed
// time would fold in scheduling, GC, and the race and coverage instrumentation
// this repository runs across three operating systems, and the clock is too
// coarse on Windows to compare runs this short. It does not call t.Parallel
// because percentScanCounting and percentScanBytes are process-wide.
func TestPercentScanStaysLinearInDeadCandidates(t *testing.T) {
	measure := func(n int) uint64 {
		src := deadPercentCandidates(n)
		percentScanBytes.Store(0)
		percentScanCounting.Store(true)
		defer percentScanCounting.Store(false)
		if _, errs := Parse(src); len(errs) == 0 {
			t.Fatalf("n = %d: the source parsed cleanly, so it no longer exercises the failing scan", n)
		}
		return percentScanBytes.Load()
	}

	small, large := measure(1000), measure(2000)

	// Measured 24,955 then 49,955 bytes -- 2.00x for a doubled source. Before
	// the budget the same pair walked 2,503,500 and 10,007,000 bytes, a 4.00x
	// step. The assertion allows up to 3x so it states the complexity rather
	// than pinning counts that ordinary scanner changes would shift.
	if large > small*3 {
		t.Fatalf("doubling the source walked %d scan bytes against %d -- over 3x, so the speculative percent scan"+
			" is superlinear in the source size again", large, small)
	}
}

// The allowance has to be an allowance, not a cap on any one literal: a valid
// percent-array argument is productive work its caller consumes, so it is never
// charged and stays parseable however long it is and however many of them a
// source holds.
func TestPercentScanBudgetLeavesValidLiteralsAlone(t *testing.T) {
	t.Parallel()

	words := make([]string, 4000)
	for i := range words {
		words[i] = fmt.Sprintf("w%d", i)
	}
	long := strings.Join(words, " ")

	for _, tc := range []struct {
		name string
		src  string
	}{
		{"parenless argument", "x = puts %w[" + long + "]\n"},
		{"inside an interpolation", `x = "#{puts %w[` + long + `]}"` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			program, errs := Parse(tc.src)
			if len(errs) > 0 {
				t.Fatalf("Parse() returned %v", errs[0])
			}
			if program == nil {
				t.Fatal("Parse() returned no program")
			}
		})
	}
}

// Spending the allowance is a real behavior change and not merely a slowdown:
// the scan declines input it would otherwise have recognized, leaving "%" as
// modulo. Only a source that has already burned four times its own length on
// candidates that led nowhere gets there, but the boundary is worth pinning so
// a later change to the accounting cannot move it silently.
func TestPercentScanDeclinesOnceTheBudgetIsSpent(t *testing.T) {
	t.Parallel()

	const input = "%w[a b]"

	fresh := newPercentScanBudget(len(input))
	if _, entries, _, ok := scanPercentArrayLiteralAt(input, 0, fresh); !ok || len(entries) != 2 {
		t.Fatalf("with an unspent budget: ok = %v, entries = %v; want true and two entries", ok, entries)
	}
	if fresh.remaining != len(input)*percentScanBudgetFactor {
		t.Fatalf("a scan that found its literal charged %d bytes; want it to cost nothing",
			len(input)*percentScanBudgetFactor-fresh.remaining)
	}

	spent := &percentScanBudget{}
	if _, _, _, ok := scanPercentArrayLiteralAt(input, 0, spent); ok {
		t.Fatal("with a spent budget: ok = true; want the scan to decline and leave % as modulo")
	}
}
