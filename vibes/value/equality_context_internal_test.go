package value

import "testing"

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
