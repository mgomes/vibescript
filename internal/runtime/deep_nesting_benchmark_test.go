package runtime

import (
	"context"
	"fmt"
	"strings"
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
// It counts the nodes the estimator visits, which is the work the complexity
// claim is about. Elapsed time would fold in scheduling, GC, and the race and
// coverage instrumentation this repository runs across three operating
// systems; allocated bytes would only approximate visits, and a change in how
// the seen maps grow would decouple the two and let this announce a fix that
// had not happened.
func TestDeepNestingScalingIsQuadraticUnderQuota(t *testing.T) {
	measure := func(quota int, depth int64) uint64 {
		script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: quota}, deepNestingSource)
		estimatorVisits.Store(0)
		estimatorVisitCounting.Store(true)
		defer estimatorVisitCounting.Store(false)
		if _, err := script.Call(context.Background(), "run", []Value{NewInt(depth)}, CallOptions{}); err != nil {
			t.Fatalf("depth %d: %v", depth, err)
		}
		return estimatorVisits.Load()
	}

	const quota = 8 << 20
	small, large := measure(quota, 1000), measure(quota, 2000)

	// Measured 2,032,034 then 8,064,034 -- 3.97x for a doubled depth. The
	// assertion needs only 2.5x, so it states the complexity rather than
	// pinning an exact count that ordinary estimator changes would shift.
	if large*2 < small*5 {
		t.Fatalf("doubling the depth cost %d estimator visits against %d -- under 2.5x, so the quotaed build is no"+
			" longer quadratic; if that is the fix for #1124, invert this expectation", large, small)
	}

	// The same build without metering walks nothing at all, which localizes the
	// cost squarely to the quota's per-assignment walk rather than to
	// construction: the estimator is the only thing that scales here. The
	// contract verifier walks the graph on every declared dispatch regardless of
	// quota, so it is the one thing that can make an unmetered build walk.
	if builtinContractVerify {
		return
	}
	for _, depth := range []int64{1000, 2000} {
		if visits := measure(Unlimited, depth); visits != 0 {
			t.Fatalf("the unmetered build walked %d nodes at depth %d, so the estimator is not the whole cost",
				visits, depth)
		}
	}
}

// A one-element literal skips the build accumulator, so the quota guarantee it
// provided has to come from the post-build check instead. These pin that it
// does, on both sides of the exemption.
func TestArrayLiteralsStillRejectPastTheQuota(t *testing.T) {
	t.Parallel()

	// Each element is a fresh temporary, which is what the accumulator exists
	// to catch; aliased elements would deduplicate and charge once.
	fresh := func(n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = fmt.Sprintf("s + %q", strings.Repeat("y", i+1))
		}
		return "def run(s)\n  [" + strings.Join(parts, ", ") + "]\nend"
	}

	tests := []struct {
		name   string
		source string
	}{
		{name: "one element, no accumulator", source: fresh(1)},
		{name: "two elements, accumulator", source: fresh(2)},
		{name: "many elements, accumulator", source: fresh(12)},
	}

	big := NewString(strings.Repeat("x", 200*1024))
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: 300 * 1024}, tc.source)
			_, err := script.Call(context.Background(), "run", []Value{big}, CallOptions{})
			if err == nil {
				t.Fatalf("%s: a literal past the quota was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), "memory quota") {
				t.Fatalf("%s: error = %v, want the memory quota", tc.name, err)
			}
		})
	}
}

// A literal that fits is still built, so the exemption does not reject work it
// should allow.
func TestSmallArrayLiteralsStillBuild(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: 8 << 20}, `def run()
  a = [1]
  b = [a]
  c = [b, b]
  [a.length, b.length, c.length].join(",")
end`)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "1,1,2" {
		t.Fatalf("got %q, want 1,1,2", got.String())
	}
}
