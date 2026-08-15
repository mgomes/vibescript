package runtime

import (
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

	const want = 144
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

// Every path that rebuilds a Builtin around an existing one either copies the
// promises deliberately or drops them, and dropping them is the safe direction:
// the rebuilt builtin falls back to the conservative behaviour. cloneBuiltinValue
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
