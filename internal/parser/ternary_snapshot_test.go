package parser

import (
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mgomes/vibescript/internal/ast"
)

// frameStackOf builds a stack holding frames, bottom first.
func frameStackOf[T any](frames []T) *frameStack[T] {
	var stack *frameStack[T]
	for _, frame := range frames {
		stack = stack.push(frame)
	}
	return stack
}

// framesOf returns the frames a stack holds, bottom first.
func framesOf[T any](stack *frameStack[T]) []T {
	frames := make([]T, stack.len())
	for i := stack.len() - 1; i >= 0; i-- {
		frames[i], _ = stack.top()
		stack = stack.pop()
	}
	return frames
}

// TestParserSnapshotRestoreLexerStacks verifies that a snapshot keeps naming the
// lexer stacks as they stood when it was taken. A speculative parse may pop an
// entry and push a different one, and the parse that resumes after a restore
// pushes further entries; neither may reach the frames the snapshot retains,
// which is what lets the same snapshot be restored more than once.
func TestParserSnapshotRestoreLexerStacks(t *testing.T) {
	t.Parallel()

	p := newParser("")
	wantTernaries := []ternaryFrame{
		{bracketDepth: 0},
		{bracketDepth: 1, parenlessKeywordCall: true},
	}
	wantBrackets := []bracketFrame{
		{token: ast.TokenLParen},
		{token: ast.TokenLBrace},
	}
	p.l.ternaryStack = frameStackOf(wantTernaries)
	p.l.bracketStack = frameStackOf(wantBrackets)
	p.l.bracketDepth = 2

	saved := p.snapshot()

	// Simulate speculative parsing that rewrites the top of both stacks.
	p.l.ternaryStack = p.l.ternaryStack.pop().push(ternaryFrame{bracketDepth: 7})
	p.l.bracketStack = p.l.bracketStack.pop().push(bracketFrame{token: ast.TokenLBracket})
	p.l.bracketDepth = 3

	p.restore(saved)

	if diff := cmp.Diff(wantTernaries, framesOf(p.l.ternaryStack), cmp.AllowUnexported(ternaryFrame{})); diff != "" {
		t.Fatalf("restored ternaryStack mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantBrackets, framesOf(p.l.bracketStack), cmp.AllowUnexported(bracketFrame{})); diff != "" {
		t.Fatalf("restored bracketStack mismatch (-want +got):\n%s", diff)
	}
	if p.l.bracketDepth != 2 {
		t.Fatalf("restored bracketDepth = %d, want 2", p.l.bracketDepth)
	}

	// Pushes after restore must not reach back into the frames the snapshot
	// retains, so the snapshot stays valid for a second restore.
	p.l.ternaryStack = p.l.ternaryStack.push(ternaryFrame{bracketDepth: 9})
	p.l.bracketStack = p.l.bracketStack.push(bracketFrame{token: ast.TokenLBracket})
	if diff := cmp.Diff(wantTernaries, framesOf(saved.lexer.ternaryStack), cmp.AllowUnexported(ternaryFrame{})); diff != "" {
		t.Fatalf("snapshot ternaryStack mutated by post-restore push (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantBrackets, framesOf(saved.lexer.bracketStack), cmp.AllowUnexported(bracketFrame{})); diff != "" {
		t.Fatalf("snapshot bracketStack mutated by post-restore push (-want +got):\n%s", diff)
	}
}

// TestFrameStackImmutable pins the stack operations the snapshot path relies on:
// every one of them leaves the stack it was called on holding what it held.
func TestFrameStackImmutable(t *testing.T) {
	t.Parallel()

	base := frameStackOf([]ternaryFrame{{bracketDepth: 1}, {bracketDepth: 2}})
	want := []ternaryFrame{{bracketDepth: 1}, {bracketDepth: 2}}

	pushed := base.push(ternaryFrame{bracketDepth: 3})
	popped := base.pop()
	replaced := base.replaceTop(ternaryFrame{bracketDepth: 9})

	if diff := cmp.Diff(want, framesOf(base), cmp.AllowUnexported(ternaryFrame{})); diff != "" {
		t.Fatalf("base mutated (-want +got):\n%s", diff)
	}
	if got, want := pushed.len(), 3; got != want {
		t.Fatalf("pushed.len() = %d, want %d", got, want)
	}
	if diff := cmp.Diff([]ternaryFrame{{bracketDepth: 1}}, framesOf(popped), cmp.AllowUnexported(ternaryFrame{})); diff != "" {
		t.Fatalf("pop mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]ternaryFrame{{bracketDepth: 1}, {bracketDepth: 9}}, framesOf(replaced), cmp.AllowUnexported(ternaryFrame{})); diff != "" {
		t.Fatalf("replaceTop mismatch (-want +got):\n%s", diff)
	}

	var empty *frameStack[ternaryFrame]
	if _, ok := empty.top(); ok {
		t.Fatal("empty stack reported a top frame")
	}
	if empty.pop() != nil || empty.replaceTop(ternaryFrame{}) != nil || empty.len() != 0 {
		t.Fatal("empty stack did not stay empty")
	}
}

// pendingFramesThenBracedParams builds a source that leaves lexer frames
// pending and then makes the parser speculate over and over. Nothing balances
// an opener: a `?` waits for a ternary separator that never arrives and a `(`
// for a `)`, and neither is dropped at a statement boundary or after a parse
// error. Every `a:{b:c}` parameter is then an ambiguous braced annotation,
// which bracedGroupIsShapeType settles by snapshotting the parser, speculating,
// and restoring.
func pendingFramesThenBracedParams(opener string, openers, params int) string {
	var sb strings.Builder
	for range openers {
		sb.WriteString(opener)
	}
	sb.WriteString("\ndef f(")
	for i := range params {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("a:{b:c}")
	}
	sb.WriteString(")\nend\n")
	return sb.String()
}

// Frames the source left pending must cost the speculations that follow them
// nothing. snapshot and restore copied the lexer's bracket and pending-ternary
// stacks, so each of them cost the whole stack: 50,000 stray `?` in front of
// 10,000 ambiguous braced parameters, 180 KB of source, took 3.9s and allocated
// 15 GB, and 40,000 stray `(` in front of 8,000 of them took 2.5s. All of it
// runs during compile, where no step or memory quota reaches it (#34).
//
// This compares what one parameter list allocates with and without the pending
// frames in front of it rather than timing either: allocated bytes are an exact
// running total, while elapsed time would fold in scheduling, GC, and the race
// and coverage instrumentation this repository runs across three operating
// systems. It does not call t.Parallel because the total counts every
// goroutine's allocations.
func TestPendingLexerFramesDoNotChargeLaterSpeculation(t *testing.T) {
	measure := func(t *testing.T, src string, wantErrors bool) uint64 {
		t.Helper()

		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		_, errs := Parse(src)
		runtime.ReadMemStats(&after)
		if wantErrors && len(errs) == 0 {
			t.Fatal("the source now parses cleanly, so the openers are no longer left pending")
		}
		if !wantErrors && len(errs) != 0 {
			t.Fatalf("the parameter list alone no longer parses, so the two measurements are not comparable: %v", errs[0])
		}
		return after.TotalAlloc - before.TotalAlloc
	}

	const params = 4000
	for _, tc := range []struct {
		name    string
		opener  string
		openers int
	}{
		{"pending ternaries", "?\n", 20000},
		{"unclosed parens", "(", 20000},
		{"unclosed brackets", "[", 20000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			alone := measure(t, pendingFramesThenBracedParams(tc.opener, 0, params), false)
			pending := measure(t, pendingFramesThenBracedParams(tc.opener, tc.openers, params), true)

			// Measured 5.6 MB for the parameters alone and 7.4 MB with 20,000
			// pending ternaries in front of them, so the openers cost about
			// what their own source costs. Before, the same pair allocated
			// 5.7 MB and 2,630 MB: 463x for a prefix that is half the source.
			// The assertion allows 4x so it states that the pending frames no
			// longer multiply the speculation rather than pinning byte counts
			// that ordinary parser changes would shift.
			if pending > alone*4 {
				t.Fatalf("%d pending frames took the parse from %d to %d allocated bytes -- over 4x, so every"+
					" speculation pays for the whole lexer stack again", tc.openers, alone, pending)
			}
		})
	}
}
