package runtime

import (
	"context"
	"fmt"
	"testing"
)

// TestEqualPredicateEnumClonedIdentity confirms equal? on enums and enum values
// reports backing-storage identity, not the structural equivalence Equal uses.
// A value cloned out to the host (for example, by a capability return) holds a
// fresh backing pointer while keeping the same owner script and member name.
// enumDefsEqual/enumValueDefsEqual treat such a clone as == (and eql?) to the
// original, but equal? must report false because the two no longer share
// storage. equal? against the same backing pointer still reports true.
func TestEqualPredicateEnumClonedIdentity(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `enum Status
  Draft
  Published
end`)

	statusDraft := enumTestValue(t, script, "Status", "Draft")
	clonedDraft := cloneValueForHost(statusDraft)
	if clonedDraft.Kind() != KindEnumValue {
		t.Fatalf("cloneValueForHost(Status::Draft) kind = %v, want enum value", clonedDraft.Kind())
	}
	if EnumValueOf(clonedDraft) == EnumValueOf(statusDraft) {
		t.Fatal("cloneValueForHost(Status::Draft) shares the original backing pointer; test cannot observe the identity contract")
	}
	if !clonedDraft.Equal(statusDraft) {
		t.Fatal("cloned enum value should remain == to the original")
	}
	if clonedDraft.Eql(statusDraft) != clonedDraft.Equal(statusDraft) {
		t.Fatal("enum value eql? should match == (same kind and value)")
	}
	if clonedDraft.Identical(statusDraft) {
		t.Fatal("cloned enum value must not be equal? to the original; it holds distinct storage")
	}
	if !statusDraft.Identical(statusDraft) {
		t.Fatal("an enum value must be equal? to itself")
	}

	enumDef := NewEnum(script.enums["Status"])
	clonedEnum := cloneValueForHost(enumDef)
	if clonedEnum.Kind() != KindEnum {
		t.Fatalf("cloneValueForHost(Status) kind = %v, want enum", clonedEnum.Kind())
	}
	if EnumOf(clonedEnum) == EnumOf(enumDef) {
		t.Fatal("cloneValueForHost(Status) shares the original backing pointer; test cannot observe the identity contract")
	}
	if !clonedEnum.Equal(enumDef) {
		t.Fatal("cloned enum should remain == to the original")
	}
	if clonedEnum.Identical(enumDef) {
		t.Fatal("cloned enum must not be equal? to the original; it holds distinct storage")
	}
	if !enumDef.Identical(enumDef) {
		t.Fatal("an enum must be equal? to itself")
	}
}

// TestEqualPredicateEnumDispatchesIdentity confirms the universal equal?
// predicate on enums and enum values routes through Value.Identical rather than
// Value.Equal. The predicate builtin captures the receiver, so invoking it with
// a host clone that is Equal-but-not-Identical to the receiver must report
// false, while the receiver compared with itself reports true.
func TestEqualPredicateEnumDispatchesIdentity(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `enum Status
  Draft
  Published
end`)

	statusDraft := enumTestValue(t, script, "Status", "Draft")
	clonedDraft := cloneValueForHost(statusDraft)

	probe, ok := universalMember(statusDraft, "equal?")
	if !ok {
		t.Fatal("universalMember did not resolve equal? for an enum value")
	}
	builtin := valueBuiltin(probe)
	if builtin == nil {
		t.Fatal("equal? did not resolve to a builtin")
	}

	got, err := builtin.Fn(nil, NewNil(), []Value{clonedDraft}, nil, NewNil())
	if err != nil {
		t.Fatalf("equal? against host clone: %v", err)
	}
	if got.Bool() {
		t.Fatal("equal? against a host clone returned true; it must dispatch to Identical and report false")
	}

	got, err = builtin.Fn(nil, NewNil(), []Value{statusDraft}, nil, NewNil())
	if err != nil {
		t.Fatalf("equal? against self: %v", err)
	}
	if !got.Bool() {
		t.Fatal("equal? against the same backing value returned false; it must report true")
	}
}

// TestEqlPredicate exercises the universal eql? predicate across core value
// kinds: it reports hash-key equality, so operands must share a kind and value.
// An Int never eql-matches a Float even when their numeric values coincide,
// mirroring Ruby's 1.eql?(1.0) == false.
func TestEqlPredicate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want Value
	}{
		{name: "int equal", expr: `1.eql?(1)`, want: NewBool(true)},
		{name: "int unequal", expr: `1.eql?(2)`, want: NewBool(false)},
		{name: "int vs float", expr: `1.eql?(1.0)`, want: NewBool(false)},
		{name: "float equal", expr: `1.0.eql?(1.0)`, want: NewBool(true)},
		{name: "float vs int", expr: `1.0.eql?(1)`, want: NewBool(false)},
		{name: "string equal", expr: `"a".eql?("a")`, want: NewBool(true)},
		{name: "string unequal", expr: `"a".eql?("b")`, want: NewBool(false)},
		{name: "string vs int", expr: `"1".eql?(1)`, want: NewBool(false)},
		{name: "symbol equal", expr: `:a.eql?(:a)`, want: NewBool(true)},
		{name: "symbol vs string", expr: `:a.eql?("a")`, want: NewBool(false)},
		{name: "bool equal", expr: `true.eql?(true)`, want: NewBool(true)},
		{name: "bool unequal", expr: `true.eql?(false)`, want: NewBool(false)},
		{name: "nil equal", expr: `nil.eql?(nil)`, want: NewBool(true)},
		{name: "nil vs false", expr: `nil.eql?(false)`, want: NewBool(false)},
		{name: "range equal", expr: `(1..3).eql?(1..3)`, want: NewBool(true)},
		{name: "range unequal", expr: `(1..3).eql?(1..4)`, want: NewBool(false)},
		{name: "array by content", expr: `[1, 2].eql?([1, 2])`, want: NewBool(true)},
		{name: "array unequal content", expr: `[1, 2].eql?([1, 3])`, want: NewBool(false)},
		{name: "hash by content", expr: `{ a: 1 }.eql?({ a: 1 })`, want: NewBool(true)},
		{name: "duration equal", expr: `1.hour.eql?(1.hour)`, want: NewBool(true)},
		{name: "time equal", expr: `Time.utc(2024, 1, 1).eql?(Time.utc(2024, 1, 1))`, want: NewBool(true)},
		{name: "money equal", expr: `money("1.00 USD").eql?(money("1.00 USD"))`, want: NewBool(true)},
		{name: "money currency mismatch", expr: `money("1.00 USD").eql?(money("1.00 EUR"))`, want: NewBool(false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend\n")
			got := callFunc(t, script, "run", nil)
			if !got.Equal(tc.want) {
				t.Fatalf("%s = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

// TestEqualPredicateImmutableValues confirms equal? treats immutable scalar
// kinds as identical when they share a kind and value, since the language
// exposes no separate identity for equal immutables (1.equal?(1) is true).
func TestEqualPredicateImmutableValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want Value
	}{
		{name: "int identical", expr: `1.equal?(1)`, want: NewBool(true)},
		{name: "int different", expr: `1.equal?(2)`, want: NewBool(false)},
		{name: "int vs float", expr: `1.equal?(1.0)`, want: NewBool(false)},
		{name: "float identical", expr: `1.0.equal?(1.0)`, want: NewBool(true)},
		{name: "string identical content", expr: `"a".equal?("a")`, want: NewBool(true)},
		{name: "string different content", expr: `"a".equal?("b")`, want: NewBool(false)},
		{name: "symbol identical", expr: `:a.equal?(:a)`, want: NewBool(true)},
		{name: "bool identical", expr: `true.equal?(true)`, want: NewBool(true)},
		{name: "nil identical", expr: `nil.equal?(nil)`, want: NewBool(true)},
		{name: "range identical", expr: `(1..3).equal?(1..3)`, want: NewBool(true)},
		{name: "duration identical", expr: `1.hour.equal?(1.hour)`, want: NewBool(true)},
		{name: "time identical", expr: `Time.utc(2024, 1, 1).equal?(Time.utc(2024, 1, 1))`, want: NewBool(true)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend\n")
			got := callFunc(t, script, "run", nil)
			if !got.Equal(tc.want) {
				t.Fatalf("%s = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

// TestEqualPredicateReferenceIdentity confirms equal? on mutable composites and
// script instances reports backing-storage identity: a value is identical to
// itself and to an alias, but two independently constructed composites with the
// same contents are not identical even though they are eql?.
func TestEqualPredicateReferenceIdentity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		script string
		want   []Value
	}{
		{
			name: "array",
			script: `def run()
  a = [1, 2, 3]
  b = a
  c = [1, 2, 3]
  [a.equal?(b), a.equal?(c), a.eql?(c)]
end`,
			want: []Value{NewBool(true), NewBool(false), NewBool(true)},
		},
		{
			name: "hash",
			script: `def run()
  a = { x: 1 }
  b = a
  c = { x: 1 }
  [a.equal?(b), a.equal?(c), a.eql?(c)]
end`,
			want: []Value{NewBool(true), NewBool(false), NewBool(true)},
		},
		{
			name: "instance",
			script: `class Box
end

def run()
  a = Box.new
  b = a
  c = Box.new
  [a.equal?(b), a.equal?(c), a.eql?(b), a.eql?(c)]
end`,
			want: []Value{NewBool(true), NewBool(false), NewBool(true), NewBool(false)},
		},
		{
			// Empty hash literals build a fresh backing map per literal, so two
			// {} are distinct objects but eql? by content, mirroring Ruby.
			name: "empty hash literals",
			script: `def run()
  a = {}
  b = a
  c = {}
  [a.equal?(b), a.equal?(c), a.eql?(c)]
end`,
			want: []Value{NewBool(true), NewBool(false), NewBool(true)},
		},
		{
			// The JSON parser returns an empty hash for "{}". Two such parses must
			// stay distinct objects, the regression the finding called out.
			name: "json empty objects",
			script: `def run()
  a = JSON.parse("{}")
  c = JSON.parse("{}")
  [a.equal?(a), a.equal?(c), a.eql?(c)]
end`,
			want: []Value{NewBool(true), NewBool(false), NewBool(true)},
		},
		{
			// All empty arrays are equal? to one another. array.select preallocates
			// its result with make([]Value, 0, len(arr)), so filtering everything
			// out yields an empty array with spare capacity and a non-zerobase
			// backing pointer; it must still be equal? to a literal empty array.
			name: "empty array from select",
			script: `def run()
  a = [1].select { |x| false }
  [a.equal?([]), a.eql?([]), [].equal?([])]
end`,
			want: []Value{NewBool(true), NewBool(true), NewBool(true)},
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

// TestEqualityPredicateHashResolutionIsConstantWork confirms resolving the
// universal eql?/equal? predicates on hash and object receivers stays O(1) in the
// number of stored keys. The predicates always win over stored entries for these
// kinds, so resolveMember answers them before typed dispatch; it must not route
// through hashMember's miss path, which materializes did-you-mean candidates from
// every key. A regression that reinstated that walk would scale per-call work and
// allocations with the receiver size, so resolving on a large receiver must stay
// far below the key count rather than allocating once per key.
func TestEqualityPredicateHashResolutionIsConstantWork(t *testing.T) {
	// testing.AllocsPerRun forbids running under t.Parallel, so this test and its
	// subtests run serially.
	const receiverKeys = 4096

	// constantWorkAllocCeiling bounds the per-call allocations the fixed path may
	// make. It is a small constant independent of receiverKeys, so it tolerates the
	// few extra allocations the race detector adds while still failing decisively
	// if resolution reverts to allocating per stored key (which would land in the
	// thousands for a receiverKeys-sized receiver).
	const constantWorkAllocCeiling = 64

	cases := []struct {
		name     string
		make     func(map[string]Value) Value
		property string
	}{
		{name: "hash eql?", make: NewHash, property: "eql?"},
		{name: "hash equal?", make: NewHash, property: "equal?"},
		{name: "object eql?", make: NewObject, property: "eql?"},
		{name: "object equal?", make: NewObject, property: "equal?"},
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
				t.Fatalf("resolving %s on a %d-key receiver allocated %.0f times (ceiling %d); resolution must not scale with key count", tc.property, receiverKeys, allocs, constantWorkAllocCeiling)
			}
		})
	}
}

// buildKeyedEntries returns a map of count distinct string keys for sizing a
// hash or object receiver in allocation regression tests.
func buildKeyedEntries(count int) map[string]Value {
	entries := make(map[string]Value, count)
	for i := range count {
		entries[fmt.Sprintf("key_%d", i)] = NewInt(int64(i))
	}
	return entries
}

// TestEqualityPredicateUserOverride confirms a class may define its own eql? or
// equal? methods, which take precedence over the universal fallback exactly as
// Ruby overrides Object#eql?/Object#equal?.
func TestEqualityPredicateUserOverride(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		method string
	}{
		{name: "eql override", method: "eql?"},
		{name: "equal override", method: "equal?"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `class Tag
  def `+tc.method+`(other)
    "overridden"
  end
end

def run()
  Tag.new.`+tc.method+`(Tag.new)
end`)
			got := callFunc(t, script, "run", nil)
			if !got.Equal(NewString("overridden")) {
				t.Fatalf("%s override = %v, want overridden", tc.method, got)
			}
		})
	}
}

// TestEqualityPredicateNotShadowedByStoredKeys confirms a hash or namespace
// object entry keyed "eql?" or "equal?" is treated as data, not a member, so it
// never preempts the universal equality predicate. Member dispatch (h.eql?)
// resolves the predicate while index access (h["eql?"]) still reads the stored
// value, matching Ruby's separation of method calls from element lookup.
func TestEqualityPredicateNotShadowedByStoredKeys(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		script string
		want   []Value
	}{
		{
			name: "hash eql key",
			script: `def run()
  h = { "eql?": "data" }
  [h.eql?(h), h.eql?({ x: 1 }), h["eql?"]]
end`,
			want: []Value{NewBool(true), NewBool(false), NewString("data")},
		},
		{
			name: "hash equal key",
			script: `def run()
  h = { "equal?": 99 }
  alias = h
  [h.equal?(alias), h.equal?({}), h["equal?"]]
end`,
			want: []Value{NewBool(true), NewBool(false), NewInt(99)},
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

// TestEqualityPredicateNotShadowedByStoredMember confirms a stored instance ivar
// or class var keyed "eql?"/"equal?" is treated as data, not a member, so it
// never preempts the universal equality predicate. assignToMember stores a
// property with no matching setter directly in the receiver's ivars/class vars,
// so `box.equal? = 1` would otherwise let instanceMember/classMember return that
// stored value and make `box.equal?(box)` try to call a non-callable Int. Member
// dispatch must instead resolve the universal identity predicate.
func TestEqualityPredicateNotShadowedByStoredMember(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		script string
		want   []Value
	}{
		{
			name: "instance ivar",
			script: `class Box
end

def run()
  b = Box.new
  b.equal? = 1
  b.eql? = 2
  [b.equal?(b), b.equal?(Box.new), b.eql?(b), b.eql?(Box.new)]
end`,
			want: []Value{NewBool(true), NewBool(false), NewBool(true), NewBool(false)},
		},
		{
			name: "class var",
			script: `class Box
end

def run()
  Box.equal? = 1
  Box.eql? = 2
  [Box.equal?(Box), Box.equal?(1), Box.eql?(Box)]
end`,
			want: []Value{NewBool(true), NewBool(false), NewBool(true)},
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

// TestEqualityPredicatePrivateOverrideRaises confirms a private eql?/equal?
// override is not silently bypassed by the universal fallback. External lookup
// paths — a bound member probe and the reduce(:op) shorthand — must raise the
// private-method error rather than resolving the builtin predicate.
func TestEqualityPredicatePrivateOverrideRaises(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		script string
		fn     string
		want   string
	}{
		{
			name: "bound probe of private eql",
			script: `class Tag
  private def eql?(other)
    true
  end
end

def run()
  probe = Tag.new.eql?
  probe(Tag.new)
end`,
			fn:   "run",
			want: "private method eql?",
		},
		{
			name: "direct call of private equal",
			script: `class Tag
  private def equal?(other)
    true
  end
end

def run()
  Tag.new.equal?(Tag.new)
end`,
			fn:   "run",
			want: "private method equal?",
		},
		{
			name: "reduce sends private eql",
			script: `class Tag
  private def eql?(other)
    true
  end
end

def run()
  [Tag.new, Tag.new].reduce(:eql?)
end`,
			fn:   "run",
			want: "private method eql?",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			requireCallErrorContains(t, script, tc.fn, nil, CallOptions{}, tc.want)
		})
	}
}

// TestEqualityPredicatePrivateMethodInternalAccess confirms the private-member
// suppression only blocks external callers: the receiver itself may still invoke
// its private eql?/equal? override, so the universal fallback never masks a
// legitimately reachable private method.
func TestEqualityPredicatePrivateMethodInternalAccess(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `class Tag
  private def eql?(other)
    "internal"
  end

  def probe(other)
    eql?(other)
  end
end

def run()
  Tag.new.probe(Tag.new)
end`)
	got := callFunc(t, script, "run", nil)
	if !got.Equal(NewString("internal")) {
		t.Fatalf("internal private eql? call = %v, want internal", got)
	}
}

// TestEqualityPredicateStoredBuiltin confirms the stored-member-call path, where
// the bound predicate builtin is invoked separately from the receiver, follows
// the same contract as a direct call.
func TestEqualityPredicateStoredBuiltin(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `def run()
  probe = 1.eql?
  identity = 1.equal?
  [probe(1), probe(1.0), probe(2), identity(1), identity(2)]
end`)
	result := callFunc(t, script, "run", nil)
	compareArrays(t, result, []Value{
		NewBool(true),
		NewBool(false),
		NewBool(false),
		NewBool(true),
		NewBool(false),
	})
}

// TestEqualityPredicateStoredBuiltinChargesReceiver confirms a stored bound
// predicate is charged against the memory quota for the receiver it keeps alive.
// Each iteration binds a probe to a distinct, growing string, so the retained
// receivers accumulate well beyond the builtin slots alone. The quota sits above
// the slots-only footprint (what treating builtins as free would charge) but
// below the slots-plus-receivers footprint, so the run must trip the quota.
func TestEqualityPredicateStoredBuiltinChargesReceiver(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 200000, MemoryQuotaBytes: 8192}, `def run
  probes = []
  big = ""
  for i in 1..50
    big = big + "abcdefghij"
    probes = probes.push(big.eql?)
  end
  probes.size
end`)
	requireRunMemoryQuotaError(t, script, nil, CallOptions{})
}

// TestEqualityPredicateLoopWithoutStoredBuiltinFits is the control for
// TestEqualityPredicateStoredBuiltinChargesReceiver: the identical loop that
// grows the same strings but does not retain any probe stays within the same
// quota. This isolates the OOM to the retained receivers rather than the loop's
// transient string allocations.
func TestEqualityPredicateLoopWithoutStoredBuiltinFits(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 200000, MemoryQuotaBytes: 8192}, `def run
  count = 0
  big = ""
  for i in 1..50
    big = big + "abcdefghij"
    count = count + 1
  end
  count
end`)
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
		t.Fatalf("expected control loop to stay within quota, got %v", err)
	}
}

// TestEqualityPredicateCallErrors confirms eql? and equal? reject the wrong
// arity, keyword arguments, and blocks rather than silently coercing them.
func TestEqualityPredicateCallErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want string
	}{
		{name: "eql no args", expr: `1.eql?()`, want: "int.eql? expects 1 argument, got 0"},
		{name: "eql two args", expr: `1.eql?(1, 2)`, want: "int.eql? expects 1 argument, got 2"},
		{name: "equal no args", expr: `1.equal?()`, want: "int.equal? expects 1 argument, got 0"},
		{name: "eql kwargs", expr: `1.eql?(other: 1)`, want: "int.eql? does not accept keyword arguments"},
		{name: "eql block", expr: `1.eql?(1) { 2 }`, want: "int.eql? does not accept a block"},
		{name: "string eql block", expr: `"a".eql?("a") { 2 }`, want: "string.eql? does not accept a block"},
		{name: "array equal arity", expr: `[1].equal?()`, want: "array.equal? expects 1 argument, got 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend\n")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}
