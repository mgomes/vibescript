package runtime

import (
	"context"
	"testing"
)

// sort ordered symbols, nil, and booleans while <=> ordered none of them, so
// the operator misreported what the language can do: an author probing with
// <=> concluded symbols were unsortable, which was wrong.
//
// The fix follows Ruby per kind rather than simply widening everything, since
// Ruby's own coverage is uneven: Symbol includes Comparable, NilClass defines
// <=> but no relational operators, and TrueClass defines neither.
func TestSymbolsOrderUnderEveryComparison(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{`(:a <=> :b).inspect`, "-1"},
		{`(:b <=> :a).inspect`, "1"},
		{`(:a <=> :a).inspect`, "0"},
		// Symbol includes Comparable, so the relational operators work too.
		{`(:a < :b).to_s`, "true"},
		{`(:b > :a).to_s`, "true"},
		{`(:a <= :a).to_s`, "true"},
		{`(:b >= :a).to_s`, "true"},
		// Agreement with sort, which is the disagreement that made this worth
		// fixing.
		{`([:c, :a, :b].sort == [:a, :b, :c]).to_s`, "true"},
		{`((:a <=> :b) == -1).to_s`, "true"},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %s, want %s", tc.expr, got.String(), tc.want)
			}
		})
	}
}

// nil answers <=> against itself but has no relational operators, matching
// Ruby, where `nil <=> nil` is 0 and `nil < nil` raises.
func TestNilOrdersUnderSpaceshipOnly(t *testing.T) {
	t.Parallel()

	script := compileScript(t, "def run()\n  (nil <=> nil).inspect\nend")
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "0" {
		t.Fatalf("nil <=> nil = %s, want 0", got.String())
	}

	for _, op := range []string{"<", "<=", ">", ">="} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  nil "+op+" nil\nend")
			if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
				t.Fatalf("nil %s nil was accepted, want it rejected as in Ruby", op)
			}
		})
	}
}

// TrueClass defines neither <=> nor the relational operators in Ruby, so
// booleans keep answering nil and raising respectively -- even though sort
// accepts them here, which is a pre-existing deliberate divergence.
func TestBooleansDoNotOrderUnderComparison(t *testing.T) {
	t.Parallel()

	script := compileScript(t, "def run()\n  (true <=> false).inspect\nend")
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "nil" {
		t.Fatalf("true <=> false = %s, want nil", got.String())
	}

	for _, op := range []string{"<", ">"} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  true "+op+" false\nend")
			if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
				t.Fatalf("true %s false was accepted, want it rejected as in Ruby", op)
			}
		})
	}
}

// Mismatched kinds stay unordered rather than inventing an order across kinds.
func TestMismatchedKindsStayUnordered(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{`(:a <=> 1).inspect`, `(nil <=> 1).inspect`, `(:a <=> "a").inspect`, `(nil <=> false).inspect`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+expr+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", expr, err)
			}
			if got.String() != "nil" {
				t.Fatalf("%s = %s, want nil", expr, got.String())
			}
		})
	}
}
