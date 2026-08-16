package runtime

import (
	"context"
	"strings"
	"testing"
)

// Arrays had no ordering at all, so an array of arrays could not be sorted.
// That mattered beyond arrays themselves: a hash entry is a pair, so
// hash.to_a.sort -- the documented route to an ordered hash -- failed.
func TestArraySpaceshipComparesLexicographically(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "first differing element decides", expr: `([1, 2] <=> [1, 3]).inspect`, want: "-1"},
		{name: "equal arrays", expr: `([1, 2] <=> [1, 2]).inspect`, want: "0"},
		{name: "longer with equal prefix sorts last", expr: `([1, 2] <=> [1]).inspect`, want: "1"},
		{name: "shorter with equal prefix sorts first", expr: `([1] <=> [1, 2]).inspect`, want: "-1"},
		{name: "empty arrays", expr: `([] <=> []).inspect`, want: "0"},
		{name: "empty sorts before non-empty", expr: `([] <=> [1]).inspect`, want: "-1"},
		{name: "nested arrays recurse", expr: `([[1, [2]]] <=> [[1, [3]]]).inspect`, want: "-1"},
		{name: "strings order", expr: `(["a", "b"] <=> ["a", "c"]).inspect`, want: "-1"},
		// Symbols and nil order as elements, which is what makes a pair from
		// a hash comparable.
		{name: "symbol elements order", expr: `([:a] <=> [:b]).inspect`, want: "-1"},
		{name: "nil elements are equal", expr: `([nil] <=> [nil]).inspect`, want: "0"},
		{name: "symbol headed pairs", expr: `([:north, 300] <=> [:south, 200]).inspect`, want: "-1"},
		// An element pair that cannot be ordered makes the whole comparison
		// nil, as in Ruby, rather than raising.
		{name: "incomparable element yields nil", expr: `([1, 2] <=> [1, "a"]).inspect`, want: "nil"},
		{name: "array against a non-array yields nil", expr: `([1, 2] <=> "x").inspect`, want: "nil"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
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

// The ordering members all route through the same comparator, so each one
// gains array ordering together.
func TestOrderingMembersAcceptArrays(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "sort", expr: `[[2, 1], [1, 9]].sort.inspect`, want: "[[1, 9], [2, 1]]"},
		{name: "sort is stable across keys", expr: `[[2, "b"], [1, "z"], [1, "a"]].sort.inspect`, want: `[[1, "a"], [1, "z"], [2, "b"]]`},
		{name: "min", expr: `[[1, 2], [1, 1]].min.inspect`, want: "[1, 1]"},
		{name: "max", expr: `[[1, 2], [1, 1]].max.inspect`, want: "[1, 2]"},
		{name: "minmax", expr: `[[1, 2], [1, 1]].minmax.inspect`, want: "[[1, 1], [1, 2]]"},
		// The motivating case: a hash ordered through to_a.
		{name: "hash entries sort", expr: `({north: 300, south: 200}).to_a.sort.inspect`, want: `[["north", 300], ["south", 200]]`},
		// A multi-key sort key is an array, which is the natural spelling.
		{name: "sort_by with an array key", expr: `[{d: "z", n: "a"}, {d: "a", n: "q"}].sort_by { |r| [r[:d], r[:n]] }.inspect`, want: `[{d: "a", n: "q"}, {d: "z", n: "a"}]`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
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

// sort must still refuse arrays it cannot order, rather than inventing one.
func TestSortRejectsIncomparableArrayElements(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      [[1, "a"], [1, 2]].sort
    end
    `)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected sort to reject arrays with incomparable elements")
	}
	if !strings.Contains(err.Error(), "not comparable") {
		t.Fatalf("error = %v, want it to report incomparable values", err)
	}
}

// Ruby's Array defines <=> but does not include Comparable, so the relational
// operators keep rejecting arrays. Adding <=> must not quietly enable them.
func TestRelationalOperatorsStillRejectArrays(t *testing.T) {
	t.Parallel()

	for _, op := range []string{"<", "<=", ">", ">="} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  [1, 2] "+op+" [1, 3]\nend")
			if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
				t.Fatalf("[1, 2] %s [1, 3] was accepted, want it rejected as in Ruby", op)
			}
		})
	}
}

// Scalar operand coverage follows Ruby per kind: symbols order, nil orders
// against itself, and booleans do not order even though sort accepts them.
func TestScalarSpaceshipCoverage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{`(:a <=> :b).inspect`, "-1"},
		{`(:b <=> :a).inspect`, "1"},
		{`(:a <=> :a).inspect`, "0"},
		{`(nil <=> nil).inspect`, "0"},
		// TrueClass defines no <=> in Ruby.
		{`(true <=> false).inspect`, "nil"},
		// Mismatched kinds stay unordered.
		{`(:a <=> 1).inspect`, "nil"},
		{`(nil <=> 1).inspect`, "nil"},
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

// A self-referential array must terminate. Ruby answers 0 for a pair of
// distinct cyclic arrays rather than exhausting the stack, and so does this.
func TestCyclicArrayComparisonTerminates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "array against itself", body: "a = [1]\n  a.push(a)\n  (a <=> a).inspect", want: "0"},
		{name: "two distinct cyclic arrays", body: "a = [1]\n  a.push(a)\n  b = [1]\n  b.push(b)\n  (a <=> b).inspect", want: "0"},
		{name: "cyclic sorts without hanging", body: "a = [1]\n  a.push(a)\n  b = [1]\n  b.push(b)\n  [a, b].sort.length.to_s", want: "2"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.body+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got.String(), tc.want)
			}
		})
	}
}

// Two sibling subtrees may legitimately compare the same pair of arrays, so
// the cycle set must be scoped to the current comparison stack rather than
// marking a pair as seen for the whole traversal.
func TestRepeatedArrayPairAcrossSiblingsStillCompares(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      inner = [1, 2]
      left = [inner, inner, 1]
      right = [inner, inner, 2]
      (left <=> right).inspect
    end
    `)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "-1" {
		t.Fatalf("repeated sibling pair compared to %s, want -1", got.String())
	}
}

// A NaN element leaves the arrays unordered, exactly as a bare NaN
// comparison is unordered.
func TestNaNElementLeavesArraysUnordered(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      nan = 0.0 / 0.0
      ([1, nan] <=> [1, 2.0]).inspect
    end
    `)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "nil" {
		t.Fatalf("NaN element compared to %s, want nil", got.String())
	}
}
