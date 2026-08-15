package runtime

import (
	"context"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mgomes/vibescript/vibes/value"
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

// maxAmplificationBound separates "the memo was destroyed" from "the memo held".
const maxAmplificationBound = 15.0

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
		// A host-registered builtin exchanging only scalars. Nothing crosses, so
		// the attacker keeps its private counter and the victim is unaffected.
		// This shape was missing for two review rounds; the object-carrying one
		// below is what it was missing.
		{"host_builtin_scalar", "def run(n)\n  i = 0\n  while i < n\n    host_touch()\n    i = i + 1\n  end\n  i\nend"},
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
	const maxAmplification = maxAmplificationBound

	control := scopedEpochVictimVisitsPerElement(t, n)
	for _, attacker := range attackers {
		t.Run(attacker.name, func(t *testing.T) {
			engine := MustNewEngine(Config{MemoryQuotaBytes: 64 << 20, StepQuota: Unlimited})
			engine.RegisterBuiltin("host_touch", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
				return NewInt(1), nil
			})
			script := compileScriptWithEngine(t, engine, attacker.src)
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

// TestUnclonedCrossingRetiresThePrivateCounter covers the hole the
// disjointness argument does not close on its own.
//
// A host builtin registered through the embedding API receives and returns the
// interpreter's own Values uncloned: only the Script.Call boundary copies, and
// this is not that boundary. So a body that stashes a container on one call and
// returns it on another makes it reachable from two executions at once, and
// ordinary script code in the second can then grow it. A write attributed to
// that execution alone would leave the first serving a memo that omits the
// growth, under its quota.
//
// That is not covered by the value package's contract, which forbids the host
// mutating a Value while a call given it is running: here the host only shares,
// and script code does the mutating, sequentially.
func TestUnclonedCrossingRetiresThePrivateCounter(t *testing.T) {
	stash := NewTypedHash(4)
	if err := stash.HashSet(NewString("k"), NewInt(1)); err != nil {
		t.Fatalf("seed stash: %v", err)
	}

	engine := MustNewEngine(Config{StepQuota: Unlimited, MemoryQuotaBytes: 64 << 20})
	engine.RegisterBuiltin("host_stash", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return stash, nil
	})

	// First: the container really does cross uncloned. If this ever starts
	// failing because the builtin boundary began copying, the sharing hazard is
	// gone and the marking below is merely redundant rather than wrong.
	script := compileScriptWithEngine(t, engine, "def run\n  h = host_stash()\n  h[\"grown\"] = 2\n  h.length\nend")
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := stash.HashLen(); got != 2 {
		t.Fatalf("expected the script to have grown the host's own container, got length %d; "+
			"if the builtin boundary now clones, this test's premise has changed", got)
	}

	// Second: an execution that took a container from a host builtin must have
	// retired its private counter, so its later writes are visible to every
	// other execution's memo rather than only its own.
	probe := MustNewEngine(Config{StepQuota: Unlimited, MemoryQuotaBytes: 64 << 20})
	probe.RegisterBuiltin("host_stash", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return stash, nil
	})
	var aliased bool
	probe.RegisterBuiltin("host_check", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		aliased = !exec.privateEpochQualified.Load()
		return NewNil(), nil
	})
	checkScript := compileScriptWithEngine(t, probe, "def run\n  h = host_stash()\n  host_check()\n  1\nend")
	if _, err := checkScript.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
		t.Fatalf("probe call: %v", err)
	}
	if !aliased {
		t.Fatalf("an execution that received a container from a host builtin kept its private mutation counter, " +
			"so a write here would be invisible to another execution holding the same container")
	}

	// A scalar-only host builtin shares nothing and must not retire it.
	var scalarAliased bool
	scalarEngine := MustNewEngine(Config{StepQuota: Unlimited, MemoryQuotaBytes: 64 << 20})
	scalarEngine.RegisterBuiltin("host_scalar", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewInt(7), nil
	})
	scalarEngine.RegisterBuiltin("host_check", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		scalarAliased = !exec.privateEpochQualified.Load()
		return NewNil(), nil
	})
	scalarScript := compileScriptWithEngine(t, scalarEngine, "def run\n  n = host_scalar()\n  host_check()\n  n\nend")
	if _, err := scalarScript.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
		t.Fatalf("scalar call: %v", err)
	}
	if scalarAliased {
		t.Fatalf("a scalar-only host builtin retired the private counter; a scalar cannot alias a container")
	}
}

// TestCrossScriptClosureRetiresThePrivateCounter covers a path the marking
// design missed and the inverted default catches without having been told about
// it. A block belonging to a different Script is passed through the inbound
// rebinder unchanged, because re-rooting a foreign closure would change which
// script's environment it reads. It therefore arrives carrying its own captured
// env chain, and every container in that chain is reachable from both
// executions. Nothing in this file taught the hook about closures: the rebind
// returned its input, so qualification was revoked.
func TestCrossScriptClosureRetiresThePrivateCounter(t *testing.T) {
	source := MustNewEngine(Config{StepQuota: Unlimited, MemoryQuotaBytes: 64 << 20})
	producer := compileScriptWithEngine(t, source, "def run\n  captured = [1, 2, 3]\n  proc { captured }\nend")
	closure, err := producer.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("producing the closure: %v", err)
	}
	if closure.Kind() != KindBlock {
		t.Skipf("producer returned %s, not a block; this path needs a closure to exercise", closure.Kind())
	}

	consumerEngine := MustNewEngine(Config{StepQuota: Unlimited, MemoryQuotaBytes: 64 << 20})
	var qualified bool
	consumerEngine.RegisterBuiltin("host_check", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		qualified = exec.privateEpochQualified.Load()
		return NewNil(), nil
	})
	consumer := compileScriptWithEngine(t, consumerEngine, "def run(f)\n  host_check()\n  1\nend")
	if _, err := consumer.Call(context.Background(), "run", []Value{closure}, CallOptions{}); err != nil {
		t.Fatalf("consuming the closure: %v", err)
	}
	if qualified {
		t.Fatalf("an execution handed a closure from another script kept its private counter, " +
			"so a write through that closure's captured environment would be invisible to the other execution")
	}
}

// TestUnqualifiedExecutionWritesGoProcessWide is the write-side property, which
// is a separate claim from the read-side one and was missing.
//
// "An execution uses the private counter only while nothing uncloned has
// entered it" governs which counter an execution READS. It says nothing about
// which counter a write ADVANCES, and the two came apart: environment binding
// writes advanced a counter reached through the scope chain regardless of
// qualification, so an unqualified execution advanced a private counter while
// reading the process-wide one, and its memo survived its own mutation.
//
// The write-side property is: a write must advance every counter that could be
// read by an execution able to observe the affected graph.
func TestUnqualifiedExecutionWritesGoProcessWide(t *testing.T) {
	exec, env := newEstimatorCacheExec()
	estimatorCacheShapes(env)
	if !exec.privateEpochQualified.Load() {
		t.Fatalf("a freshly set up execution should start qualified")
	}

	// Something uncloned entered: the execution now reads the process-wide
	// counter, so its writes have to reach that counter too.
	exec.revokePrivateEpoch()
	before := value.MutationEpoch()
	env.Define("written", NewArray([]Value{NewInt(1)}))
	if value.MutationEpoch() == before {
		t.Fatalf("an unqualified execution's binding write did not advance the process-wide counter, " +
			"so it advanced a counter no observer of this graph reads")
	}
}

// TestObjectPassThroughDoesNotRevoke covers a false revocation rather than an
// under-count. hashIdentity answers 0 for KindObject, so comparing two objects
// with it made every object look identical to every other and revoked when
// nothing was shared. That errs safe, but a needless revocation puts the
// execution back on the process-wide counter and restores exactly the
// uncharged quadratic amplification this change exists to remove, so it fails
// the purpose while looking harmless.
func TestObjectPassThroughDoesNotRevoke(t *testing.T) {
	a := NewObject(map[string]Value{"x": NewInt(1)})
	b := NewObject(map[string]Value{"x": NewInt(1)})
	if sameContainerPayload(a, b) {
		t.Fatalf("two independently built objects compared as the same payload, " +
			"so every cloned object would revoke qualification and forfeit the private counter")
	}
	if !sameContainerPayload(a, a) {
		t.Fatalf("an object did not compare equal to itself, so a genuine uncloned crossing would go undetected")
	}
}

// TestHostBuiltinObjectArgumentCostsEveryoneElse records the shape the twelve
// assertions above miss, and it is a cost of the mechanism working correctly
// rather than a defect.
//
// An attacker passing a container to a host-registered builtin has a container
// cross the host boundary, which revokes its qualification, because the host
// could stash it and let a third execution mutate it later. That revocation is
// right for the attacker's own accounting. But an unqualified execution's writes
// all go process-wide, so every later write in that loop discards the memo of
// every other execution in the process, and the amplification this change
// removes comes back for scripts that had nothing to do with it. The cost of one
// execution's safety is paid by everyone else.
//
// The bound here records today's behavior rather than endorsing it. Consuming
// a non-retaining declaration is what should bring this shape back into the
// single-digit band the others hold, because a host that stores no reference to
// what it is handed cannot let a third execution reach it, which is exactly the
// hazard the revocation exists for. Tighten this to maxAmplification when that
// lands; if it can be tightened without that, the revocation was never needed
// for this shape and the reasoning above is wrong.
func TestHostBuiltinObjectArgumentCostsEveryoneElse(t *testing.T) {
	skipIfEstimatorVerify(t)
	const n = 3000
	// Measured at roughly 500x. The bound leaves room for scheduling while still
	// failing if the shape degrades by another order.
	const recordedAmplification = 2000.0

	control := scopedEpochVictimVisitsPerElement(t, n)
	engine := MustNewEngine(Config{MemoryQuotaBytes: 64 << 20, StepQuota: Unlimited})
	engine.RegisterBuiltin("host_obj", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
		return NewNil(), nil
	})
	script := compileScriptWithEngine(t, engine,
		"def run(n)\n  o = {a: 1}\n  i = 0\n  while i < n\n    host_obj(o)\n    i = i + 1\n  end\n  i\nend")

	var stop, failed atomic.Bool
	var rounds atomic.Uint64
	var wg sync.WaitGroup
	wg.Go(func() {
		for !stop.Load() {
			if _, err := script.Call(context.Background(), "run", []Value{NewInt(50)}, CallOptions{}); err != nil {
				failed.Store(true)
				return
			}
			rounds.Add(1)
		}
	})
	for rounds.Load() == 0 && !failed.Load() {
		runtime.Gosched()
	}
	started := rounds.Load()
	underAttack := scopedEpochVictimVisitsPerElement(t, n)
	during := rounds.Load() - started
	stop.Store(true)
	wg.Wait()

	if failed.Load() {
		t.Fatalf("attacker script failed; the measurement would be of nothing running")
	}
	if during == 0 {
		t.Fatalf("attacker completed no call during the measurement window, so the result is not evidence either way")
	}
	t.Logf("host builtin taking an object argument: %.1f visits/element against %.1f alone (%.0fx)",
		underAttack, control, underAttack/control)
	if underAttack > control*recordedAmplification {
		t.Fatalf("this shape degraded beyond what was recorded: %.0fx against a recorded %.0fx",
			underAttack/control, recordedAmplification)
	}
}

// TestNonRetainingDeclarationRestoresTheBand is the other half of
// TestHostBuiltinObjectArgumentCostsEveryoneElse. The same attacker, with the
// same object argument, against a builtin that has declared it retains nothing:
// the crossing no longer revokes, so its writes stay on its own counter and
// cost no other execution anything.
//
// This is what makes the recorded amplification in the other test a cost of the
// default rather than of the mechanism. It also pins the falsification: the
// shape does NOT come back into the band without the declaration, so the
// revocation was genuinely needed for it.
func TestNonRetainingDeclarationRestoresTheBand(t *testing.T) {
	skipIfEstimatorVerify(t)
	const n = 3000
	control := scopedEpochVictimVisitsPerElement(t, n)

	engine := MustNewEngine(Config{MemoryQuotaBytes: 64 << 20, StepQuota: Unlimited})
	engine.RegisterBuiltin("host_obj", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
		return NewNil(), nil
	})
	DeclareNonRetaining(engine.builtins["host_obj"])
	script := compileScriptWithEngine(t, engine,
		"def run(n)\n  o = {a: 1}\n  i = 0\n  while i < n\n    host_obj(o)\n    i = i + 1\n  end\n  i\nend")

	var stop, failed atomic.Bool
	var rounds atomic.Uint64
	var wg sync.WaitGroup
	wg.Go(func() {
		for !stop.Load() {
			if _, err := script.Call(context.Background(), "run", []Value{NewInt(50)}, CallOptions{}); err != nil {
				failed.Store(true)
				return
			}
			rounds.Add(1)
		}
	})
	for rounds.Load() == 0 && !failed.Load() {
		runtime.Gosched()
	}
	started := rounds.Load()
	underAttack := scopedEpochVictimVisitsPerElement(t, n)
	during := rounds.Load() - started
	stop.Store(true)
	wg.Wait()

	if failed.Load() {
		t.Fatalf("attacker script failed; the measurement would be of nothing running")
	}
	if during == 0 {
		t.Fatalf("attacker completed no call during the measurement window, so the result is not evidence either way")
	}
	t.Logf("declared non-retaining: %.1f visits/element against %.1f alone (%.0fx)",
		underAttack, control, underAttack/control)
	if underAttack > control*maxAmplificationBound {
		t.Fatalf("a declared non-retaining builtin still cost an unrelated execution its memo: %.0fx", underAttack/control)
	}
}
