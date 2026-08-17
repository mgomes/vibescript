package runtime

import (
	"context"
	"testing"
)

// countingRestBindingBlock is the smallest block that both forces a bind charge
// -- a rest-collecting destructure parameter is the only binding shape that does
// -- and reports how many times it actually ran.
func countingRestBindingBlock(calls *int) Value {
	pos := Position{Line: 1, Column: 1}
	probe := NewBuiltin("test.callback_probe", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		*calls++
		return NewNil(), nil
	})
	env := newEnv(nil)
	env.Define("__probe__", probe)
	target := &DestructureTarget{
		Position: pos,
		Elements: []DestructureElement{
			{Target: &Identifier{Name: "head", Position: pos}},
			{Target: &Identifier{Name: "tail", Position: pos}, Rest: true},
		},
	}
	body := []Statement{&ExprStmt{Position: pos, Expr: &CallExpr{
		Position: pos,
		Callee:   &Identifier{Name: "__probe__", Position: pos},
	}}}
	return NewBlock([]Param{{Kind: ParamNormal, Target: target}}, body, env)
}

// While a driver output is registered, building a rest-binding block's bind
// charge walks the reachable graph, and that walk is billed to the step quota.
// It is recorded where it happens rather than charged there, because that site
// cannot return an error, so something has to settle the counter.
//
// Settling it in the driver's loop cannot be right, and that is what this pins.
// The counter is filled once, when the runner is built, which is before the
// first callback -- so a driver settling after each callback still lets that
// first one run on a quota the walk had already spent. callBlock settles
// instead, being the one point every block invocation passes through, so no
// callback can run before the walk preceding it is paid for.
//
// With the graph far larger than the quota, that means no callback runs at all.
// Before this, exactly one did.
func TestConstructionWalkIsPaidBeforeAnyCallback(t *testing.T) {
	t.Parallel()

	const graphNodes = 40_000
	const stepQuota = 100
	const keyCount = 12

	graph := make([]Value, graphNodes)
	for i := range graphNodes {
		graph[i] = NewInt(int64(i))
	}
	// Every key misses, so each one would invoke the block.
	keys := make([]Value, keyCount)
	for i := range keyCount {
		keys[i] = NewString(string(rune('a' + i)))
	}

	tests := []struct {
		method  string
		prepare func(block Value) (Value, Value)
	}{
		{"fetch_values", func(block Value) (Value, Value) {
			return NewHash(map[string]Value{}), block
		}},
	}

	for _, tc := range tests {
		method := tc.method
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			calls := 0
			receiver, block := tc.prepare(countingRestBindingBlock(&calls))
			exec := &Execution{ctx: context.Background(), quota: stepQuota, memoryQuota: 1 << 30}
			exec.root = newEnv(nil)
			exec.root.Define("graph", NewArray(graph))
			exec.envStack = append(exec.envStack, exec.root)

			_, err := callHashMember(t, exec, receiver, method, keys, block)
			if err == nil {
				t.Fatalf("hash.%s completed under a %d-step quota while its bind charge walked a "+
					"%d-node graph; that walk costs about %d steps and must exhaust the quota",
					method, stepQuota, graphNodes, graphNodes/estimatorNodesPerStep)
			}
			if calls != 0 {
				t.Fatalf("hash.%s ran %d callbacks under a %d-step quota that its %d-step construction "+
					"walk had already exhausted; the walk is being settled after the callbacks it "+
					"precedes rather than before them",
					method, calls, stepQuota, graphNodes/estimatorNodesPerStep)
			}
		})
	}
}
