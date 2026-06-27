package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestParserSnapshotRestoreTernaryStack verifies that snapshot/restore deep-copy
// the lexer's pending-ternary stack rather than sharing its backing array. A
// speculative parse may pop a pending ternary and then push a different one
// within the snapshot's original length; if the snapshot shared the backing
// array, that push would overwrite a retained entry and corrupt the restored
// stack. The round-trip must leave the stack exactly as it was at snapshot time.
func TestParserSnapshotRestoreTernaryStack(t *testing.T) {
	t.Parallel()

	p := newParser("")
	want := []ternaryFrame{
		{bracketDepth: 0},
		{bracketDepth: 1, parenlessKeywordCall: true},
	}
	p.l.ternaryStack = append([]ternaryFrame(nil), want...)
	p.l.bracketDepth = 1

	saved := p.snapshot()

	// Simulate a speculative parse that pops the innermost pending ternary and
	// then pushes a new one at a different nesting level. With a shared backing
	// array this would clobber the entry the snapshot still expects.
	p.l.ternaryStack = p.l.ternaryStack[:len(p.l.ternaryStack)-1]
	p.l.ternaryStack = append(p.l.ternaryStack, ternaryFrame{bracketDepth: 7})
	p.l.bracketDepth = 3

	p.restore(saved)

	if diff := cmp.Diff(want, p.l.ternaryStack, cmp.AllowUnexported(ternaryFrame{})); diff != "" {
		t.Fatalf("restored ternaryStack mismatch (-want +got):\n%s", diff)
	}
	if p.l.bracketDepth != 1 {
		t.Fatalf("restored bracketDepth = %d, want 1", p.l.bracketDepth)
	}

	// A push after restore must not reach back into the snapshot's retained
	// copy, so the snapshot stays valid for a second restore.
	p.l.ternaryStack = append(p.l.ternaryStack, ternaryFrame{bracketDepth: 9})
	if diff := cmp.Diff(want, saved.lexer.ternaryStack, cmp.AllowUnexported(ternaryFrame{})); diff != "" {
		t.Fatalf("snapshot ternaryStack mutated by post-restore push (-want +got):\n%s", diff)
	}
}
