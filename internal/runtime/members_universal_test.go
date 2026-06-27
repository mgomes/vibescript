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

// TestHostCloneEnumPreservesAliasIdentity confirms two references to the same
// enum member in a returned graph (for example [Status::Draft, Status::Draft])
// clone to one backing pointer, so the cloned aliases stay equal? to each other.
// cloneEnumDef rebuilds a fresh *EnumDef and fresh members per call, so without
// memoizing the clone on the source *EnumDef the two occurrences would clone to
// distinct pointers; equal? compares enums and members by backing pointer, so
// those distinct clones would wrongly report not-identical even though they were
// one member inside the script. The cloned enum definition reachable through a
// member must likewise be the same pointer for both aliases.
func TestHostCloneEnumPreservesAliasIdentity(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `enum Status
  Draft
  Published
end`)

	statusDraft := enumTestValue(t, script, "Status", "Draft")
	statusPublished := enumTestValue(t, script, "Status", "Published")

	cloned := cloneValueForHost(NewArray([]Value{statusDraft, statusDraft, statusPublished}))
	items := cloned.Array()

	if !items[0].Identical(items[1]) {
		t.Fatal("the same enum member cloned through two paths produced distinct members; aliases must stay equal?")
	}
	if EnumValueOf(items[0]) != EnumValueOf(items[1]) {
		t.Fatal("cloned enum member aliases hold distinct *EnumValueDef pointers; host clone must memoize the enum clone")
	}
	if items[0].Identical(statusDraft) {
		t.Fatal("cloned enum member shares identity with the original; test cannot observe the clone")
	}

	// Two members of one enum must clone through the same enum definition so
	// member-level identity stays consistent across the whole returned graph.
	draftEnum := EnumValueOf(items[0]).Enum
	publishedEnum := EnumValueOf(items[2]).Enum
	if draftEnum != publishedEnum {
		t.Fatal("members of one enum cloned through distinct enum definitions; the enum clone must be memoized for the whole graph")
	}
	if draftEnum == EnumValueOf(statusDraft).Enum {
		t.Fatal("cloned enum definition shares identity with the original; test cannot observe the clone")
	}
}

// TestHostCloneEnumDefAndMemberShareIdentity confirms an enum definition and one
// of its members reachable through the same returned graph clone through one
// memoized *EnumDef, so the cloned member belongs to the cloned enum. Without the
// shared enum cache the standalone enum value and the member would clone to two
// distinct enum definitions and break member-of-enum identity across the boundary.
func TestHostCloneEnumDefAndMemberShareIdentity(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `enum Status
  Draft
  Published
end`)

	enumDef := NewEnum(script.enums["Status"])
	statusDraft := enumTestValue(t, script, "Status", "Draft")

	cloned := cloneValueForHost(NewArray([]Value{enumDef, statusDraft}))
	items := cloned.Array()

	clonedEnum := EnumOf(items[0])
	clonedMemberEnum := EnumValueOf(items[1]).Enum
	if clonedEnum != clonedMemberEnum {
		t.Fatal("the cloned enum definition and the cloned member's enum are distinct; the enum clone must be memoized across the graph")
	}
	if clonedEnum == EnumOf(enumDef) {
		t.Fatal("cloned enum definition shares identity with the original; test cannot observe the clone")
	}
}

// TestHostCloneFunctionPreservesAliasIdentity confirms two references to the same
// script function in a returned graph (for example [inc, inc]) clone to one
// backing pointer, so the cloned aliases stay equal? to each other.
// cloneFunctionForHostWithState rebuilds a fresh *ScriptFunction per call, so
// without memoizing the clone on the source *ScriptFunction the two occurrences
// would clone to distinct pointers; equal? compares functions by backing pointer,
// so those distinct clones would wrongly report not-identical even though they
// were one function inside the script.
func TestHostCloneFunctionPreservesAliasIdentity(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `def inc(n)
  n + 1
end`)

	inc := exportedFunctionValue(t, script, "inc")

	cloned := cloneValueForHost(NewArray([]Value{inc, inc}))
	items := cloned.Array()

	if FunctionOf(items[0]) == FunctionOf(inc) {
		t.Fatal("cloned function shares the original backing pointer; test cannot observe the clone")
	}
	if FunctionOf(items[0]) != FunctionOf(items[1]) {
		t.Fatal("the same function cloned through two paths produced distinct *ScriptFunction pointers; host clone must memoize the function clone")
	}
	if !items[0].Identical(items[1]) {
		t.Fatal("the same function cloned through two paths is not equal? to itself; aliases must stay identical across the host boundary")
	}
}

// TestHostClonePlainBuiltinPreservesAliasIdentity confirms two references to the
// same plain (non receiver-bound) builtin in a returned graph (for example
// `p = JSON.parse; [p, p]`) clone to one backing *Builtin, so the cloned aliases
// stay equal? to each other. cloneBuiltinValue mints a fresh *Builtin per
// occurrence, so without memoizing the clone on the source *Builtin the two
// occurrences would clone to distinct pointers; equal? compares builtins by
// backing pointer, so those distinct clones would wrongly report not-identical
// even though they were one callable inside the script.
func TestHostClonePlainBuiltinPreservesAliasIdentity(t *testing.T) {
	t.Parallel()

	parse := NewBuiltin("JSON.parse", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewNil(), nil
	})

	cloned := cloneValueForHost(NewArray([]Value{parse, parse}))
	items := cloned.Array()

	cloneA := valueBuiltin(items[0])
	cloneB := valueBuiltin(items[1])
	if cloneA == nil || cloneB == nil {
		t.Fatal("cloned plain builtin aliases did not resolve to builtins")
	}
	if cloneA == valueBuiltin(parse) {
		t.Fatal("cloned plain builtin shares the original backing pointer; test cannot observe the clone")
	}
	if cloneA != cloneB {
		t.Fatal("the same plain builtin cloned through two paths produced distinct *Builtin pointers; host clone must memoize the builtin clone")
	}
	if !items[0].Identical(items[1]) {
		t.Fatal("the same plain builtin cloned through two paths is not equal? to itself; aliases must stay identical across the host boundary")
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

// TestHostCloneBoundPredicatePreservesAliasIdentity confirms the same bound
// predicate (a bound equal?) reachable through two paths in a returned graph
// clones to a single *Builtin, so the two cloned references stay equal? to each
// other. Reconstructing a fresh *Builtin per path would give each alias its own
// pointer; since equal? compares builtins by backing pointer, those aliases
// would wrongly report not-identical across the host boundary. Each cloned alias
// must also rebind to the cloned receiver from the same graph.
func TestHostCloneBoundPredicatePreservesAliasIdentity(t *testing.T) {
	t.Parallel()

	receiver := NewHash(map[string]Value{"a": NewInt(1)})
	probe, ok := universalMember(receiver, "equal?")
	if !ok {
		t.Fatal("universalMember did not resolve equal? for a hash")
	}

	// Export the receiver and the same bound predicate through two array slots,
	// the way Script.Call hands a returned graph to the host, then clone it.
	cloned := cloneValueForHost(NewArray([]Value{receiver, probe, probe}))
	items := cloned.Array()
	clonedReceiver := items[0]
	probeA := valueBuiltin(items[1])
	probeB := valueBuiltin(items[2])
	if probeA == nil || probeB == nil {
		t.Fatal("cloned equal? aliases did not resolve to builtins")
	}

	if !items[1].Identical(items[2]) {
		t.Fatal("the same bound predicate cloned through two paths produced distinct builtins; aliases must stay equal?")
	}
	if probeA != probeB {
		t.Fatal("cloned predicate aliases hold distinct *Builtin pointers; host clone must memoize the bound builtin")
	}

	// Each alias must still rebind to the cloned receiver from the same graph.
	for i, b := range []*Builtin{probeA, probeB} {
		got, err := b.Fn(nil, NewNil(), []Value{clonedReceiver}, nil, NewNil())
		if err != nil {
			t.Fatalf("cloned equal? alias %d against cloned receiver: %v", i, err)
		}
		if !got.Bool() {
			t.Fatalf("cloned equal? alias %d returned false against the cloned receiver; it did not rebind", i)
		}
	}
}

// TestHostCloneRecursiveBoundPredicatePreservesAliasIdentity covers a bound
// predicate whose receiver reaches the predicate itself: an array `a` stores
// `p = a.equal?`, returned as `[p, a]`. Cloning item 0 (`p`) must register its
// clone before cloning the receiver `a`, because the walk through `a` reaches `p`
// again; otherwise that recursion mints a second clone and the outer call
// overwrites the cache, so the predicate cloned directly (`cloned[0]`) and the one
// reached through the receiver (`cloned[1][0]`) end up distinct and wrongly report
// not-`equal?`, even though they were one builtin before the host boundary.
func TestHostCloneRecursiveBoundPredicatePreservesAliasIdentity(t *testing.T) {
	t.Parallel()

	// Build the receiver array first, bind the predicate to it, then store the
	// predicate back into the same backing slice so the receiver reaches the
	// predicate bound to it. Array identity is the backing pointer, so the in-place
	// write keeps the probe's captured receiver pointing at this same array.
	slot := make([]Value, 1)
	receiver := NewArray(slot)
	probe, ok := universalMember(receiver, "equal?")
	if !ok {
		t.Fatal("universalMember did not resolve equal? for an array")
	}
	slot[0] = probe

	cloned := cloneValueForHost(NewArray([]Value{probe, receiver}))
	items := cloned.Array()
	clonedProbe := items[0]
	clonedReceiver := items[1]

	if clonedReceiver.Identical(receiver) {
		t.Fatal("cloned receiver shares identity with the original; test cannot observe the clone")
	}

	reachedProbe := clonedReceiver.Array()[0]
	if !clonedProbe.Identical(reachedProbe) {
		t.Fatal("the predicate cloned directly and the one reached through its own receiver are distinct builtins; the recursive clone must reuse the reserved clone")
	}

	// The deduplicated clone must rebind to the cloned receiver from the same graph.
	clonedBuiltin := valueBuiltin(clonedProbe)
	if clonedBuiltin == nil {
		t.Fatal("cloned equal? did not resolve to a builtin")
	}
	got, err := clonedBuiltin.Fn(nil, NewNil(), []Value{clonedReceiver}, nil, NewNil())
	if err != nil {
		t.Fatalf("cloned equal? against cloned receiver: %v", err)
	}
	if !got.Bool() {
		t.Fatal("cloned equal? returned false against the cloned receiver; the recursive clone did not rebind")
	}
}

// TestCallRebindRecursiveBoundPredicatePreservesAliasIdentity is the inbound
// counterpart to TestHostCloneRecursiveBoundPredicatePreservesAliasIdentity:
// Script.Call rebinds incoming arguments through callFunctionRebinder, which must
// likewise reserve a bound predicate's clone before rebinding its receiver so a
// receiver that reaches the predicate bound to it (an argument `[p, a]` where
// `a[0]` is the same `p = a.equal?`) dedups to one builtin. Otherwise the callee
// observes arg[0].equal?(arg[1][0]) == false even though the inbound graph held a
// single predicate object.
func TestCallRebindRecursiveBoundPredicatePreservesAliasIdentity(t *testing.T) {
	t.Parallel()

	script := compileScript(t, "def run()\n  nil\nend")
	root := newEnv(nil)
	rebinder := newCallFunctionRebinder(script, root, map[string]*ClassDef{}, map[string]*EnumDef{})

	slot := make([]Value, 1)
	receiver := NewArray(slot)
	probe, ok := universalMember(receiver, "equal?")
	if !ok {
		t.Fatal("universalMember did not resolve equal? for an array")
	}
	slot[0] = probe

	rebound := rebinder.rebindValue(NewArray([]Value{probe, receiver}))
	items := rebound.Array()
	reboundProbe := items[0]
	reboundReceiver := items[1]

	reachedProbe := reboundReceiver.Array()[0]
	if !reboundProbe.Identical(reachedProbe) {
		t.Fatal("the predicate rebound directly and the one reached through its own receiver are distinct builtins; the recursive rebind must reuse the reserved clone")
	}

	reboundBuiltin := valueBuiltin(reboundProbe)
	if reboundBuiltin == nil {
		t.Fatal("rebound equal? did not resolve to a builtin")
	}
	got, err := reboundBuiltin.Fn(nil, NewNil(), []Value{reboundReceiver}, nil, NewNil())
	if err != nil {
		t.Fatalf("rebound equal? against rebound receiver: %v", err)
	}
	if !got.Bool() {
		t.Fatal("rebound equal? returned false against the rebound receiver; the recursive rebind did not rebind")
	}
}

// TestCallRebindBoundPredicatePreservesAliasIdentity is the inbound counterpart
// to TestHostCloneBoundPredicatePreservesAliasIdentity: Script.Call rebinds
// incoming arguments through callFunctionRebinder, which must likewise memoize a
// bound predicate so the same builtin reached through two paths rebinds to a
// single *Builtin and the aliases stay equal? to each other.
func TestCallRebindBoundPredicatePreservesAliasIdentity(t *testing.T) {
	t.Parallel()

	script := compileScript(t, "def run()\n  nil\nend")
	root := newEnv(nil)
	rebinder := newCallFunctionRebinder(script, root, map[string]*ClassDef{}, map[string]*EnumDef{})

	receiver := NewHash(map[string]Value{"a": NewInt(1)})
	probe, ok := universalMember(receiver, "equal?")
	if !ok {
		t.Fatal("universalMember did not resolve equal? for a hash")
	}

	rebound := rebinder.rebindValue(NewArray([]Value{receiver, probe, probe}))
	items := rebound.Array()
	reboundReceiver := items[0]
	probeA := valueBuiltin(items[1])
	probeB := valueBuiltin(items[2])
	if probeA == nil || probeB == nil {
		t.Fatal("rebound equal? aliases did not resolve to builtins")
	}

	if !items[1].Identical(items[2]) {
		t.Fatal("the same bound predicate rebound through two paths produced distinct builtins; aliases must stay equal?")
	}
	if probeA != probeB {
		t.Fatal("rebound predicate aliases hold distinct *Builtin pointers; the rebinder must memoize the bound builtin")
	}

	for i, b := range []*Builtin{probeA, probeB} {
		got, err := b.Fn(nil, NewNil(), []Value{reboundReceiver}, nil, NewNil())
		if err != nil {
			t.Fatalf("rebound equal? alias %d against rebound receiver: %v", i, err)
		}
		if !got.Bool() {
			t.Fatalf("rebound equal? alias %d returned false against the rebound receiver; it did not rebind", i)
		}
	}
}

// TestEqualPredicateAliasIdentityAcrossScriptCalls exercises alias preservation
// end to end through Script.Call: one call exports a receiver and the same bound
// equal? predicate twice, and a second call receives that host-cloned graph and
// compares the two predicate aliases with equal?. They must report identical
// even though Script.Call host-cloned and rebound the graph between the calls.
func TestEqualPredicateAliasIdentityAcrossScriptCalls(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def export_probe
  obj = {a: 1}
  probe = obj.equal?
  [probe, probe]
end

def check(pair)
  pair[0].equal?(pair[1])
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
		t.Fatalf("two aliases of one bound predicate reported %#v; they must stay equal? across the host boundary", result)
	}
}

// TestPlainBuiltinAliasIdentityAcrossScriptCalls is the plain-builtin counterpart
// to TestEqualPredicateAliasIdentityAcrossScriptCalls, exercising the finding's
// exact scenario end to end through Script.Call: one call binds a plain (non
// receiver-bound) builtin to a local and returns it through two array slots
// (`p = JSON.parse; [p, p]`), and a second call receives that host-cloned graph
// and compares the two aliases with equal?. cloneBuiltinForHost mints a fresh
// *Builtin per occurrence, so without memoizing the clone on the source builtin
// the two slots would cross the host boundary as distinct pointers; equal?
// compares builtins by backing pointer, so the aliases would wrongly report
// not-identical even though they were one callable inside the script.
func TestPlainBuiltinAliasIdentityAcrossScriptCalls(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def export_probe
  p = JSON.parse
  [p, p]
end

def check(pair)
  pair[0].equal?(pair[1])
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
		t.Fatalf("two aliases of one plain builtin reported %#v; they must stay equal? across the host boundary", result)
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

// TestEqualityPredicateObjectDataFieldNotShadowing confirms a KindObject that is
// an ordinary data object — the shape hosts and capabilities return — does not let
// a stored non-callable "eql?"/"equal?" field shadow the universal predicate.
// Member dispatch must resolve the universal identity predicate (so obj.equal?(obj)
// is true), while the stored field stays readable as data through index access.
// A callable stored under the same name (a module/capability method export) must
// still shadow, so namespace exports remain reachable.
func TestEqualityPredicateObjectDataFieldNotShadowing(t *testing.T) {
	t.Parallel()
	script := compileScript(t, "def run()\n  nil\nend")
	exec := newExecutionForCall(script, context.Background(), newEnv(nil), CallOptions{})

	dataField := NewInt(7)
	obj := NewObject(map[string]Value{"eql?": dataField, "equal?": dataField})

	for _, property := range []string{"eql?", "equal?"} {
		member, err := exec.resolveMember(obj, property, Position{}, false)
		if err != nil {
			t.Fatalf("resolveMember(%s) on data object: %v", property, err)
		}
		builtin := valueBuiltin(member)
		if builtin == nil {
			t.Fatalf("%s on a data object resolved to %v, want the universal predicate builtin; a data field must not shadow it", property, member.Kind())
		}
		got, err := builtin.Fn(exec, NewNil(), []Value{obj}, nil, NewNil())
		if err != nil {
			t.Fatalf("invoking resolved %s against the object: %v", property, err)
		}
		if !got.Bool() {
			t.Fatalf("obj.%s(obj) reported false; the universal identity predicate must answer for a data object", property)
		}
		// The field is still readable as data through index access.
		if stored, ok := obj.Hash()[property]; !ok || !stored.Equal(dataField) {
			t.Fatalf("data field %q no longer readable as data: got %v ok=%v", property, stored, ok)
		}
	}

	// A callable stored under the same name is a method export and must shadow.
	callable := NewAutoBuiltin("export.eql?", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
		return NewString("export eql"), nil
	})
	namespace := NewObject(map[string]Value{"eql?": callable})
	member, err := exec.resolveMember(namespace, "eql?", Position{}, false)
	if err != nil {
		t.Fatalf("resolveMember(eql?) on namespace object: %v", err)
	}
	if !member.Identical(callable) {
		t.Fatalf("a callable eql? export was not resolved as the stored member; module exports must remain reachable")
	}
}

// TestEqualityPredicateObjectDataFieldEndToEnd is the script-level counterpart to
// TestEqualityPredicateObjectDataFieldNotShadowing: a host hands a data object
// carrying eql?/equal? fields to a script, which must observe identity through
// member dispatch while still reading the fields as data through index access.
func TestEqualityPredicateObjectDataFieldEndToEnd(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run(obj)
  [obj.equal?(obj), obj.eql?(obj), obj["eql?"], obj["equal?"]]
end`)

	obj := NewObject(map[string]Value{"eql?": NewInt(1), "equal?": NewString("data")})
	result, err := script.Call(context.Background(), "run", []Value{obj}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	compareArrays(t, result, []Value{
		NewBool(true),
		NewBool(true),
		NewInt(1),
		NewString("data"),
	})
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
