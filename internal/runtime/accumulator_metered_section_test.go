package runtime

import (
	"context"
	"testing"
	"time"
)

// TestAccumulatorMeteredSectionSkipsPeriodicWalk pins the core mechanic:
// step()'s periodic slow path runs the full memory walk outside a section,
// skips it while one is active (including while nested sections hold it open),
// and resumes it as soon as the outermost section ends.
func TestAccumulatorMeteredSectionSkipsPeriodicWalk(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1}
	if err := exec.checkMemory(); err == nil {
		t.Fatal("sanity: a 1-byte quota must fail a direct memory check")
	}

	exec.steps = 15
	requireErrorIs(t, exec.step(), errMemoryQuotaExceeded)

	end := exec.beginAccumulatorMeteredSection()
	exec.steps = 31
	if err := exec.step(); err != nil {
		t.Fatalf("periodic walk must be skipped inside a section, got %v", err)
	}

	endInner := exec.beginAccumulatorMeteredSection()
	exec.steps = 47
	if err := exec.step(); err != nil {
		t.Fatalf("periodic walk must stay skipped inside a nested section, got %v", err)
	}
	endInner()
	exec.steps = 63
	if err := exec.step(); err != nil {
		t.Fatalf("ending the inner section must not resume checks while the outer is active, got %v", err)
	}

	end()
	if exec.accumMeteredSections != 0 {
		t.Fatalf("section counter = %d after all sections ended, want 0", exec.accumMeteredSections)
	}
	exec.steps = 79
	requireErrorIs(t, exec.step(), errMemoryQuotaExceeded)
}

// TestAccumulatorMeteredSectionKeepsStepQuotaAndCancellation pins that a
// section only skips the periodic memory walk: the step quota still trips and
// a canceled context is still observed on the same periodic schedule.
func TestAccumulatorMeteredSectionKeepsStepQuotaAndCancellation(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), quota: 20, memoryQuota: 1}
	defer exec.beginAccumulatorMeteredSection()()
	var err error
	for range 21 {
		if err = exec.step(); err != nil {
			break
		}
	}
	requireErrorIs(t, err, errStepQuotaExceeded)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := &Execution{ctx: ctx, quota: 1 << 30, memoryQuota: 1}
	defer canceled.beginAccumulatorMeteredSection()()
	canceled.steps = 15
	requireErrorIs(t, canceled.step(), context.Canceled)
}

// TestBuiltinDispatchSuspendsAccumulatorMeteredSections pins the auto-degrade
// at builtin dispatch: a nested callable invoked while a section is active
// runs with the counter suspended (full periodic checks), and the caller's
// section is restored when the callee returns.
func TestBuiltinDispatchSuspendsAccumulatorMeteredSections(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	observed := -1
	probe := NewBuiltin("probe", func(inner *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		observed = inner.accumMeteredSections
		return NewNil(), nil
	})

	end := exec.beginAccumulatorMeteredSection()
	if _, err := exec.invokeCallable(probe, NewNil(), nil, nil, NewNil(), Position{}); err != nil {
		t.Fatalf("invokeCallable(probe) error = %v", err)
	}
	if observed != 0 {
		t.Fatalf("nested builtin observed %d active sections, want 0 (suspended)", observed)
	}
	if exec.accumMeteredSections != 1 {
		t.Fatalf("section counter = %d after nested dispatch returned, want 1 (restored)", exec.accumMeteredSections)
	}
	end()
	if exec.accumMeteredSections != 0 {
		t.Fatalf("section counter = %d after section ended, want 0", exec.accumMeteredSections)
	}
}

// TestBlockReentrySuspendsAccumulatorMeteredSections pins the auto-degrade at
// script re-entry: a block invoked while a builtin holds a section open runs
// with zero active sections (callBlock and builtin dispatch both suspend the
// counter), and the builtin's section is restored once the block returns. The
// gated materializer loops never invoke blocks; this guards the design against
// a future loop that does.
func TestBlockReentrySuspendsAccumulatorMeteredSections(t *testing.T) {
	t.Parallel()

	duringBlock := -1
	afterBlock := -1
	engine := MustNewEngine(Config{StepQuota: 1 << 20, MemoryQuotaBytes: 32 << 20})
	engine.builtins["probe_sections"] = NewBuiltin("probe_sections", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		duringBlock = exec.accumMeteredSections
		return NewNil(), nil
	})
	engine.builtins["with_section"] = NewBuiltin("with_section", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, block Value) (Value, error) {
		end := exec.beginAccumulatorMeteredSection()
		defer end()
		if _, err := exec.CallBlock(block, []Value{NewInt(1)}); err != nil {
			return NewNil(), err
		}
		afterBlock = exec.accumMeteredSections
		return NewNil(), nil
	})
	script := compileScriptWithEngine(t, engine, `def run()
  with_section() do |x|
    probe_sections()
  end
end`)

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
		t.Fatalf("script error: %v", err)
	}
	if duringBlock != 0 {
		t.Fatalf("block re-entry observed %d active sections, want 0 (suspended)", duringBlock)
	}
	if afterBlock != 1 {
		t.Fatalf("section counter = %d after block returned, want 1 (restored)", afterBlock)
	}
}

// TestBlocklessFlattenLargeBuildWallTime pins the wall-time fix for the #604
// adversarial finding: a 1M-leaf blockless flatten under raised quotas used to
// re-walk the whole reachable heap every 16 steps (O(N * heap / 16), minutes
// of CPU); with the accumulator-metered section it completes in well under a
// second of interpreter time. The bound is deliberately generous so slow or
// instrumented CI hosts never flake, while still failing by a wide margin if
// the quadratic periodic walk ever returns.
func TestBlocklessFlattenLargeBuildWallTime(t *testing.T) {
	if testing.Short() {
		t.Skip("wall-time pin, skipped in -short")
	}
	t.Parallel()

	const groups = 1000
	const perGroup = 1000
	const leaves = groups * perGroup
	nested := make([]Value, groups)
	for i := range nested {
		inner := make([]Value, perGroup)
		for j := range inner {
			inner[j] = NewInt(int64(i*perGroup + j))
		}
		nested[i] = NewArray(inner)
	}

	engine := MustNewEngine(Config{StepQuota: 64 << 20, MemoryQuotaBytes: 1 << 30})
	script := compileScriptWithEngine(t, engine, `def run(values)
  values.flatten.size
end`)

	start := time.Now()
	got, err := script.Call(context.Background(), "run", []Value{NewArray(nested)}, CallOptions{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("flatten of %d leaves failed after %v: %v", leaves, elapsed, err)
	}
	if got.Kind() != KindInt || got.Int() != leaves {
		t.Fatalf("flatten size = %v, want %d", got, leaves)
	}
	if elapsed > 30*time.Second {
		t.Fatalf("flatten of %d leaves took %v, want well under 30s (quadratic periodic walk regression)", leaves, elapsed)
	}
}
