package runtime

import (
	"context"
	"testing"
)

// restBindingParam is the only block parameter shape that costs a bind charge,
// and therefore the only one whose runner records a graph walk while a driver
// output is registered.
func restBindingParam() Param {
	pos := Position{Line: 1, Column: 1}
	return Param{Kind: ParamNormal, Target: &DestructureTarget{
		Position: pos,
		Elements: []DestructureElement{
			{Target: &Identifier{Name: "head", Position: pos}},
			{Target: &Identifier{Name: "tail", Position: pos}, Rest: true},
		},
	}}
}

// emptyNestedDriverBlock returns a block that runs a real nested driver over an
// empty receiver. array.each builds its runner before it discovers there is
// nothing to iterate, so the runner's bind charge is recorded and the block it
// was built for is never invoked.
//
// Its own parameter is plain, so this block costs no charge: the only recorded
// walk in the test is the nested driver's.
func emptyNestedDriverBlock(nestedRan *int) Value {
	pos := Position{Line: 1, Column: 1}
	nested := NewBuiltin("test.nested_each", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		*nestedRan++
		empty := NewArray(nil)
		member, err := arrayMember(empty, "each")
		if err != nil {
			return NewNil(), err
		}
		return valueBuiltin(member).Fn(exec, empty, nil, nil,
			NewBlock([]Param{restBindingParam()}, nil, newEnv(nil)))
	})
	env := newEnv(nil)
	env.Define("__nested__", nested)
	body := []Statement{&ExprStmt{Position: pos, Expr: &CallExpr{
		Position: pos,
		Callee:   &Identifier{Name: "__nested__", Position: pos},
	}}}
	return NewBlock([]Param{{Kind: ParamNormal, Name: "k"}}, body, env)
}

// A charge recorded by a driver that never invokes its block has no callback to
// settle it, so settling only at callBlock leaves it pending indefinitely while
// the enclosing loop carries on.
//
// That is not hypothetical arithmetic: hash.fetch_values processes a present key
// without invoking anything, and a present key can cost no steps at all, so the
// lookup runs its whole remaining key list against a quota the pending charge
// had already exhausted. Settling when the runner is built closes it, because
// that is where the charge is recorded.
//
// The assertion is differential -- the same lookup with and without a long run
// of present keys -- so it measures the thing that matters (does the loop keep
// going?) rather than a base cost that changes for unrelated reasons.
func TestNonInvokingNestedDriverStopsTheEnclosingLoop(t *testing.T) {
	t.Parallel()

	const graphNodes = 40_000
	const presentKeys = 50_000
	const stepQuota = 100

	graph := make([]Value, graphNodes)
	for i := range graphNodes {
		graph[i] = NewInt(int64(i))
	}

	// The receiver is fixed at its full size for both runs. Only the number of
	// keys LOOKED UP varies. Sizing the receiver with the lookup instead would
	// change what every base walk costs, and the growth that produced would look
	// exactly like the loop running on -- a difference that has nothing to do
	// with the defect.
	key := func(i int) string {
		return "k" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) +
			string(rune('a'+(i/676)%26)) + string(rune('a'+(i/17576)%26))
	}
	entries := make(map[string]Value, presentKeys)
	for i := range presentKeys {
		entries[key(i)] = NewInt(int64(i))
	}

	// walkedForKeys runs the lookup with n present keys following one miss, and
	// reports the estimator work the lookup performed.
	walkedForKeys := func(t *testing.T, n int) (int, int, error) {
		t.Helper()
		args := make([]Value, 0, n+1)
		args = append(args, NewString("zzz_missing"))
		for i := range n {
			args = append(args, NewString(key(i)))
		}

		nestedRan := 0
		block := emptyNestedDriverBlock(&nestedRan)
		exec := &Execution{ctx: context.Background(), quota: stepQuota, memoryQuota: 1 << 30}
		exec.root = newEnv(nil)
		exec.root.Define("graph", NewArray(graph))
		exec.envStack = append(exec.envStack, exec.root)

		before := exec.memoryEst.walked
		_, err := callHashMember(t, exec, NewHash(entries), "fetch_values", args, block)
		return exec.memoryEst.walked - before, nestedRan, err
	}

	withoutKeys, ranA, errA := walkedForKeys(t, 0)
	withKeys, ranB, errB := walkedForKeys(t, presentKeys)

	if ranA != 1 || ranB != 1 {
		t.Fatalf("the nested driver ran %d and %d times, want 1 each; the test is not exercising it", ranA, ranB)
	}
	if errA == nil || errB == nil {
		t.Fatalf("the lookup completed under a %d-step quota whose construction walk alone exceeds it "+
			"(errors: %v, %v)", stepQuota, errA, errB)
	}

	// The present keys invoke nothing, so they must not be reached once the
	// nested driver's charge has exhausted the quota.
	if growth := withKeys - withoutKeys; growth > presentKeys/10 {
		t.Fatalf("adding %d present keys after the miss grew the lookup's estimator work by %d nodes "+
			"(%d -> %d); the charge recorded by a nested driver that never invoked its block is still "+
			"pending, so the lookup kept processing keys against an exhausted quota",
			presentKeys, growth, withoutKeys, withKeys)
	}
}
