package parser

import (
	"strings"
	"testing"
)

// PoC for audit finding `parser-type-expr-unbounded-recursion-dos` (sev3):
// array<array<...>> type annotations drive parseTypeExpr<->parseTypeAtom mutual
// recursion with no depth guard; deep enough nesting overflows the goroutine
// stack -> unrecoverable fatal -> host crash at parse time. Secure behavior: a
// bounded parse error, not a process abort.

func nestedArrayType(depth int) string {
	var b strings.Builder
	b.WriteString("def f(x : ")
	b.WriteString(strings.Repeat("array<", depth))
	b.WriteString("int")
	b.WriteString(strings.Repeat(">", depth))
	b.WriteString(") end")
	return b.String()
}

// Baseline: modest depth parses without crashing (sanity that the syntax is right).
func TestAuditTypeDepthBaseline(t *testing.T) {
	_, errs := Parse(nestedArrayType(50))
	t.Logf("depth=50 parsed, errors=%d", len(errs))
}

// The actual assertion: a deeply nested type must NOT crash the host. If the
// parser has no depth cap this will fatal-error (uncatchable) and fail the run;
// if it returns a bounded parse error, the test passes (documenting the fix).
func TestAuditTypeDepthNoHostCrash(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic from Parse (finding) : %v [parser-type-expr-unbounded-recursion-dos]", r)
		}
	}()
	_, errs := Parse(nestedArrayType(100000))
	if len(errs) == 0 {
		t.Errorf("depth=100000: parser returned no error (no depth cap) " +
			"[parser-type-expr-unbounded-recursion-dos]")
	} else {
		t.Logf("depth=100000: bounded with %d parse error(s) -- safe", len(errs))
	}
}
