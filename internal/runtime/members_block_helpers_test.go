package runtime

import "testing"

// TestTapReturnsReceiverAcrossKinds confirms Object#tap yields the receiver to
// its block and returns the original receiver unchanged, regardless of the
// block's result, on every core value kind.
func TestTapReturnsReceiverAcrossKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want Value
	}{
		{"string", `"ada".tap { |s| s.upcase }`, NewString("ada")},
		{"int", `3.tap { |n| n * 100 }`, NewInt(3)},
		{"float", `(2.5).tap { |n| n + 1 }`, NewFloat(2.5)},
		{"bool", `true.tap { |b| false }`, NewBool(true)},
		{"nil", `nil.tap { |x| 1 }`, NewNil()},
		{"symbol", `:ok.tap { |s| s }`, NewSymbol("ok")},
		{"array", `[1, 2, 3].tap { |a| a.length }`, NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})},
		{"hash", `{a: 1}.tap { |h| h }`, NewHash(map[string]Value{"a": NewInt(1)})},
		{"range", `(1..3).tap { |r| r }`, NewRange(Range{Start: 1, End: 3})},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalUniversal(t, tc.expr)
			if !got.Equal(tc.want) {
				t.Fatalf("%s = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

// TestYieldSelfReturnsBlockResultAcrossKinds confirms Object#yield_self yields
// the receiver to its block and returns the block's result on every core value
// kind.
func TestYieldSelfReturnsBlockResultAcrossKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want Value
	}{
		{"string", `"ada".yield_self { |s| s.upcase }`, NewString("ADA")},
		{"int", `3.yield_self { |n| n * 100 }`, NewInt(300)},
		{"float", `(2.5).yield_self { |n| n + 1 }`, NewFloat(3.5)},
		{"bool", `true.yield_self { |b| !b }`, NewBool(false)},
		{"nil", `nil.yield_self { |x| 7 }`, NewInt(7)},
		{"symbol", `:ok.yield_self { |s| s.to_s }`, NewString("ok")},
		{"array", `[1, 2, 3].yield_self { |a| a.length }`, NewInt(3)},
		{"hash", `{a: 1}.yield_self { |h| h.size }`, NewInt(1)},
		{"range", `(1..3).yield_self { |r| r.to_a }`, NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalUniversal(t, tc.expr)
			if !got.Equal(tc.want) {
				t.Fatalf("%s = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

// TestUniversalHelpersYieldReceiverToBlock confirms the block receives the
// receiver as its single argument: tap returns the receiver after running the
// block against it, while yield_self returns the block's transformation of the
// same yielded value.
func TestUniversalHelpersYieldReceiverToBlock(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run()
  tapped = "ada".tap { |name| name.upcase }
  transformed = "ada".yield_self { |name| name.upcase }
  [tapped, transformed]
end`)
	got := callFunc(t, script, "run", nil)
	want := NewArray([]Value{
		NewString("ada"),
		NewString("ADA"),
	})
	if diff := valueDiff(want, got); diff != "" {
		t.Fatalf("yielded-receiver mismatch (-want +got):\n%s", diff)
	}
}

// TestUniversalHelpersRejectMisuse confirms tap and yield_self reject calls
// without a block and calls that supply positional or keyword arguments.
func TestUniversalHelpersRejectMisuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{"tap_no_block", `"x".tap`, "tap requires a block"},
		{"yield_self_no_block", `"x".yield_self`, "yield_self requires a block"},
		{"tap_positional_arg", `"x".tap(1) { |s| s }`, "tap does not take arguments"},
		{"yield_self_positional_arg", `"x".yield_self(1) { |s| s }`, "yield_self does not take arguments"},
		{"tap_keyword_arg", `"x".tap(k: 1) { |s| s }`, "tap does not take keyword arguments"},
		{"yield_self_keyword_arg", `"x".yield_self(k: 1) { |s| s }`, "yield_self does not take keyword arguments"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}
