package runtime

import (
	"context"
	"fmt"
	"testing"
)

// smallHashArray builds an n-element array of one-entry hashes, the shape a
// membership probe walks element by element.
func smallHashArray(n int) Value {
	elems := make([]Value, n)
	for i := range elems {
		h := NewTypedHash(1)
		if err := h.HashSet(NewString(fmt.Sprintf("k%d", i)), NewInt(int64(i))); err != nil {
			panic(err)
		}
		elems[i] = h
	}
	return NewArray(elems)
}

// probeVisits counts the graph nodes the estimator visits while a membership
// probe scans receiver for probe.
func probeVisits(t *testing.T, receiver, probe Value) uint64 {
	t.Helper()

	script := compileScriptWithConfig(t, Config{
		MemoryQuotaBytes: 64 << 20,
		StepQuota:        Unlimited,
	}, "def run(a, p)\n  a.include?(p).inspect\nend")
	estimatorVisits.Store(0)
	estimatorVisitCounting.Store(true)
	defer estimatorVisitCounting.Store(false)
	if _, err := script.Call(context.Background(), "run", []Value{receiver, probe}, CallOptions{}); err != nil {
		t.Fatalf("include?: %v", err)
	}
	return estimatorVisits.Load()
}

// A membership probe charges one step per candidate, and the periodic quota
// check that step drives walks the reachable graph every stepSlowPathPeriod
// steps — at builtin depth always uncached. Validating the equality walk's
// sort scratch before every allocation put a second, unconditional whole-graph
// walk on each candidate comparison, so probing an array of small hashes did
// stepSlowPathPeriod times the estimator work of the identical probe that
// answers on a kind mismatch, for the same number of charged steps.
//
// The receiver is identical in both probes, so both do the same number of
// periodic checks over the same graph; only the hash probe reaches the map
// sort. Their visit counts must therefore stay within the same order.
func TestHashProbeDoesNotWalkPerComparison(t *testing.T) {
	const n = 800

	receiver := smallHashArray(n)
	hashProbe := NewTypedHash(1)
	if err := hashProbe.HashSet(NewString("nomatch"), NewInt(-1)); err != nil {
		t.Fatalf("HashSet: %v", err)
	}

	// An integer probe mismatches every candidate on kind alone, so it never
	// sorts a map: it is the same scan with the scratch path removed.
	withHash := probeVisits(t, receiver, hashProbe)
	withInt := probeVisits(t, receiver, NewInt(-1))

	if withInt == 0 {
		t.Fatal("the control probe visited no nodes; the counter is not wired")
	}
	// Generous headroom: the bound tracks whether comparisons re-walk the
	// graph, not the exact node count.
	if withHash > withInt*4 {
		t.Errorf("probing %d small hashes visited %d estimator nodes against the "+
			"kind-mismatch control's %d; a hash comparison must not force its own "+
			"whole-graph walk per candidate", n, withHash, withInt)
	}
}

// legacyHash builds a string-keyed hash with entries keys. Its comparison
// sorts the key set in one allocation, so the walk reaches its whole scratch
// footprint in a single reservation.
func legacyHash(prefix string, entries int) Value {
	m := make(map[string]Value, entries)
	for i := range entries {
		m[fmt.Sprintf("%s%d", prefix, i)] = NewInt(int64(i))
	}
	return NewHash(m)
}

// Batching the pricing by cumulative bytes is only sound inside one walk. A
// comparison resets the scratch it prices — a reused context explicitly drops
// the previous walk's footprint — so a watermark that outlives the boundary
// suppresses the check for every later comparison whose scratch merely matches
// what an earlier walk already priced. These operands reach their whole
// footprint in one reservation of equal size every time, which is exactly the
// case a smaller-byte-count heuristic cannot recognize as a new walk; a probe
// loop over such candidates bought one priced comparison and free ones after.
//
// Each comparison is measured on its own: a priced walk visits graph nodes, a
// suppressed one visits none. The quota is chosen so these operands' scratch
// clears the granule it implies — at a large enough quota the granule exceeds
// the scratch and nothing is priced at all, which the first-comparison check
// below catches rather than letting the test pass without measuring anything.
func TestEachComparisonPricesItsOwnScratch(t *testing.T) {
	exec := &Execution{root: newEnv(nil), memoryQuota: 8 << 20}
	exec.envStack = exec.envStackArr[:0]

	left, right := legacyHash("a_", 4000), legacyHash("b_", 4000)
	ctx := exec.meteredEquality()

	visits := func() uint64 {
		estimatorVisits.Store(0)
		estimatorVisitCounting.Store(true)
		ctx.Equal(left, right)
		estimatorVisitCounting.Store(false)
		if err := ctx.Err(); err != nil {
			t.Fatalf("comparison: %v", err)
		}
		return estimatorVisits.Load()
	}

	if first := visits(); first == 0 {
		t.Fatal("the first comparison must price the scratch it allocates")
	}
	for i := range 3 {
		if later := visits(); later == 0 {
			t.Fatalf("comparison %d priced nothing: the granule watermark outlived "+
				"the comparison boundary, so every later comparison whose scratch "+
				"matches an already-priced walk allocates unchecked", i+2)
		}
	}
}

// Walk scratch is Go-local and never reserved, so whatever the validator
// declines to price is invisible to the periodic check as well. A granule
// fixed in bytes therefore stops meaning anything the moment the quota it was
// sized against is not the quota in force: a host configuring well under the
// default profile, or an execution that has nearly exhausted a large quota,
// both leave less headroom than the granule allows to go unpriced. The granule
// has to track the budget actually remaining, while staying coarse enough at
// full headroom that small comparisons are still free.
func TestScratchPricingTracksQuotaAndHeadroom(t *testing.T) {
	const scratch = 48 << 10
	big := legacyHash("x_", 4000)

	for _, tc := range []struct {
		name        string
		quota, used int
		wantPriced  bool
	}{
		{"host configures a quota below the granule", 128 << 10, 0, true},
		{"default quota nearly exhausted", 16 << 20, (16 << 20) - (8 << 10), true},
		{"default quota with ample headroom", 16 << 20, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exec := &Execution{root: newEnv(nil), memoryQuota: tc.quota}
			exec.envStack = exec.envStackArr[:0]
			validate := exec.equalityScratchValidatorFunc()

			// Open the comparison, then state the headroom the last estimate saw.
			_ = validate(0, big, big)
			exec.lastMemoryUsage = tc.used

			estimatorVisits.Store(0)
			estimatorVisitCounting.Store(true)
			_ = validate(scratch, big, big)
			estimatorVisitCounting.Store(false)

			priced := estimatorVisits.Load() > 0
			if priced != tc.wantPriced {
				t.Errorf("%d KiB of scratch under a %d-byte quota with %d bytes used: priced=%v, want %v",
					scratch>>10, tc.quota, tc.used, priced, tc.wantPriced)
			}
		})
	}
}

// Repricing scratch once per granule bounds what goes unpriced; it must not
// exempt a total that matters. A key slice too big for the quota is still
// refused before it is allocated, and a walk that reaches the quota one
// granule at a time is still stopped — which is the accounting the scratch
// validator exists to provide.
func TestEqualityScratchStillRefusesOversizedAllocations(t *testing.T) {
	exec := &Execution{root: newEnv(nil), memoryQuota: 1 << 20}
	exec.envStack = exec.envStackArr[:0]
	validate := exec.equalityScratchValidatorFunc()

	if err := validate(hashKeySortScratchEntryBytes, NewNil(), NewNil()); err != nil {
		t.Fatalf("a one-entry hash's %d-byte key slice must not be refused: %v",
			hashKeySortScratchEntryBytes, err)
	}
	if err := validate(2*exec.memoryQuota, NewNil(), NewNil()); err == nil {
		t.Fatal("scratch twice the whole quota must be refused before it is allocated")
	}

	fresh := &Execution{root: newEnv(nil), memoryQuota: 1 << 20}
	fresh.envStack = fresh.envStackArr[:0]
	climb := fresh.equalityScratchValidatorFunc()
	refused := false
	for held := 0; held <= 2*fresh.memoryQuota; held += fresh.equalityScratchGranule() {
		if err := climb(held, NewNil(), NewNil()); err != nil {
			refused = true
			break
		}
	}
	if !refused {
		t.Fatal("a walk accumulating scratch past the quota one granule at a time must still be stopped")
	}
}
