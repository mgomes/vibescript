package runtime

import (
	"strings"
	"testing"
	"unsafe"
)

// estimatedBuiltinBytes is unsafe.Sizeof(Builtin{}), so every field added to
// the struct is a change to how much memory a reachable builtin is charged,
// and golden expectations across the memory suite move with it. The contract
// flags were chosen to fit the padding the struct already carried; this pins
// that they still do, so a later field that grows the struct has to be a
// deliberate accounting change rather than a silent one.
func TestBuiltinStructSizeIsPinned(t *testing.T) {
	t.Parallel()

	const want = 120
	if got := unsafe.Sizeof(Builtin{}); got != uintptr(want) {
		t.Errorf("unsafe.Sizeof(Builtin{}) = %d, want %d; this feeds estimatedBuiltinBytes, "+
			"so changing it reprices every reachable builtin and moves memory golden "+
			"numbers across the suite", got, want)
	}
}

// Both promises default to the conservative answer, and they are independent:
// nothing about a Builtin should let one of them imply the other.
func TestBuiltinContractDefaultsAreConservative(t *testing.T) {
	t.Parallel()

	plain := valueBuiltin(NewBuiltin("plain", nil))
	if plain.declaredNonMutating() {
		t.Error("an undeclared builtin reported itself non-mutating; the default must be conservative")
	}
	if plain.declaredNonRetaining() {
		t.Error("an undeclared builtin reported itself non-retaining; the default must be conservative")
	}

	mutating := valueBuiltin(DeclareNonRetaining(NewBuiltin("kept", nil)))
	if mutating.declaredNonMutating() {
		t.Error("declaring non-retaining also declared non-mutating; the promises are independent")
	}
	retaining := valueBuiltin(DeclareNonMutating(NewBuiltin("pure", nil)))
	if retaining.declaredNonRetaining() {
		t.Error("declaring non-mutating also declared non-retaining; the promises are independent")
	}

	var zero Builtin
	if zero.declaredNonMutating() || zero.declaredNonRetaining() {
		t.Error("the zero Builtin declared a promise; a Builtin built without a constructor must be conservative")
	}
	var nilBuiltin *Builtin
	if nilBuiltin.declaredNonMutating() || nilBuiltin.declaredNonRetaining() {
		t.Error("a nil Builtin declared a promise")
	}
}

// The declared names must reach the builtins that dispatch actually invokes,
// not merely sit in a list: the table wraps every member in a receiver guard
// after building it, and a declaration applied to the wrong value would be a
// silent no-op that reads as a working feature.
func TestDeclaredMembersDispatchAsNonMutating(t *testing.T) {
	t.Parallel()

	tables := []struct {
		kind    string
		table   *memberTable
		build   func(string) (Value, error)
		names   []string
		mutator []string
	}{
		{
			kind:    "int",
			table:   intBuiltinMembers,
			build:   intMemberBuiltin,
			names:   append(append([]string{}, pureNumericMemberNames...), "even?", "odd?"),
			mutator: []string{"times", "upto", "step"},
		},
		{
			kind:    "string",
			table:   stringBuiltinMembers,
			build:   stringMemberBuiltin,
			names:   pureStringMemberNames,
			mutator: []string{"upcase!", "strip!", "clear", "concat", "replace", "insert"},
		},
		{
			kind:    "array",
			table:   arrayBuiltinMembers,
			build:   arrayMemberBuiltin,
			names:   pureArrayMemberNames,
			mutator: []string{"push", "pop", "shift", "clear", "insert", "fill", "map!", "sort!", "map", "each"},
		},
	}

	for _, tc := range tables {
		for _, name := range tc.names {
			member, ok := tc.table.lookup(name, tc.build)
			if !ok {
				t.Errorf("%s.%s is declared non-mutating but does not resolve", tc.kind, name)
				continue
			}
			if !BuiltinOf(member).declaredNonMutating() {
				t.Errorf("%s.%s is in the declared list but dispatches undeclared; the "+
					"declaration did not reach the builtin dispatch invokes", tc.kind, name)
			}
		}
		// The other direction, which is the one that matters for soundness: a
		// member that writes to its receiver or drives user code must not have
		// picked the promise up.
		for _, name := range tc.mutator {
			member, ok := tc.table.lookup(name, tc.build)
			if !ok {
				continue
			}
			if BuiltinOf(member).declaredNonMutating() {
				t.Errorf("%s.%s declares non-mutating; a member that writes to its "+
					"receiver or drives user code must stay conservative", tc.kind, name)
			}
		}
	}
}

// The bang guard is a gate, so it has to be shown rejecting something. Without
// this the guard could be spelled wrong and every declaration would still look
// fine, because nothing in the curated lists ends in "!" anyway.
func TestDeclaringNonMutatingRejectsBangMembers(t *testing.T) {
	t.Parallel()

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("declaring a bang member non-mutating was accepted; the guard that keeps " +
				"in-place mutators out of the contract does not fire")
		}
		msg, ok := recovered.(string)
		if !ok || !strings.Contains(msg, "in-place mutator") {
			t.Fatalf("panicked with %v, want a message naming the in-place mutator convention", recovered)
		}
	}()
	newTypedMemberTable([]string{"upcase!"}, KindString).declaringNonMutating("upcase!")
}

// Every path that rebuilds a Builtin around an existing one either copies the
// promises deliberately or drops them, and dropping them is the safe direction:
// the rebuilt builtin falls back to the conservative behavior. cloneBuiltinValue
// is an exhaustive field-by-field copy, so it is the one that has to be kept in
// step on purpose; this pins its choice either way rather than leaving it to a
// reader to notice.
func TestClonedBuiltinKeepsItsContract(t *testing.T) {
	t.Parallel()

	declared := DeclareNonRetaining(DeclareNonMutating(NewBuiltin("declared", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
		return NewNil(), nil
	})))
	cloned := valueBuiltin(cloneBuiltinValue(declared))
	if !cloned.declaredNonMutating() || !cloned.declaredNonRetaining() {
		t.Errorf("cloneBuiltinValue dropped a declared contract (nonMutating=%v nonRetaining=%v); "+
			"per-call cloning must not silently reclassify a builtin",
			cloned.declaredNonMutating(), cloned.declaredNonRetaining())
	}

	plain := NewBuiltin("plain", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
		return NewNil(), nil
	})
	clonedPlain := valueBuiltin(cloneBuiltinValue(plain))
	if clonedPlain.declaredNonMutating() || clonedPlain.declaredNonRetaining() {
		t.Error("cloneBuiltinValue invented a contract for an undeclared builtin")
	}
}
