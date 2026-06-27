package runtime

import (
	"context"
	"testing"
)

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

// TestBlockHelperHashResolutionIsConstantWork confirms resolving the universal
// tap/yield_self block helpers on a hash or object receiver that does not store
// such a key stays O(1) in the number of stored keys. Unlike the equality
// predicates, the block helpers are data-eligible (a stored tap/yield_self key is
// data), so they resolve through typed dispatch rather than the always-wins
// shortcut. Their miss must therefore be reported by resolveTypedMember with a
// cheap fixed error rather than routed through hashMember's miss path, which
// materializes did-you-mean candidates from every key. A regression that routed
// the miss through hashMember would scale per-call work and allocations with the
// receiver size, so resolving on a large receiver must stay far below the key
// count rather than allocating once per key.
func TestBlockHelperHashResolutionIsConstantWork(t *testing.T) {
	// testing.AllocsPerRun forbids running under t.Parallel, so this test and its
	// subtests run serially.
	const receiverKeys = 4096

	// constantWorkAllocCeiling bounds the per-call allocations the fixed-miss path
	// may make. It is a small constant independent of receiverKeys, so it tolerates
	// the few extra allocations the race detector adds while still failing
	// decisively if resolution reverts to building did-you-mean candidates per
	// stored key (which would land in the thousands for a receiverKeys-sized
	// receiver).
	const constantWorkAllocCeiling = 64

	cases := []struct {
		name     string
		make     func(map[string]Value) Value
		property string
	}{
		{name: "hash tap", make: NewHash, property: "tap"},
		{name: "hash yield_self", make: NewHash, property: "yield_self"},
		{name: "object tap", make: NewObject, property: "tap"},
		{name: "object yield_self", make: NewObject, property: "yield_self"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := compileScript(t, "def run()\n  nil\nend")
			exec := newExecutionForCall(script, context.Background(), newEnv(nil), CallOptions{})

			receiver := tc.make(buildKeyedEntries(receiverKeys))
			allocs := testing.AllocsPerRun(100, func() {
				if _, err := exec.resolveMember(receiver, tc.property, Position{}, false); err != nil {
					t.Fatalf("resolveMember(%s) on %s: %v", tc.property, receiver.Kind(), err)
				}
			})
			if allocs > constantWorkAllocCeiling {
				t.Fatalf("resolving %s on a %d-key receiver allocated %.0f times (ceiling %d); a block-helper miss must not scale with key count", tc.property, receiverKeys, allocs, constantWorkAllocCeiling)
			}
		})
	}
}

// TestBlockHelperShadowedByStoredKeys confirms a stored hash entry or object
// data field keyed "tap" or "yield_self" is ordinary data the typed dispatch
// returns, so it shadows the universal block helper. Member dispatch (h.tap)
// reads the stored value rather than invoking the helper, while a receiver
// without such a key still resolves the helper. This is the data-eligible
// counterpart to the equality predicates, which always win over a stored key.
func TestBlockHelperShadowedByStoredKeys(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		script string
		want   []Value
	}{
		{
			name: "hash tap key",
			script: `def run()
  h = { "tap": "data" }
  [h.tap, h["tap"]]
end`,
			want: []Value{NewString("data"), NewString("data")},
		},
		{
			name: "hash yield_self key",
			script: `def run()
  h = { "yield_self": 7 }
  [h.yield_self, h["yield_self"]]
end`,
			want: []Value{NewInt(7), NewInt(7)},
		},
		{
			name: "hash without tap key still taps",
			script: `def run()
  h = { a: 1 }
  [h.tap { |x| x }, h.yield_self { |x| x.size }]
end`,
			want: []Value{NewHash(map[string]Value{"a": NewInt(1)}), NewInt(1)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			compareArrays(t, result, tc.want)
		})
	}
}
