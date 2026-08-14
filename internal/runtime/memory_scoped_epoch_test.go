package runtime

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"testing"
)

// TestScopedEpochSurvivesForeignMutation is the mechanism assertion, with no
// concurrency in it: one execution's memo must survive another execution's
// writes, and must still be discarded by its own. The poisoning trick from
// TestBaseWalkMemoIsUsedAndInvalidated is what makes reuse observable -- a
// served memo returns the poisoned total, a discarded one returns the fresh
// walk. Runs without t.Parallel so no concurrent test's mutations move the
// process-wide counter mid-assertion.
func TestScopedEpochSurvivesForeignMutation(t *testing.T) {
	victim, victimEnv := newEstimatorCacheExec()
	estimatorCacheShapes(victimEnv)
	foreign, foreignEnv := newEstimatorCacheExec()
	estimatorCacheShapes(foreignEnv)

	fresh := freshUncachedEstimate(victim)
	if cold := victim.estimateMemoryUsage(); cold != fresh {
		t.Fatalf("cold estimate %d != fresh %d", cold, fresh)
	}
	if victim.baseWalkCache == nil || !victim.baseWalkCache.valid {
		t.Fatalf("memo not committed by cold estimate")
	}

	const poison = 1 << 20
	victim.baseWalkCache.graphBytes += poison

	// Every category of write the foreign execution can perform. None of them
	// touches anything the victim can reach, so none may cost the victim its
	// memo. Before the epoch was scoped, each of these advanced the one
	// process-wide counter and discarded it.
	foreign.bumpMutationEpoch()
	foreignEnv.Define("scratch", NewInt(1))
	setArrayElems(foreign, NewArray([]Value{NewInt(1)}), []Value{NewInt(2)})
	if err := hashSet(foreign, NewHash(map[string]Value{}), NewString("k"), NewInt(1)); err != nil {
		t.Fatalf("foreign hashSet: %v", err)
	}

	if got := victim.estimateMemoryUsage(); got != fresh+poison {
		t.Fatalf("foreign execution's writes discarded the victim's memo: got %d, want %d", got, fresh+poison)
	}

	// The victim's own write must still discard it, or the scoping has bought
	// speed by under-counting.
	victim.bumpMutationEpoch()
	if got := victim.estimateMemoryUsage(); got != fresh {
		t.Fatalf("victim's own bump did not discard its memo: got %d, want fresh %d", got, fresh)
	}
}

// TestScopedMutatorInvalidatesEvenWhenItFails covers the failure mode the
// scoping's safety argument does NOT cover on its own. Omitting a site is
// fail-safe, because an unconverted site keeps bumping the process-wide
// counter. A converted site is only fail-safe if no path can mutate and then
// leave without bumping, and hash storing has exactly that shape: writing into
// a legacy string-keyed hash promotes it to typed storage first, allocating the
// typed-entry map and the insertion-order backing, and the key normalization
// that can fail runs after the promotion. Bumping after a successful store left
// the graph grown with no counter advanced, so a later check reused a memo that
// omitted the promotion -- an under-count, which is the one direction this
// design exists to exclude.
//
// The scoped mutators therefore bump before they write, which makes the bug
// unwritable rather than merely absent. This asserts the property through the
// failing path.
func TestScopedMutatorInvalidatesEvenWhenItFails(t *testing.T) {
	exec, env := newEstimatorCacheExec()
	estimatorCacheShapes(env)
	legacy := NewHash(map[string]Value{"a": NewInt(1)})
	env.Define("legacy", legacy)

	fresh := freshUncachedEstimate(exec)
	if cold := exec.estimateMemoryUsage(); cold != fresh {
		t.Fatalf("cold estimate %d != fresh %d", cold, fresh)
	}
	const poison = 1 << 20
	exec.baseWalkCache.graphBytes += poison

	// NaN is rejected as a hash key, but only after the promotion above it has
	// already grown the wrapper.
	if err := hashSet(exec, legacy, NewFloat(math.NaN()), NewInt(2)); err == nil {
		t.Fatalf("expected a NaN key to be rejected, so the failing path is exercised")
	}
	if got := exec.estimateMemoryUsage(); got == fresh+poison {
		t.Fatalf("a failed store left the memo valid after it had already grown the graph: got %d", got)
	}
}

// scopedEpochVictimSource is linear in its receiver on its own: the block body
// performs no mutation and calls no builtin, so nothing invalidates the
// region's prefix memo from inside.
const scopedEpochVictimSource = "def run(a)\n  a.map { |x| x }\nend"

// skipIfEstimatorVerify skips a measurement the differential oracle invalidates.
// Under VIBES_ESTIMATOR_VERIFY the region base walk recomputes a whole-stack
// reference on every check, hit or miss (see memory_blockregion.go), so
// per-element estimator work grows with the receiver whether or not the memo is
// being served: the control below reads 2209.7 visits/element at n=1000 against
// 8772.6 at n=4000 with the memo working perfectly. The scaling assertion still
// passes in that mode, because the oracle inflates both sides and leaves the
// ratio bounded, which is exactly the wrong-reason pass the control exists to
// catch. The oracle's own job is byte-for-byte agreement with a reference walk,
// and it does that across the rest of the corpus.
func skipIfEstimatorVerify(t *testing.T) {
	t.Helper()
	if estimatorVerify {
		t.Skip("the estimator oracle re-walks a full reference on every check, so per-element walk counts no longer isolate memo misses")
	}
}

// scopedEpochVictimVisitsPerElement measures the estimator nodes the victim
// walks per receiver element, which is the quantity a memo miss inflates.
// Per-element rather than total deliberately: the base walk's own cost grows
// with the receiver, so a total that grows with n proves nothing on its own,
// while a per-element figure that grows with n can only be the memo missing
// more often.
func scopedEpochVictimVisitsPerElement(t *testing.T, n int) float64 {
	t.Helper()
	script := compileScriptWithConfig(t, Config{
		MemoryQuotaBytes: 64 << 20,
		StepQuota:        Unlimited,
	}, scopedEpochVictimSource)
	elems := make([]Value, n)
	for i := range elems {
		elems[i] = NewInt(int64(i))
	}
	estimatorVisits.Store(0)
	estimatorVisitCounting.Store(true)
	defer estimatorVisitCounting.Store(false)
	if _, err := script.Call(context.Background(), "run", []Value{NewArray(elems)}, CallOptions{}); err != nil {
		t.Fatalf("victim call: %v", err)
	}
	return float64(estimatorVisits.Load()) / float64(n)
}

// TestScopedEpochControlIsFlat is the control for the test below. It measures
// the same quantity with nothing else running, and the figure must not grow
// with n. Without it, "per-element visits grow with n under an attacker" could
// equally be the base walk getting more expensive as the receiver grows, which
// would be true with or without a memo and would make the assertion meaningless.
func TestScopedEpochControlIsFlat(t *testing.T) {
	skipIfEstimatorVerify(t)
	small := scopedEpochVictimVisitsPerElement(t, 1000)
	large := scopedEpochVictimVisitsPerElement(t, 4000)
	if large > small*2 {
		t.Fatalf("control is not flat: %.1f visits/element at n=1000 against %.1f at n=4000; "+
			"the per-element measure no longer isolates memo misses", small, large)
	}
}

// TestConcurrentExecutionCannotDestroyMemo is the defect. A second execution in
// the process used to invalidate this one's base-walk memo on every write --
// and, because builtin dispatch bumps too, on every builtin call, without
// mutating anything at all. Each miss re-walks the victim's whole reachable
// graph, and that walk is deliberately unbilled, so the step quota never
// intervened: the victim's estimator work went quadratic in its own receiver
// under a workload it does not share a single value with.
//
// Every shape below is a category of write that reaches the mutation epoch.
// They are swept together because fixing the reported one first left two of
// them (array append and hash store, at 526x and 468x) still routing through
// the value package's process-wide bump.
func TestConcurrentExecutionCannotDestroyMemo(t *testing.T) {
	skipIfEstimatorVerify(t)
	attackers := []struct{ name, src string }{
		{"builtin_dispatch", "def run(n)\n  i = 0\n  while i < n\n    1.to_s\n    i = i + 1\n  end\n  i\nend"},
		// A composite rebind, not `i = i + 1`: a scalar-to-scalar rebind takes
		// bumpEpochUnlessScalarRebind's early return and never touches the epoch
		// at all, so it passed this assertion on master too and was covering
		// nothing.
		{"env_rebind", "def run(n)\n  i = 0\n  v = 0\n  while i < n\n    v = [i]\n    i = i + 1\n  end\n  i\nend"},
		{"array_append", "def run(n)\n  a = []\n  i = 0\n  while i < n\n    a << 1\n    a = []\n    i = i + 1\n  end\n  i\nend"},
		{"hash_store", "def run(n)\n  h = {}\n  i = 0\n  while i < n\n    h[\"k\"] = i\n    i = i + 1\n  end\n  i\nend"},
		{"ivar_store", "class C\n  def bump(n)\n    i = 0\n    while i < n\n      @v = i\n      i = i + 1\n    end\n    i\n  end\nend\ndef run(n)\n  C.new.bump(n)\nend"},
		{"index_assign", "def run(n)\n  a = [1, 2, 3]\n  i = 0\n  while i < n\n    a[0] = i\n    i = i + 1\n  end\n  i\nend"},
		// The wrapper mutators below reach the epoch through the value package
		// rather than the runtime. They were left on the process-wide counter in
		// the first pass and measured afterwards rather than assumed harmless,
		// which is how three of them turned out to be live vectors: hash delete
		// at 365x, hash merge at 334x and hash clear at 65x.
		{"hash_delete", "def run(n)\n  h = {}\n  i = 0\n  while i < n\n    h[\"k\"] = i\n    h.delete(\"k\")\n    i = i + 1\n  end\n  i\nend"},
		{"hash_clear", "def run(n)\n  h = {}\n  i = 0\n  while i < n\n    h[\"k\"] = i\n    h.clear\n    i = i + 1\n  end\n  i\nend"},
		{"hash_merge", "def run(n)\n  i = 0\n  while i < n\n    h = {a: 1}\n    h.merge({b: 2})\n    i = i + 1\n  end\n  i\nend"},
		{"hash_default", "def run(n)\n  i = 0\n  while i < n\n    h = Hash.new(0)\n    h[\"k\"] = i\n    i = i + 1\n  end\n  i\nend"},
		{"hash_keys", "def run(n)\n  h = {a: 1, b: 2}\n  i = 0\n  while i < n\n    h.keys\n    i = i + 1\n  end\n  i\nend"},
		{"bound_receiver", "def run(n)\n  a = [1, 2, 3]\n  i = 0\n  while i < n\n    a.length\n    i = i + 1\n  end\n  i\nend"},
	}

	const n = 3000
	// The attacker only has to be running; how many iterations it completes
	// varies with load, and the assertion must not depend on it. Under the
	// process-wide epoch every one of its iterations cost the victim a whole
	// re-walk, so even a slow attacker moved this figure by two orders of
	// magnitude.
	// The bound separates "the memo was destroyed" from "the memo held", not a
	// tight performance target: a destroyed memo costs hundreds of times the
	// control, while an intact one stays in single digits even with a competing
	// execution taking CPU. Two shapes still route to the process-wide counter
	// by design (the legacy-map materialization behind hash.keys, and the
	// bound-receiver cell, neither of which has an execution in scope), and both
	// measure in that single-digit band.
	const maxAmplification = 15.0

	control := scopedEpochVictimVisitsPerElement(t, n)
	for _, attacker := range attackers {
		t.Run(attacker.name, func(t *testing.T) {
			script := compileScriptWithConfig(t, Config{
				MemoryQuotaBytes: 64 << 20,
				StepQuota:        Unlimited,
			}, attacker.src)
			var stop atomic.Bool
			var failed atomic.Bool
			var wg sync.WaitGroup
			wg.Go(func() {
				for !stop.Load() {
					if _, err := script.Call(context.Background(), "run", []Value{NewInt(20000)}, CallOptions{}); err != nil {
						failed.Store(true)
						return
					}
				}
			})
			underAttack := scopedEpochVictimVisitsPerElement(t, n)
			stop.Store(true)
			wg.Wait()

			if failed.Load() {
				t.Fatalf("attacker script failed; the measurement below would be of nothing running")
			}
			if underAttack > control*maxAmplification {
				t.Fatalf("a concurrent execution destroyed this one's base-walk memo: "+
					"%.1f visits/element under a %s workload against %.1f alone (%.0fx)",
					underAttack, attacker.name, control, underAttack/control)
			}
		})
	}
}
