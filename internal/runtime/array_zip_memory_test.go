package runtime

import (
	"context"
	goruntime "runtime"
	"strings"
	"testing"
)

// TestZipPricesItsResultBeforeBuilding pins that Array#zip rejects an
// oversized result before allocating it. The result is
// O(len(receiver) * len(args)) while the inputs are only
// O(len(receiver) + len(args)), so the pre-call check on the operands says
// nothing about it and the post-call check runs after it exists. A wrapper
// with many array parameters could therefore multiply a quota-sized receiver
// into a result many times the quota, and the quota error arrived only after
// the spike: this case allocated 26 MiB against a 4 MiB quota (#44).
// Not parallel: MemStats.TotalAlloc is process-wide, so a concurrent test's
// allocations would land between the two snapshots and be attributed here.
func TestZipPricesItsResultBeforeBuilding(t *testing.T) {
	const args = 400
	var src strings.Builder
	params := make([]string, args)
	empties := make([]string, args)
	for i := range args {
		params[i] = "a" + string(rune('A'+i%26)) + string(rune('a'+(i/26)%26))
		empties[i] = "[]"
	}
	joined := strings.Join(params, ", ")
	src.WriteString("def widen(" + joined + ")\n  base.zip(" + joined + ")\nend\n\n")
	src.WriteString("def run()\n  widen(" + strings.Join(empties, ", ") + ")\nend\n")

	const quotaBytes = 4 << 20
	script := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: quotaBytes}, src.String())
	base := make([]Value, 2000)
	for i := range base {
		base[i] = NewInt(int64(i))
	}

	var before, after goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&before)
	_, err := script.Call(context.Background(), "run", nil,
		CallOptions{Globals: map[string]Value{"base": NewArray(base)}})
	goruntime.ReadMemStats(&after)

	if err == nil {
		t.Fatal("a zip result far past the memory quota must be rejected")
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	// Generous next to the 26 MiB the unpriced build allocated, tight enough
	// that rebuilding the whole result would trip it.
	if limit := uint64(2 * quotaBytes); allocated > limit {
		t.Fatalf("zip allocated %.2f MiB before failing, want under %.2f MiB",
			float64(allocated)/(1<<20), float64(limit)/(1<<20))
	}
}

// TestZipStaysCorrectUnderThePriceCheck pins that pricing the result does not
// disturb ordinary zips, including the ragged case Ruby pads with nil.
func TestZipStaysCorrectUnderThePriceCheck(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  [1, 2, 3].zip([4, 5], [6])
end`)
	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("an ordinary zip must succeed: %v", err)
	}
	if got := result.Inspect(); got != "[[1, 4, 6], [2, 5, nil], [3, nil, nil]]" {
		t.Fatalf("zip = %s, want the nil-padded rows", got)
	}
}
