package runtime

import (
	"context"
	"fmt"
	"testing"
)

// minStepsToComplete binary-searches the smallest step quota at which one
// expression over an n-element array completes.
func minStepsToComplete(t *testing.T, expr string, size int) int {
	t.Helper()

	elems := make([]Value, size)
	for i := range elems {
		elems[i] = NewInt(int64(i))
	}
	receiver := NewArray(elems)
	src := fmt.Sprintf("def run(a)\n  %s\nend", expr)

	lo, hi := 1, 4*size+1000
	for lo < hi {
		mid := (lo + hi) / 2
		script := compileScriptWithConfig(t, Config{StepQuota: mid, MemoryQuotaBytes: Unlimited}, src)
		if _, err := script.Call(context.Background(), "run", []Value{receiver}, CallOptions{}); err != nil {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// A scan over the whole receiver charges one step per element, so the step
// quota bounds the work it does. These scans previously charged a flat 3-6
// steps whatever the receiver's size, which let a script scan an arbitrarily
// large host-supplied array for a constant budget (#1131). Array#sum has always
// charged per element; these now match it.
func TestArrayScansChargeStepsPerElement(t *testing.T) {
	t.Parallel()

	scans := []struct {
		name string
		expr string
	}{
		{name: "include? miss", expr: "a.include?(-1).inspect"},
		{name: "index miss", expr: "a.index(-1).inspect"},
		{name: "rindex miss", expr: "a.rindex(-1).inspect"},
		{name: "min", expr: "a.min"},
		{name: "max", expr: "a.max"},
		{name: "minmax", expr: "a.minmax.length"},
		{name: "uniq", expr: "a.uniq.length"},
		{name: "sum (already metered)", expr: "a.sum"},
	}

	const small, large = 5000, 40000
	for _, tc := range scans {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsToComplete(t, tc.expr, small)
			atLarge := minStepsToComplete(t, tc.expr, large)

			// Proportional: an 8x receiver costs at least 4x the steps. The
			// bound is loose so per-call overhead cannot fail it; the defect
			// this pins produced identical counts at both sizes.
			if atLarge < atSmall*4 {
				t.Fatalf("%s charged %d steps at %d elements and %d at %d: not proportional to the receiver",
					tc.name, atSmall, small, atLarge, large)
			}
		})
	}
}

// Charging per element inside the scan rather than up front keeps an early
// match cheap: finding the first element must not cost the whole receiver.
func TestArrayScansStillExitEarly(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{"a.include?(0).inspect", "a.index(0).inspect"} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsToComplete(t, expr, 5000)
			atLarge := minStepsToComplete(t, expr, 40000)
			if atSmall > 100 || atLarge > 100 {
				t.Fatalf("%s cost %d and %d steps for a match at index 0, want a constant handful",
					expr, atSmall, atLarge)
			}
		})
	}
}
