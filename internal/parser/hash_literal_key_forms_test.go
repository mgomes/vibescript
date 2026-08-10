package parser

import (
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

// The hash-literal build accumulator prices a key through hashDisplayKey, which
// returns the key's own string for a string or symbol and calls Inspect for
// anything else. Its replay re-prices every earlier key on every pair, so a key
// kind that renders would be re-rendered O(pairs squared) times, allocating a
// fresh string each time that the estimator's identity dedup cannot collapse.
// The accumulator is safe from that only because a literal key is always a
// label or a quoted label, and both evaluate to a string or symbol whose
// rendering is the string itself.
//
// This pins that premise. A computed-key form (`{expr => v}`, `{(expr): v}`)
// would make the replay's rendering reachable and has to be weighed against
// that walk before it lands.
func TestHashLiteralKeysAreAlwaysLabels(t *testing.T) {
	t.Parallel()

	program, errs := Parse("h = {name: 1, \"quoted\": 2, other: 3}")
	if len(errs) != 0 {
		t.Fatalf("parsing label keys failed: %v", errs[0])
	}

	if len(program.Statements) != 1 {
		t.Fatalf("parsed %d statements, want the single assignment", len(program.Statements))
	}
	assign, ok := program.Statements[0].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("statement is %T, want an assignment", program.Statements[0])
	}
	literal, ok := assign.Value.(*ast.HashLiteral)
	if !ok {
		t.Fatalf("assigned value is %T, want a hash literal", assign.Value)
	}
	if len(literal.Pairs) != 3 {
		t.Fatalf("literal has %d pairs, want 3", len(literal.Pairs))
	}
	for _, pair := range literal.Pairs {
		switch pair.Key.(type) {
		case *ast.SymbolLiteral, *ast.StringLiteral:
		default:
			t.Errorf("hash literal key is %T; the build accumulator's key pricing renders "+
				"anything that is not a string or symbol, once per earlier pair per pair",
				pair.Key)
		}
	}
}

// The forms that would carry a computed key are rejected outright, which is
// what keeps every literal key a label.
func TestHashLiteralRejectsComputedKeys(t *testing.T) {
	t.Parallel()

	for _, src := range []string{
		"h = {1 => 2}",
		"h = {(1): 2}",
		"h = {:sym => 1}",
		"h = {1: 2}",
		"k = [1, 2]\nh = {[k, 1] => 3}",
	} {
		if _, errs := Parse(src); len(errs) == 0 {
			t.Errorf("%q parsed; a computed hash-literal key would make the build accumulator's "+
				"replay rendering reachable", src)
		}
	}
}
