package value

import (
	"math/big"
	"testing"
)

// TestEqualityContextDropsOperandRootsAfterCompare pins that a comparison
// does not outlive its answer inside the context: the runtime pools one
// context per execution and embeds others in set helpers, so retained
// rootLeft/rootRight would keep the compared graphs — possibly large
// host-returned temporaries invisible to the memory estimator — reachable
// until the next comparison runs.
func TestEqualityContextDropsOperandRootsAfterCompare(t *testing.T) {
	t.Parallel()

	var ctx EqualityContext
	ctx.SetCharge(func(int) error { return nil })
	left := NewArray([]Value{NewString("payload")})
	right := NewArray([]Value{NewString("payload")})
	if !ctx.Equal(left, right) {
		t.Fatal("arrays must compare equal")
	}
	if ctx.state.rootLeft.Kind() != KindNil || ctx.state.rootRight.Kind() != KindNil {
		t.Fatal("Equal retained the compared operand graphs after answering")
	}
	if !ctx.Eql(left, right) {
		t.Fatal("arrays must be eql")
	}
	if ctx.state.rootLeft.Kind() != KindNil || ctx.state.rootRight.Kind() != KindNil {
		t.Fatal("Eql retained the compared operand graphs after answering")
	}
}

// TestEqualityContextDropsOversizedTraversalMap pins the pooling bound on the
// cycle-detection map: clearing it between comparisons keeps its buckets, so
// a walk that visited many distinct composite pairs must not leave its whole
// backing on a context that outlives the comparison, while small walks keep
// their map for reuse.
func TestEqualityContextDropsOversizedTraversalMap(t *testing.T) {
	t.Parallel()

	build := func(n int) Value {
		elems := make([]Value, n)
		for i := range elems {
			elems[i] = NewArray([]Value{NewInt(int64(i))})
		}
		return NewArray(elems)
	}

	var ctx EqualityContext
	if !ctx.Equal(build(3), build(3)) {
		t.Fatal("small arrays must compare equal")
	}
	if ctx.state.seen == nil {
		t.Fatal("a small walk must keep its traversal map for reuse")
	}
	if !ctx.Equal(build(200), build(200)) {
		t.Fatal("large arrays must compare equal")
	}
	if ctx.state.seen != nil {
		t.Fatal("a walk past the retain threshold must drop the traversal map")
	}
}

// TestInspectSizingUsesDecimalBoundForBigInts pins that display sizing never
// materializes a big integer's decimal form: the projection must be the
// allocation-free upper bound, since the sizing pass runs before any caller
// reservation covers the rendering it projects.
func TestInspectSizingUsesDecimalBoundForBigInts(t *testing.T) {
	t.Parallel()

	bi := new(big.Int).Lsh(big.NewInt(1), 1<<16)
	n, err := NewBigInt(bi).inspectByteLenBoundedWithState(newValueStringState(), func() error { return nil })
	if err != nil {
		t.Fatalf("sizing failed: %v", err)
	}
	if want := bigIntDecimalLenUpperBound(bi); n != want {
		t.Fatalf("sizing = %d, want the allocation-free bound %d", n, want)
	}
	small := big.NewInt(-1234567890123456789)
	sized, err := NewBigInt(small).inspectByteLenBoundedWithState(newValueStringState(), func() error { return nil })
	if err != nil {
		t.Fatalf("sizing failed: %v", err)
	}
	if actual := len(small.String()); sized < actual {
		t.Fatalf("bound %d falls short of the actual rendering %d", sized, actual)
	}
}
