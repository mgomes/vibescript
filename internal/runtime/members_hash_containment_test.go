package runtime

import (
	"context"
	"errors"
	"strconv"
	"testing"
)

// largeHashReceiver builds a hash with count string-keyed integer entries for
// the containment tests. The entries live only in the returned value, so a bare
// Execution can drive a transform builtin against it without the receiver
// counting toward the base memory estimate.
func largeHashReceiver(count int) Value {
	entries := make(map[string]Value, count)
	for i := range count {
		entries["k"+strconv.Itoa(i)] = NewInt(int64(i))
	}
	return NewHash(entries)
}

// callHashMember resolves a hash member builtin and invokes it directly so the
// containment tests can supply a controlled Execution. The builtins are pure
// functions of (exec, receiver, args, kwargs, block), mirroring how the
// interpreter dispatches them.
func callHashMember(t *testing.T, exec *Execution, receiver Value, name string, args []Value, block Value) (Value, error) {
	t.Helper()
	member, err := hashMember(receiver, name)
	if err != nil {
		t.Fatalf("hashMember(%s): %v", name, err)
	}
	builtin := valueBuiltin(member)
	if builtin == nil {
		t.Fatalf("hash member %s is not a builtin", name)
	}
	return builtin.Fn(exec, receiver, args, nil, block)
}

func TestHashBlocklessTransformTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	// Each blockless transform projects the output map it would materialize and
	// rejects the call before reserving a backing map that dwarfs the quota. The
	// receiver holds far more entries than the tiny quota can hold, so the
	// up-front projected check fails rather than the statement-level check
	// catching the allocation after the fact.
	const count = 100_000
	const quota = 8 * 1024

	tests := []struct {
		name string
		args []Value
	}{
		{name: "compact"},
		{name: "except"},
		{name: "merge", args: []Value{largeHashReceiver(count)}},
		{name: "replace", args: []Value{largeHashReceiver(count)}},
		{name: "store", args: []Value{NewSymbol("extra"), NewInt(1)}},
		{name: "remap_keys", args: []Value{NewHash(map[string]Value{"k0": NewSymbol("renamed")})}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			receiver := largeHashReceiver(count)
			exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
			_, err := callHashMember(t, exec, receiver, tc.name, tc.args, NewNil())
			requireErrorIs(t, err, errMemoryQuotaExceeded)
		})
	}
}

func TestHashBlocklessTransformHonorsStepQuota(t *testing.T) {
	t.Parallel()

	// Walking a large hash charges a step per entry so a transform stops on the
	// step limit even when the memory quota is ample enough for the result.
	const count = 5_000
	const stepQuota = 100

	tests := []struct {
		name string
		args []Value
	}{
		{name: "compact"},
		{name: "except"},
		{name: "merge", args: []Value{largeHashReceiver(count)}},
		{name: "slice", args: hashSymbolKeys(count)},
		{name: "remap_keys", args: []Value{NewHash(map[string]Value{})}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			receiver := largeHashReceiver(count)
			exec := &Execution{ctx: context.Background(), quota: stepQuota, memoryQuota: 64 << 20}
			_, err := callHashMember(t, exec, receiver, tc.name, tc.args, NewNil())
			requireErrorIs(t, err, errStepQuotaExceeded)
		})
	}
}

func TestHashBlocklessTransformHonorsCancellation(t *testing.T) {
	t.Parallel()

	// step() polls cancellation on its first invocation, so a canceled context
	// aborts the walk before any entries are copied. A small receiver is enough.
	receiver := largeHashReceiver(8)

	tests := []struct {
		name string
		args []Value
	}{
		{name: "compact"},
		{name: "except"},
		{name: "merge", args: []Value{largeHashReceiver(8)}},
		{name: "slice", args: hashSymbolKeys(8)},
		{name: "remap_keys", args: []Value{NewHash(map[string]Value{})}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			exec := &Execution{ctx: ctx, quota: 1 << 30, memoryQuota: 0}
			_, err := callHashMember(t, exec, receiver, tc.name, tc.args, NewNil())
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s under canceled context = %v, want context.Canceled", tc.name, err)
			}
		})
	}
}

// hashSymbolKeys returns count symbol keys matching the entries built by
// largeHashReceiver, for driving slice with many candidate keys.
func hashSymbolKeys(count int) []Value {
	keys := make([]Value, count)
	for i := range count {
		keys[i] = NewSymbol("k" + strconv.Itoa(i))
	}
	return keys
}

func TestHashBlockTransformTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	// Block-based transforms preflight the output map before reserving it, so a
	// large receiver trips the quota before the first block call. Building the
	// receiver in-script keeps the receiver out of the call arguments while the
	// transform's projected check observes its size.
	const count = 50_000
	source := `def run(n)
  h = {}
  for i in 1..n
    h["k" + i] = i
  end
  h.transform_values { |v| v }
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 30, MemoryQuotaBytes: 256 * 1024}, source)
	requireRunMemoryQuotaError(t, script, []Value{NewInt(count)}, CallOptions{})
}

func TestHashBlockTransformHonorsStepQuota(t *testing.T) {
	t.Parallel()

	// Each block invocation charges a step, so a block-based transform over a
	// large hash stops on the step limit even with ample memory. The receiver is
	// passed in so the tight step quota trips inside the transform's block walk
	// rather than while building the hash.
	source := `def run(values)
  values.transform_values { |v| v }
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 40, MemoryQuotaBytes: 64 << 20}, source)
	requireCallRuntimeErrorType(t, script, "run", []Value{largeHashReceiver(2_000)}, CallOptions{}, runtimeErrorTypeLimit)
}

func TestHashBlockTransformHonorsCancellation(t *testing.T) {
	t.Parallel()

	// A canceled context aborts the block walk: step() polls cancellation on its
	// first invocation, so even a tiny hash observes it.
	source := `def run(values)
  values.select { |k, v| v > 0 }
end`
	script := compileScript(t, source)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	receiver := NewHash(map[string]Value{"a": NewInt(1), "b": NewInt(2), "c": NewInt(3)})
	_, err := script.Call(ctx, "run", []Value{receiver}, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("select under canceled context = %v, want context.Canceled", err)
	}
}
