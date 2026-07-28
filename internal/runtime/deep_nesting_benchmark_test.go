package runtime

import (
	"context"
	"fmt"
	"runtime"
	"testing"
)

// deepNestingSource builds a chain of single-element arrays: cur = [cur],
// repeated depth times. Every iteration is an AssignStmt, which the statement
// loop follows with a full checkMemory().
const deepNestingSource = `def run(depth)
  cur = [1]
  i = 0
  while i < depth
    cur = [cur]
    i = i + 1
  end
  cur.length
end`

// BenchmarkDeepNestingUnderQuota measures the construction cost that #1124
// reports. Under a memory quota it is quadratic in depth: rebinding a local
// bumps the mutation epoch, which is necessary because the env's reachable
// graph really changed, so the memoized base walk is invalid on every
// iteration and the next check re-walks the whole chain.
//
// It exists so a fix has something to measure. Any incremental-estimation
// work should turn the growth here from quadratic into linear, which
// TestDeepNestingScalingIsQuadraticUnderQuota states as the current baseline.
func BenchmarkDeepNestingUnderQuota(b *testing.B) {
	for _, depth := range []int64{500, 1000, 2000} {
		b.Run(fmt.Sprintf("depth-%d", depth), func(b *testing.B) {
			script := compileScriptWithConfig(b, Config{StepQuota: Unlimited, MemoryQuotaBytes: 8 << 20}, deepNestingSource)
			b.ResetTimer()
			for range b.N {
				if _, err := script.Call(context.Background(), "run", []Value{NewInt(depth)}, CallOptions{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkDeepNestingUnquotaed is the same build with metering disabled, which
// is linear. The gap between the two is the cost the quota adds.
func BenchmarkDeepNestingUnquotaed(b *testing.B) {
	for _, depth := range []int64{500, 1000, 2000} {
		b.Run(fmt.Sprintf("depth-%d", depth), func(b *testing.B) {
			script := compileScriptWithConfig(b, Config{StepQuota: Unlimited, MemoryQuotaBytes: Unlimited}, deepNestingSource)
			b.ResetTimer()
			for range b.N {
				if _, err := script.Call(context.Background(), "run", []Value{NewInt(depth)}, CallOptions{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// The quota's work grows faster than the depth does, while the unmetered build
// stays linear. This states the #1124 baseline as an executable fact rather
// than a table in an issue, so a fix has a pass/fail target: make the quotaed
// build scale like the unmetered one and this test's expectation inverts.
//
// It measures allocated bytes rather than elapsed time. The walk allocates
// seen-set entries in proportion to the nodes it visits, so total allocation
// tracks the work done, and TotalAlloc is cumulative and deterministic for a
// deterministic program -- unlike wall-clock, which on this repository's CI
// would fold in scheduler contention, GC, and the race and coverage
// instrumentation across three operating systems.
func TestDeepNestingScalingIsQuadraticUnderQuota(t *testing.T) {
	measure := func(quota int, depth int64) uint64 {
		script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: quota}, deepNestingSource)
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		if _, err := script.Call(context.Background(), "run", []Value{NewInt(depth)}, CallOptions{}); err != nil {
			t.Fatalf("depth %d: %v", depth, err)
		}
		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}

	const quota = 8 << 20
	small, large := measure(quota, 1000), measure(quota, 2000)

	// Measured about 3.9x; the assertion needs only more than 2.5x.
	if large*2 < small*5 {
		t.Fatalf("doubling the depth allocated %d bytes against %d -- under 2.5x, so the quotaed build is no longer"+
			" quadratic; if that is the fix for #1124, invert this expectation", large, small)
	}

	// The same build without metering stays linear, which localizes the cost
	// to the quota's per-assignment walk rather than to construction.
	unmeteredSmall, unmeteredLarge := measure(Unlimited, 1000), measure(Unlimited, 2000)
	if unmeteredLarge*2 > unmeteredSmall*5 {
		t.Fatalf("the unmetered build also grew superlinearly (%d then %d bytes), so the cost is not the quota's walk",
			unmeteredSmall, unmeteredLarge)
	}
}
