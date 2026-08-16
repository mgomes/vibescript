package runtime

import (
	"context"
	"fmt"
	"testing"
)

// pushFibShapedFrame mimics the runtime's env-stack shape for one naive-recursion
// call: a fresh call frame parented to the root, holding a single compact-int
// binding and a nil call block, pushed twice (the call frame plus the
// evalStatements body scope share one env identity).
func pushFibShapedFrame(exec *Execution, n int64) *Env {
	frame := newEnv(exec.root)
	frame.setDynamic("n", NewInt(n))
	frame.setCallBlock(NewNil())
	exec.pushEnv(frame)
	exec.pushEnv(frame)
	return frame
}

// estimateGraphBaseChecked resets the shared estimator, runs the fast base walk
// (which reconciles the dormant prefix and, under the oracle, self-verifies
// against the reference), and returns the total.
func estimateGraphBaseChecked(exec *Execution) int {
	exec.memoryEst.reset()
	return exec.estimateGraphBaseFast(&exec.memoryEst)
}

func TestReconcileDormantCommitsScalarFrames(t *testing.T) {
	prev := estimatorVerify
	estimatorVerify = true
	defer func() { estimatorVerify = prev }()

	exec := &Execution{root: newEnv(nil), memoryQuota: 1 << 30}
	exec.envStack = exec.envStackArr[:0]

	const depth = 12
	// Grow the stack one call at a time, estimating at each depth exactly as the
	// interpreter would check memory on entry to each call.
	for k := range depth {
		pushFibShapedFrame(exec, int64(k))
		// The oracle inside estimateGraphBase panics on any fast/reference
		// divergence, so reaching here means the walk is byte-exact at this depth.
		estimateGraphBaseChecked(exec)
	}

	// With depth distinct frames on the stack, every frame below the executing
	// top pair is dormant: (depth-1) frames, two slots each.
	wantDormant := depth - 1
	if got := len(exec.dormant); got != wantDormant {
		t.Fatalf("committed dormant frames = %d, want %d", got, wantDormant)
	}
	if exec.dormantSlots != 2*wantDormant {
		t.Fatalf("dormantSlots = %d, want %d", exec.dormantSlots, 2*wantDormant)
	}
	// Each fib-shaped frame charges estimatedEnvBytes + len("n").
	wantBytes := wantDormant * (estimatedEnvBytes + len("n"))
	if exec.dormantBytes != wantBytes {
		t.Fatalf("dormantBytes = %d, want %d", exec.dormantBytes, wantBytes)
	}
}

func TestReconcileDormantRetractsOnPop(t *testing.T) {
	prev := estimatorVerify
	estimatorVerify = true
	defer func() { estimatorVerify = prev }()

	exec := &Execution{root: newEnv(nil), memoryQuota: 1 << 30}
	exec.envStack = exec.envStackArr[:0]

	pushFibShapedFrame(exec, 0)
	pushFibShapedFrame(exec, 1)
	pushFibShapedFrame(exec, 2)
	estimateGraphBaseChecked(exec)
	if len(exec.dormant) != 2 {
		t.Fatalf("expected 2 dormant frames before pop, got %d", len(exec.dormant))
	}
	committedBytes := exec.dormantBytes

	// Pop the top call's two slots; the frame beneath resumes and must be
	// retracted from the committed prefix.
	exec.popEnv()
	exec.popEnv()
	estimateGraphBaseChecked(exec)
	if len(exec.dormant) != 1 {
		t.Fatalf("expected 1 dormant frame after pop, got %d", len(exec.dormant))
	}
	if exec.dormantBytes >= committedBytes {
		t.Fatalf("dormantBytes did not shrink after pop: %d (was %d)", exec.dormantBytes, committedBytes)
	}
}

func TestReconcileDormantSkipsNonScalarFrame(t *testing.T) {
	prev := estimatorVerify
	estimatorVerify = true
	defer func() { estimatorVerify = prev }()

	exec := &Execution{root: newEnv(nil), memoryQuota: 1 << 30}
	exec.envStack = exec.envStackArr[:0]

	// A committable scalar frame at the bottom.
	pushFibShapedFrame(exec, 0)
	// A frame holding a mutable array: its footprint can grow through a shared
	// reference, so it must never be committed as dormant.
	arrFrame := newEnv(exec.root)
	arrFrame.setDynamic("xs", NewArray([]Value{NewInt(1), NewInt(2)}))
	exec.pushEnv(arrFrame)
	exec.pushEnv(arrFrame)
	// A third scalar frame on top so both lower frames are below the active pair.
	pushFibShapedFrame(exec, 2)

	estimateGraphBaseChecked(exec)

	// Only the bottom scalar frame commits; the array frame stops the prefix, so
	// it and everything above stay in the walked (active) region.
	if len(exec.dormant) != 1 {
		t.Fatalf("expected exactly the bottom scalar frame committed, got %d", len(exec.dormant))
	}
	if exec.dormant[0].env != exec.envStack[0] {
		t.Fatalf("committed frame is not the bottom scalar frame")
	}
}

// dormantRebindPayload is the byte count of each string the rebinding fixtures
// allocate. Two of them are live at once, so a walk that trusts a stale dormant
// total charges roughly half of what the reference walk charges.
const dormantRebindPayload = 400_000

// dormantRebindSource builds a program where a dormant frame is rebound from a
// scope the estimator's fast path must not skip. `outer` holds one compact-Int
// local, which is exactly the shape that qualifies its frame as dormant, and
// hands `sink` a closure over it. `sink` runs that closure through reentry, so
// the rebind happens under whichever construct reentry names, then allocates a
// payload of its own. Both payloads are live at `hog.size`, so a walk that still
// charges `outer` its committed scalar-only total sees only one of them.
func dormantRebindSource(reentry string) string {
	payload := fmt.Sprint(dormantRebindPayload)
	return `
def sink(cb)
` + reentry + `
  hog = "B" * ` + payload + `
  hog.size
end

def outer()
  small = 0
  sink(-> { small = "A" * ` + payload + ` })
  small.size
end
`
}

// callDormantRebind compiles and runs a dormantRebindSource program under quota.
func callDormantRebind(t *testing.T, src string, quota int) (Value, error) {
	t.Helper()
	engine := MustNewEngine(Config{StepQuota: Unlimited, MemoryQuotaBytes: quota, RecursionLimit: 10_000})
	script, err := engine.CompileSnippet(src, "__main__")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return script.Call(context.Background(), "outer", nil, CallOptions{})
}

// requireDormantRebindCharged asserts that both payloads are charged at once: a
// quota with room for one of them plus the surrounding graph must reject the
// program, while a quota with room for both must run it to completion. Only the
// pair proves the accounting rather than a program that cannot run at all.
func requireDormantRebindCharged(t *testing.T, src string) {
	t.Helper()
	const oneFits = dormantRebindPayload + 200_000
	if _, err := callDormantRebind(t, src, oneFits); err == nil {
		t.Fatalf("quota of %d bytes admitted two live %d-byte payloads", oneFits, dormantRebindPayload)
	} else {
		requireRuntimeErrorType(t, err, runtimeErrorTypeLimit)
	}
	got, err := callDormantRebind(t, src, 4*dormantRebindPayload)
	if err != nil {
		t.Fatalf("generous quota rejected the program: %v", err)
	}
	if got.Kind() != KindInt || got.Int() != dormantRebindPayload {
		t.Fatalf("outer() = %v, want %d", got, dormantRebindPayload)
	}
}

// TestDormantPrefixRetractsOnBuiltinBlockRebind covers #20: a builtin invoking a
// script block runs every check on beginBaseWalk's bypass, which walks the
// reference estimate and never reaches the retraction envStackGraphBytes does. A
// block that rebound a dormant caller's Int to a 400KB string left the caller
// committed at 245 bytes, so the next memoized check in the caller charged 4030
// bytes where the reference walk charged 404046 and the program ran to
// completion under a 404638-byte quota instead of the 804361 it now needs.
func TestDormantPrefixRetractsOnBuiltinBlockRebind(t *testing.T) {
	t.Parallel()
	requireDormantRebindCharged(t, dormantRebindSource("  cb.call"))
}

// TestDormantPrefixRetractsOnBlockRegionRebind covers #19: a block-iteration
// region skipped the non-base-parent bookkeeping for every scope it pushed, so a
// pure iterator running a block closed on a dormant caller never retracted that
// caller's committed total. Rebinding the caller's Int to a 400KB string through
// array.each left the program running under a 404,951-byte quota while holding
// two live 400KB strings; it now needs 804,361.
func TestDormantPrefixRetractsOnBlockRegionRebind(t *testing.T) {
	t.Parallel()
	requireDormantRebindCharged(t, dormantRebindSource("  [1].each do |i|\n    cb.call\n  end"))
}

// TestDormantEstimatorFibUnderQuota runs naive fib through the real interpreter
// under a finite memory quota so the estimator walks on every call. Under the
// oracle (VIBES_ESTIMATOR_VERIFY=1, set by TestMain) each walk self-verifies
// against the reference, so a correct result here is also proof the dormant fast
// path is byte-exact along the real recursion.
func TestDormantEstimatorFibUnderQuota(t *testing.T) {
	engine := MustNewEngine(Config{StepQuota: Unlimited, MemoryQuotaBytes: 5_000_000, RecursionLimit: 10_000})
	script, err := engine.CompileSnippet(`
def fib(n)
  if n < 2
    n
  else
    fib(n - 1) + fib(n - 2)
  end
end

fib(20)
`, "__main__")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got, err := script.Call(context.Background(), "fib", []Value{NewInt(20)}, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.Kind() != KindInt || got.Int() != 6765 {
		t.Fatalf("fib(20) = %v, want 6765", got)
	}
}
