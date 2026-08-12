package runtime

import (
	"context"
	"fmt"
	"testing"
)

// The drivers below accumulate their results into a Go local that no reachable
// root points at, so the memory checks that run inside their callbacks reach
// those results only through a registered output walk root (see
// memory_output.go). The two tests here pin the two directions that root has to
// get right, because the mechanisms that preceded it each got one right and the
// other wrong: pricing the output as scratch measured the peak correctly but
// double-charged a result the callback also stored somewhere reachable, while
// not pricing it at all avoided the double charge and lost the peak.
const (
	outputDriverEntries  = 40
	outputDriverPayload  = 2048
	outputDriverRetained = outputDriverEntries * outputDriverPayload
)

func outputDriverReceivers(t *testing.T) (Value, Value) {
	t.Helper()
	arr := make([]Value, outputDriverEntries)
	for i := range outputDriverEntries {
		arr[i] = NewInt(int64(i))
	}
	entries := make(map[string]Value, outputDriverEntries)
	for i := range outputDriverEntries {
		entries[fmt.Sprintf("k%03d", i)] = NewInt(int64(i))
	}
	return NewArray(arr), NewHash(entries)
}

// A callback that keeps its own result -- memoizing it into a reachable
// container and returning it -- hands the same bytes to two views: the driver's
// Go-local output and the reachable graph. A walk root deduplicates them on
// identity, so they are charged once.
//
// Reserving the output as scratch could not: the reservation reaches a check
// through estimateScalarBase as a plain byte count while the graph walk reaches
// the callback's own copy, and nothing relates the two. That charged this shape
// about twice its real cost, and the quota below sits in the gap -- comfortably
// above what the retained bytes actually need and comfortably below twice it, so
// a driver that double-charges rejects a script whose live memory always fit.
func TestMemoizingCallbackIsNotChargedTwice(t *testing.T) {
	t.Parallel()

	arrayReceiver, hashReceiver := outputDriverReceivers(t)
	const quota = 130_000

	body := fmt.Sprintf(`s = "x" * %d; memo.push(s); s`, outputDriverPayload)
	tests := []struct {
		name     string
		src      string
		receiver Value
	}{
		{"array.map", fmt.Sprintf("def run(src)\n  memo = []\n  src.map { |i| %s }\nend", body), arrayReceiver},
		{"hash.map", fmt.Sprintf("def run(h)\n  memo = []\n  h.map { |k, v| %s }\nend", body), hashReceiver},
		{"hash.map_with_index", fmt.Sprintf("def run(h)\n  memo = []\n  h.map_with_index { |p, i| %s }\nend", body), hashReceiver},
		{"hash.transform_values", fmt.Sprintf("def run(h)\n  memo = []\n  h.transform_values { |v| %s }\nend", body), hashReceiver},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: quota}, tc.src)
			if _, err := script.Call(context.Background(), "run", []Value{tc.receiver}, CallOptions{}); err != nil {
				t.Fatalf("%s rejected a build whose %d retained bytes fit the %d-byte quota: %v; "+
					"a result the callback both returned and stored is being charged once per view "+
					"instead of once per identity", tc.name, outputDriverRetained, quota, err)
			}
		})
	}
}

// The other direction. A callback that allocates a large transient while the
// driver already holds a long output is live for both at once, so the quota has
// to see their sum. Without a registered root the in-block checks measured a
// graph the output was missing from, and the transient and the output each fit a
// quota their sum exceeds.
//
// The quota below sits in that gap: above either one alone, below their sum. It
// must be rejected.
func TestRetainedOutputIsWeighedAgainstInBlockTransients(t *testing.T) {
	t.Parallel()

	arrayReceiver, hashReceiver := outputDriverReceivers(t)
	const transient = 200_000
	const quota = 250_000

	if quota <= transient || quota <= outputDriverRetained {
		t.Fatalf("quota %d must exceed the transient %d and the retained output %d on their own",
			quota, transient, outputDriverRetained)
	}
	if quota >= transient+outputDriverRetained {
		t.Fatalf("quota %d must be below the %d-byte peak it is meant to reject",
			quota, transient+outputDriverRetained)
	}

	body := fmt.Sprintf(`big = "y" * %d; z = big.size; "x" * %d`, transient, outputDriverPayload)
	tests := []struct {
		name     string
		src      string
		receiver Value
	}{
		{"array.map", fmt.Sprintf("def run(src)\n  src.map { |i| %s }\nend", body), arrayReceiver},
		{"hash.map", fmt.Sprintf("def run(h)\n  h.map { |k, v| %s }\nend", body), hashReceiver},
		{"hash.map_with_index", fmt.Sprintf("def run(h)\n  h.map_with_index { |p, i| %s }\nend", body), hashReceiver},
		{"hash.transform_values", fmt.Sprintf("def run(h)\n  h.transform_values { |v| %s }\nend", body), hashReceiver},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: quota}, tc.src)
			_, err := script.Call(context.Background(), "run", []Value{tc.receiver}, CallOptions{})
			if err == nil {
				t.Fatalf("%s admitted a build whose %d retained bytes and %d-byte in-block transient "+
					"are live together, past its %d-byte quota; the checks inside the callback are "+
					"measuring a graph the retained output is missing from",
					tc.name, outputDriverRetained, transient, quota)
			}
			requireErrorContains(t, err, "memory quota exceeded")
		})
	}
}

// countingRestBindingBlock is the smallest block that both forces a bind charge
// (a rest-collecting destructure parameter is the only shape that does) and
// reports how many times it actually ran.
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

// Registering an output root makes a rest-binding block's bind charge walk the
// reachable graph, and that walk is charged to the step quota. It is recorded
// where it happens rather than charged there, because that site cannot return an
// error, so something has to settle the counter.
//
// Settling it in each driver's loop is not enough, and that is the point of this
// test. The counter is filled once, when the runner is constructed, before the
// first callback -- so a driver that only settles after each callback still lets
// the first one run, and a driver that forgets entirely lets the whole loop run
// on a quota the walk had already exhausted. Over an 80,000-node graph that was
// 12 callbacks and 1,298 steps against a quota of 100.
//
// callBlock settles instead, which is the one point every block invocation
// passes through: no callback can run before the walk that preceded it is paid
// for. With the graph far larger than the quota, that means no callback runs at
// all.
func TestConstructionWalkIsPaidBeforeAnyCallback(t *testing.T) {
	t.Parallel()

	const graphNodes = 20_000
	const stepQuota = 100
	const rows = 12

	graph := make([]Value, graphNodes)
	for i := range graphNodes {
		graph[i] = NewInt(int64(i))
	}

	arrayRows := make([]Value, rows)
	hashRows := make(map[string]Value, rows)
	for i := range rows {
		arrayRows[i] = NewArray([]Value{NewInt(int64(i)), NewInt(int64(i))})
		hashRows[string(rune('a'+i))] = NewInt(int64(i))
	}

	tests := []struct {
		name   string
		invoke func(t *testing.T, exec *Execution, block Value) error
	}{
		{"array.map", func(t *testing.T, exec *Execution, block Value) error {
			_, err := callArrayMember(t, exec, NewArray(arrayRows), "map", nil, block)
			return err
		}},
		{"hash.map", func(t *testing.T, exec *Execution, block Value) error {
			_, err := callHashMember(t, exec, NewHash(hashRows), "map", nil, block)
			return err
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			block := countingRestBindingBlock(&calls)
			exec := &Execution{ctx: context.Background(), quota: stepQuota, memoryQuota: 1 << 30}
			exec.root = newEnv(nil)
			exec.root.Define("graph", NewArray(graph))
			exec.envStack = append(exec.envStack, exec.root)

			err := tc.invoke(t, exec, block)
			if err == nil {
				t.Fatalf("%s completed under a %d-step quota while its bind charge walked a %d-node graph; "+
					"that walk costs about %d steps and must exhaust the quota",
					tc.name, stepQuota, graphNodes, graphNodes/estimatorNodesPerStep)
			}
			if calls != 0 {
				t.Fatalf("%s ran %d callbacks under a %d-step quota that its %d-step construction walk had "+
					"already exhausted; the walk is being settled after the callbacks it precedes rather "+
					"than before them", tc.name, calls, stepQuota, graphNodes/estimatorNodesPerStep)
			}
		})
	}
}
