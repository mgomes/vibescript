package runtime

import (
	"context"
	"fmt"
	"testing"
)

// minStepsToComplete binary-searches the smallest step quota at which one
// expression over an n-element array of integers completes.
func minStepsToComplete(t *testing.T, expr string, size int) int {
	t.Helper()

	elems := make([]Value, size)
	for i := range elems {
		elems[i] = NewInt(int64(i))
	}
	return minStepsToCompleteOver(t, expr, elems, 4*size+1000)
}

// minStepsToCompleteOver is minStepsToComplete over a caller-built receiver.
// hi bounds the search and must exceed the expression's true cost.
func minStepsToCompleteOver(t *testing.T, expr string, elems []Value, hi int) int {
	t.Helper()

	receiver := NewArray(elems)
	src := fmt.Sprintf("def run(a)\n  %s\nend", expr)

	lo := 1
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

// uniq deduplicates a composite by comparing it against every distinct
// composite already seen, so n distinct composites cost n(n-1)/2 equality
// probes. Charging one step per element covered only the scalar case, where
// canonicalization is a map insert: a host-supplied array of composites sized
// to fit the quota could still burn billions of comparisons inside it (#1131).
// The probes are charged, so the quota bounds them and the cost of the scan
// grows with the square of the receiver, not linearly.
func TestUniqChargesStepsPerCompositeComparison(t *testing.T) {
	t.Parallel()

	distinctComposites := func(n int) []Value {
		elems := make([]Value, n)
		for i := range elems {
			elems[i] = NewArray([]Value{NewInt(int64(i))})
		}
		return elems
	}

	const small, large = 200, 400

	// The blockless form deduplicates the elements themselves; the block form
	// deduplicates the keys the block returns. Both match a composite by
	// scanning the distinct composites already seen, so both must charge it.
	forms := []struct {
		name  string
		expr  string
		elems func(int) []Value
	}{
		{name: "blockless", expr: "a.uniq.length", elems: distinctComposites},
		{
			name: "block key",
			expr: "a.uniq { |x| [x] }.length",
			elems: func(n int) []Value {
				elems := make([]Value, n)
				for i := range elems {
					elems[i] = NewInt(int64(i))
				}
				return elems
			},
		},
	}

	for _, form := range forms {
		t.Run(form.name, func(t *testing.T) {
			t.Parallel()
			atSmall := minStepsToCompleteOver(t, form.expr, form.elems(small), small*small)
			atLarge := minStepsToCompleteOver(t, form.expr, form.elems(large), large*large)

			// n(n-1)/2 probes means doubling the receiver quadruples the charge.
			// Assert only that it grows faster than linearly, which a flat or
			// per-element charge cannot do, so the bound does not encode the
			// exact probe count.
			if atLarge < atSmall*3 {
				t.Errorf("uniq over %d composites cost %d steps and over %d cost %d; "+
					"doubling the receiver should more than double the charge, so the "+
					"per-comparison work is unmetered", small, atSmall, large, atLarge)
			}
		})
	}

	// Deduplicating scalars stays a linear map insert -- the probe charge must
	// not turn the common case quadratic, nor cost anything when there is
	// nothing to probe.
	t.Run("scalars stay linear", func(t *testing.T) {
		t.Parallel()
		scalarSmall := minStepsToComplete(t, "a.uniq.length", small)
		scalarLarge := minStepsToComplete(t, "a.uniq.length", large)
		if scalarLarge > scalarSmall*3 {
			t.Errorf("uniq over %d scalars cost %d steps and over %d cost %d; "+
				"scalar deduplication should stay linear", small, scalarSmall, large, scalarLarge)
		}
	})
}

// Matching a composite stops at the first equal candidate, so a duplicate that
// matches near the front costs one probe, not a full scan. Charging the whole
// set's size for every element would overcharge a duplicate-heavy receiver
// enough to exhaust the quota on work it never did.
func TestUniqChargesOnlyTheProbesTheScanPerforms(t *testing.T) {
	t.Parallel()

	const distinct, repeats = 50, 400

	// A short distinct prefix followed by repetitions of its first element.
	// Every repetition matches on the first probe.
	duplicateTail := make([]Value, 0, distinct+repeats)
	for i := range distinct {
		duplicateTail = append(duplicateTail, NewArray([]Value{NewInt(int64(i))}))
	}
	for range repeats {
		duplicateTail = append(duplicateTail, NewArray([]Value{NewInt(0)}))
	}

	allDistinct := make([]Value, distinct+repeats)
	for i := range allDistinct {
		allDistinct[i] = NewArray([]Value{NewInt(int64(i))})
	}

	size := distinct + repeats
	withTail := minStepsToCompleteOver(t, "a.uniq.length", duplicateTail, size*size)
	allMisses := minStepsToCompleteOver(t, "a.uniq.length", allDistinct, size*size)

	// The tail probes once per element, so its cost is dominated by the short
	// distinct prefix; the all-distinct receiver of the same length pays the
	// full quadratic. An order of magnitude apart, so the bound holds without
	// encoding either exact count.
	if withTail*10 > allMisses {
		t.Errorf("uniq over %d elements cost %d steps when %d of them were duplicates "+
			"and %d when all were distinct; a duplicate matching on its first probe "+
			"must not be charged for a full scan", size, withTail, repeats, allMisses)
	}
}

// A scan charge must be proportional to the elements actually scanned, so an
// empty receiver costs strictly less than a one-element one. stepN charges a
// step even for a count of zero, so a charge of len(arr) written without a
// guard made [].uniq cost exactly what [x].uniq cost -- a scan it never ran,
// which an exact step quota would notice. include? and index step inside their
// loops and so were never affected; they are controls on the same assertion.
func TestEmptyReceiverPaysNoScanCharge(t *testing.T) {
	t.Parallel()

	exprs := []string{"a.uniq.length", "a.include?(-1).inspect", "a.index(-1).inspect"}
	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			empty := minStepsToCompleteOver(t, expr, nil, 1000)
			single := minStepsToCompleteOver(t, expr, []Value{NewInt(9)}, 1000)
			if empty >= single {
				t.Errorf("%s cost %d steps over an empty receiver and %d over a "+
					"one-element one; a scan over no elements must not be charged "+
					"for one", expr, empty, single)
			}
		})
	}
}
