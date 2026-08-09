package parser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
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

// modulusOnLocalSource is a source of nothing but ordinary modulo on declared
// locals. Each `a %w<d> b` looks exactly like the opening of a percent literal
// whose delimiter never closes, because every delimiter is distinct and occurs
// nowhere later in the file. None of them is a candidate -- the callee is a
// declared local, which suppresses the percent-array reading outright -- so
// none may charge the allowance, or the `puts %w[p q]` at the end would be
// declined for want of allowance and read as `puts % w[p, q]` instead.
func modulusOnLocalSource() string {
	var sb strings.Builder
	sb.WriteString("a = 1\nw = 1\nb = 1\n")
	for i, delim := range []string{"+", "-", "*", "/", "&", "!", "?", ".", ">"} {
		fmt.Fprintf(&sb, "x%d = a %%w%s b\n", i, delim)
	}
	sb.WriteString("puts %w[p q]\n")
	return sb.String()
}

// A source that is valid throughout must never have a percent literal quietly
// re-read as modulo because earlier modulo expressions used up the allowance.
// The suppression that already governs this shape has to be consulted before
// the probe rather than after it, so an expression that was never going to be a
// literal costs nothing.
func TestValidModulusOnLocalsDoesNotSpendTheBudget(t *testing.T) {
	t.Parallel()

	src := modulusOnLocalSource()
	program, errs := Parse(src)
	if len(errs) > 0 {
		t.Fatalf("Parse() returned %v", errs[0])
	}

	last := program.Statements[len(program.Statements)-1]
	stmt, ok := last.(*ast.ExprStmt)
	if !ok {
		t.Fatalf("last statement is %T, want *ast.ExprStmt", last)
	}
	call, ok := stmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("`puts %%w[p q]` parsed as %T; a spent allowance re-read it as modulo", stmt.Expr)
	}
	if len(call.Args) != 1 {
		t.Fatalf("`puts %%w[p q]` parsed with %d arguments, want 1", len(call.Args))
	}
	if _, ok := call.Args[0].(*ast.ArrayLiteral); !ok {
		t.Fatalf("`puts %%w[p q]` passed a %T, want an *ast.ArrayLiteral", call.Args[0])
	}
}

// Where the allowance genuinely does run out the parser says so. It stops
// telling percent literals from modulo at that point, which changes what a
// later `%w[...]` means, and handing back a program that quietly means
// something else is worse than refusing it.
func TestPercentScanExhaustionIsReported(t *testing.T) {
	t.Parallel()

	// Method callees, which the declared-local suppression does not cover, so
	// every one of these does have to be probed and does charge.
	var sb strings.Builder
	sb.WriteString("def foo()\n  1\nend\ndef w()\n  1\nend\ndef b()\n  1\nend\n")
	for i, delim := range []string{"+", "-", "*", "/", "&", "!", "?", ".", ">"} {
		fmt.Fprintf(&sb, "y%d = foo %%w%s b\n", i, delim)
	}
	sb.WriteString(strings.Repeat("# padding padding padding padding padding padding\n", 50))
	sb.WriteString("puts %w[p q]\n")

	_, errs := Parse(sb.String())
	if len(errs) == 0 {
		t.Fatal("Parse() reported nothing; a source that outran the allowance must not come back silently")
	}
	if got := errs[0].Error(); !strings.Contains(got, "ambiguous") {
		t.Fatalf("Parse() reported %q, want the exhausted-allowance diagnostic", got)
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
	if _, entries, _, ok, _ := scanPercentArrayLiteralAt(input, 0, fresh, 0); !ok || len(entries) != 2 {
		t.Fatalf("with an unspent budget: ok = %v, entries = %v; want true and two entries", ok, entries)
	}
	if fresh.remaining != len(input)*percentScanBudgetFactor {
		t.Fatalf("a scan that found its literal charged %d bytes; want it to cost nothing",
			len(input)*percentScanBudgetFactor-fresh.remaining)
	}

	spent := &percentScanBudget{}
	if _, _, _, ok, _ := scanPercentArrayLiteralAt(input, 0, spent, 0); ok {
		t.Fatal("with a spent budget: ok = true; want the scan to decline and leave % as modulo")
	}
}
