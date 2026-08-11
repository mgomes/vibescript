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
	for held := 0; held <= 2*fresh.memoryQuota; held += equalityScratchValidationGranularity {
		if err := climb(held, NewNil(), NewNil()); err != nil {
			refused = true
			break
		}
	}
	if !refused {
		t.Fatal("a walk accumulating scratch past the quota one granule at a time must still be stopped")
	}
}
