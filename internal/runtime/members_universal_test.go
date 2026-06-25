package runtime

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

// entryMapPtr returns the identity of a hash entry map for comparing whether two
// hashes share the same backing storage.
func entryMapPtr(entries map[string]Value) uintptr {
	return reflect.ValueOf(entries).Pointer()
}

// hashEntryMapPtr returns the identity of a hash value's entry map.
func hashEntryMapPtr(v Value) uintptr {
	return entryMapPtr(v.Hash())
}

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

// TestEqualPredicateRebindsToHostClone confirms that when a returned graph holds
// both a mutable receiver and a predicate bound to it, host-cloning the graph
// rebinds the predicate to the cloned receiver. Without the rebind the cloned
// predicate would keep comparing against the pre-clone receiver, so re-entering
// the host clone with probe(clonedReceiver) would wrongly report not-identical
// even though both came from the same object. A hash exercises the case because
// hash identity is its (now cloned) wrapper rather than a value-stable scalar.
func TestEqualPredicateRebindsToHostClone(t *testing.T) {
	t.Parallel()

	receiver := NewHash(map[string]Value{"a": NewInt(1)})
	probe, ok := universalMember(receiver, "equal?")
	if !ok {
		t.Fatal("universalMember did not resolve equal? for a hash")
	}

	// Export the receiver and its bound predicate together, the way Script.Call
	// hands a returned graph to the host, then clone the whole graph.
	cloned := cloneValueForHost(NewArray([]Value{receiver, probe}))
	clonedItems := cloned.Array()
	clonedReceiver := clonedItems[0]
	clonedProbe := clonedItems[1]

	if clonedReceiver.Identical(receiver) {
		t.Fatal("cloned receiver shares identity with the original; test cannot observe the rebind")
	}

	clonedBuiltin := valueBuiltin(clonedProbe)
	if clonedBuiltin == nil {
		t.Fatal("cloned equal? did not resolve to a builtin")
	}

	got, err := clonedBuiltin.Fn(nil, NewNil(), []Value{clonedReceiver}, nil, NewNil())
	if err != nil {
		t.Fatalf("cloned equal? against cloned receiver: %v", err)
	}
	if !got.Bool() {
		t.Fatal("cloned equal? against the cloned receiver returned false; the predicate did not rebind to the cloned receiver")
	}

	// The rebound predicate must still report false against the pre-clone receiver,
	// which is now a distinct object on the original side of the boundary.
	got, err = clonedBuiltin.Fn(nil, NewNil(), []Value{receiver}, nil, NewNil())
	if err != nil {
		t.Fatalf("cloned equal? against original receiver: %v", err)
	}
	if got.Bool() {
		t.Fatal("cloned equal? against the original receiver returned true; the clone must compare by cloned identity")
	}
}

// TestEqualPredicateRebindsAcrossScriptCalls exercises the rebind end to end
// through Script.Call: one call exports a hash and its bound equal? predicate,
// and a second call receives that host-cloned pair and invokes the predicate
// against the receiver from the same pair. The predicate must report identity
// even though Script.Call host-cloned the receiver between the two calls.
func TestEqualPredicateRebindsAcrossScriptCalls(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def export_probe
  obj = {a: 1}
  [obj, obj.equal?]
end

def check(pair)
  pair[1](pair[0])
end`)

	exported, err := script.Call(context.Background(), "export_probe", nil, CallOptions{})
	if err != nil {
		t.Fatalf("export_probe failed: %v", err)
	}
	if exported.Kind() != KindArray {
		t.Fatalf("expected array result, got %#v", exported)
	}

	result, err := script.Call(context.Background(), "check", []Value{exported}, CallOptions{})
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if result.Kind() != KindBool || !result.Bool() {
		t.Fatalf("exported equal? against its own receiver reported %#v; the predicate did not rebind to the host-cloned receiver", result)
	}
}

// TestHostCloneHashPreservesSharedIdentity confirms a hash reachable through two
// paths in a returned graph clones to a single wrapper, so the two cloned
// references stay equal? to each other. Caching only the entry map would rebuild
// a fresh wrapper per path and break identity, since hash identity is the
// wrapper rather than the entry map.
func TestHostCloneHashPreservesSharedIdentity(t *testing.T) {
	t.Parallel()

	shared := NewHash(map[string]Value{"a": NewInt(1)})
	cloned := cloneValueForHost(NewArray([]Value{shared, shared}))
	items := cloned.Array()
	if !items[0].Identical(items[1]) {
		t.Fatal("a hash shared across two paths cloned to distinct wrappers; host clone must preserve shared identity")
	}
	if items[0].Identical(shared) {
		t.Fatal("cloned hash shares identity with the original; test cannot observe the clone")
	}
}

// TestHostCloneHashPreservesSharedEntryMap confirms two distinct hash wrappers
// that intentionally share one mutable entry map clone to wrappers that still
// share a single (cloned) entry map. A host may build such a pair --
// a := NewHash(shared); b := NewHash(shared) -- and rely on index assignment
// mutating that map in place so a[:x] = 1 is visible through b. Caching the clone
// only on the wrapper identity would give each wrapper its own cloned map and
// silently break that aliasing.
func TestHostCloneHashPreservesSharedEntryMap(t *testing.T) {
	t.Parallel()

	sharedEntries := map[string]Value{"a": NewInt(1)}
	a := NewHash(sharedEntries)
	b := NewHash(sharedEntries)
	if hashIdentity(a) == hashIdentity(b) {
		t.Fatal("the two wrappers share identity; test cannot observe distinct wrappers over one map")
	}

	cloned := cloneValueForHost(NewArray([]Value{a, b}))
	items := cloned.Array()
	clonedA, clonedB := items[0], items[1]

	if clonedA.Identical(clonedB) {
		t.Fatal("distinct wrappers cloned to one wrapper; they must stay distinct objects")
	}
	if hashEntryMapPtr(clonedA) != hashEntryMapPtr(clonedB) {
		t.Fatal("cloned wrappers no longer share an entry map; in-place mutation aliasing was lost")
	}
	if hashEntryMapPtr(clonedA) == entryMapPtr(sharedEntries) {
		t.Fatal("cloned entry map shares storage with the original; test cannot observe the clone")
	}

	// A write through one cloned wrapper's entry map must be visible through the
	// other, exactly as a[:x] = 1 would be visible through b.
	clonedA.Hash()["x"] = NewInt(2)
	if got, ok := clonedB.Hash()["x"]; !ok || !got.Equal(NewInt(2)) {
		t.Fatalf("write through one cloned wrapper not visible through the other: got %v ok=%v", got, ok)
	}
}

// TestCallRebindHashPreservesSharedEntryMap is the inbound counterpart to
// TestHostCloneHashPreservesSharedEntryMap: Script.Call rebinds incoming
// arguments through callFunctionRebinder, which must likewise preserve the
// aliasing of two distinct wrappers that share one mutable entry map.
func TestCallRebindHashPreservesSharedEntryMap(t *testing.T) {
	t.Parallel()

	script := compileScript(t, "def run()\n  nil\nend")
	root := newEnv(nil)
	rebinder := newCallFunctionRebinder(script, root, map[string]*ClassDef{}, map[string]*EnumDef{})

	sharedEntries := map[string]Value{"a": NewInt(1)}
	a := NewHash(sharedEntries)
	b := NewHash(sharedEntries)

	rebound := rebinder.rebindValue(NewArray([]Value{a, b}))
	items := rebound.Array()
	reboundA, reboundB := items[0], items[1]

	if reboundA.Identical(reboundB) {
		t.Fatal("distinct wrappers rebound to one wrapper; they must stay distinct objects")
	}
	if hashEntryMapPtr(reboundA) != hashEntryMapPtr(reboundB) {
		t.Fatal("rebound wrappers no longer share an entry map; in-place mutation aliasing was lost")
	}
	if hashEntryMapPtr(reboundA) == entryMapPtr(sharedEntries) {
		t.Fatal("rebound entry map shares storage with the original; test cannot observe the rebind")
	}

	reboundA.Hash()["x"] = NewInt(2)
	if got, ok := reboundB.Hash()["x"]; !ok || !got.Equal(NewInt(2)) {
		t.Fatalf("write through one rebound wrapper not visible through the other: got %v ok=%v", got, ok)
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

// TestEqualPredicateNaNFloatReflexivity confirms equal? stays reflexive for a
// NaN float receiver. IEEE NaN != NaN, so deferring identity to value equality
// would make x.equal?(x) false and break the identity contract. A NaN bound to a
// variable must be equal? to itself, and any two NaN floats report identical
// because Vibescript exposes no distinct object for equal floats.
func TestEqualPredicateNaNFloatReflexivity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want Value
	}{
		{name: "nan reflexive", expr: "x = 0.0 / 0.0\n  x.equal?(x)", want: NewBool(true)},
		{name: "nan equal to nan", expr: "(0.0 / 0.0).equal?(0.0 / 0.0)", want: NewBool(true)},
		{name: "nan vs finite", expr: "(0.0 / 0.0).equal?(1.5)", want: NewBool(false)},
		{name: "finite vs nan", expr: "1.5.equal?(0.0 / 0.0)", want: NewBool(false)},
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

// TestEqualityPredicateTemporaryBuiltinChargesCapturedReceiver confirms a bound
// predicate produced as an immediately invoked temporary callee charges its
// captured receiver against the memory quota, just like a stored probe does.
//
// In `make_probe()(arg)` the inner call returns `big.eql?`, a builtin that
// captures the large `big` string, and the outer call invokes it with a large
// transient `arg` built inline. Neither `big` nor `arg` is reachable from any
// environment at the outer call — `big` lives only inside the returned builtin's
// capture and `arg` only on the Go call stack — so the environment-walking base
// sees neither. Only charging the callee's captured payload alongside the outer
// arguments accounts for the peak where both are live at once. The quota sits
// above either operand alone but below their combination, so the run must trip
// the quota; a regression that skipped charging the temporary callee would let
// the combined footprint slip past.
func TestEqualityPredicateTemporaryBuiltinChargesCapturedReceiver(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 200000, MemoryQuotaBytes: 7000}, `def make_probe
  "abcdefghij".ljust(3000, "z").eql?
end

def run
  make_probe()("y".ljust(3000, "w"))
end`)
	requireRunMemoryQuotaError(t, script, nil, CallOptions{})
}

// TestEqualityPredicateTemporaryBuiltinFitsUnderGenerousQuota is the control for
// TestEqualityPredicateTemporaryBuiltinChargesCapturedReceiver: the identical
// temporary-callee invocation succeeds once the quota comfortably exceeds the
// captured receiver plus the transient argument. This isolates the rejection to
// the combined peak rather than either operand on its own.
func TestEqualityPredicateTemporaryBuiltinFitsUnderGenerousQuota(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 200000, MemoryQuotaBytes: 1 << 20}, `def make_probe
  "abcdefghij".ljust(3000, "z").eql?
end

def run
  make_probe()("y".ljust(3000, "w"))
end`)
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
		t.Fatalf("expected temporary-callee invocation to stay within a generous quota, got %v", err)
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
