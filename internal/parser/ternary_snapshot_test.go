package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mgomes/vibescript/internal/ast"
)

// TestParserSnapshotRestoreLexerStacks verifies that snapshot/restore deep-copy
// the lexer's mutable stack slices rather than sharing their backing arrays. A
// speculative parse may pop an entry and then push a different one within the
// snapshot's original length; if the snapshot shared the backing array, that push
// would overwrite a retained entry and corrupt the restored stack.
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
	p.l.ternaryStack = append([]ternaryFrame(nil), wantTernaries...)
	p.l.bracketStack = append([]bracketFrame(nil), wantBrackets...)
	p.l.bracketDepth = 2

	saved := p.snapshot()

	// Simulate speculative parsing that mutates both stacks. With shared backing
	// arrays, these pushes would clobber the entries the snapshot still expects.
	p.l.ternaryStack = p.l.ternaryStack[:len(p.l.ternaryStack)-1]
	p.l.ternaryStack = append(p.l.ternaryStack, ternaryFrame{bracketDepth: 7})
	p.l.bracketStack = p.l.bracketStack[:len(p.l.bracketStack)-1]
	p.l.bracketStack = append(p.l.bracketStack, bracketFrame{token: ast.TokenLBracket})
	p.l.bracketDepth = 3

	p.restore(saved)

	if diff := cmp.Diff(wantTernaries, p.l.ternaryStack, cmp.AllowUnexported(ternaryFrame{})); diff != "" {
		t.Fatalf("restored ternaryStack mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantBrackets, p.l.bracketStack, cmp.AllowUnexported(bracketFrame{})); diff != "" {
		t.Fatalf("restored bracketStack mismatch (-want +got):\n%s", diff)
	}
	if p.l.bracketDepth != 2 {
		t.Fatalf("restored bracketDepth = %d, want 2", p.l.bracketDepth)
	}

	// Pushes after restore must not reach back into the snapshot's retained
	// copies, so the snapshot stays valid for a second restore.
	p.l.ternaryStack = append(p.l.ternaryStack, ternaryFrame{bracketDepth: 9})
	p.l.bracketStack = append(p.l.bracketStack, bracketFrame{token: ast.TokenLBracket})
	if diff := cmp.Diff(wantTernaries, saved.lexer.ternaryStack, cmp.AllowUnexported(ternaryFrame{})); diff != "" {
		t.Fatalf("snapshot ternaryStack mutated by post-restore push (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantBrackets, saved.lexer.bracketStack, cmp.AllowUnexported(bracketFrame{})); diff != "" {
		t.Fatalf("snapshot bracketStack mutated by post-restore push (-want +got):\n%s", diff)
	}
}
