package runtime

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/vibes/value"
)

// These tests pin the soundness contract of the memoized estimator base walk
// (beginBaseWalk): a memoized check must return exactly the bytes an
// unmemoized walk over the same state would, and every mutation path must
// invalidate the memo before the next check can observe stale bytes.

// freshUncachedEstimate replicates the pre-memoization estimate algorithm on a
// brand-new estimator: one base walk followed by the extras, sharing one
// seen-state. It is the oracle every memoized result must equal exactly.
func freshUncachedEstimate(exec *Execution, extras ...Value) int {
	est := newMemoryEstimator()
	total := exec.estimateMemoryUsageBase(est)
	for _, extra := range extras {
		total += est.value(extra)
	}
	return total
}

func freshUncachedCallRootsEstimate(exec *Execution, callee, receiver Value, args []Value, kwargs map[string]Value, block Value) int {
	est := newMemoryEstimator()
	total := exec.estimateMemoryUsageBase(est)
	if callee.Kind() != KindNil {
		total += est.value(callee)
	}
	if receiver.Kind() != KindNil {
		total += est.value(receiver)
	}
	for _, arg := range args {
		total += est.value(arg)
	}
	for _, kwarg := range kwargs {
		total += est.value(kwarg)
	}
	if !block.IsNil() {
		total += est.value(block)
	}
	return total
}

func newEstimatorCacheExec() (*Execution, *Env) {
	exec := &Execution{quota: 1 << 30, memoryQuota: 1 << 30}
	exec.root = newEnv(nil)
	exec.adoptRootEpoch()
	env := newEnv(exec.root)
	exec.pushEnv(env)
	return exec, env
}

func estimatorCacheRows(n int) Value {
	rows := make([]Value, 0, n)
	for i := range n {
		row := NewTypedHash(3)
		_ = row.HashSet(NewSymbol("id"), NewInt(int64(i)))
		_ = row.HashSet(NewSymbol("name"), NewString(fmt.Sprintf("row-%d", i)))
		_ = row.HashSet(NewSymbol("tags"), NewArray([]Value{NewString("a"), NewString("b")}))
		rows = append(rows, row)
	}
	return NewArray(rows)
}

// estimatorCacheShapes binds one representative graph per shape class into env
// and returns extra roots to charge on top, mirroring the states real checks
// see: flat arrays, nested typed-hash rows, aliased graphs (including an alias
// into the interior of another binding's subgraph), self-referential values,
// shared strings, hash defaults, and closures.
func estimatorCacheShapes(env *Env) []Value {
	big := NewString(strings.Repeat("x", 4096))
	flat := make([]Value, 512)
	for i := range flat {
		flat[i] = NewInt(int64(i))
	}
	env.Define("flat", NewArray(flat))

	rows := estimatorCacheRows(64)
	env.Define("rows", rows)
	env.Define("rows_alias", rows)
	env.Define("first_row", rows.Array()[0])
	env.Define("wrapped", NewArray([]Value{rows, big}))
	env.Define("big", big)
	env.Define("big_alias", big)

	selfArr := NewArray(make([]Value, 0, 2))
	selfArr.SetArrayElems(append(selfArr.Array(), selfArr, NewString("cycle")))
	env.Define("self_arr", selfArr)

	selfHash := NewTypedHash(1)
	_ = selfHash.HashSet(NewSymbol("me"), selfHash)
	env.Define("self_hash", selfHash)

	env.Define("defaulted", NewHashWithDefault(map[string]Value{"k": NewInt(1)}, big, NewNil()))

	legacy := NewHash(map[string]Value{"a": NewString("legacy"), "b": rows})
	env.Define("legacy", legacy)

	// Extras alias env-reachable state (rows, big) and add fresh values, so the
	// memoized extras walk exercises both dedup-against-base and fresh counting.
	return []Value{rows, big, NewArray([]Value{rows.Array()[1], NewString("fresh extra")})}
}

// TestBaseWalkMemoEstimateEqualsUncached asserts the core exactness contract:
// cold (memo-committing), warm (memo-consuming), and cache-disabled estimates
// all equal a fresh unmemoized estimator's result, for plain, extras, and
// call-root check shapes over the full shape battery.
func TestBaseWalkMemoEstimateEqualsUncached(t *testing.T) {
	exec, env := newEstimatorCacheExec()
	extras := estimatorCacheShapes(env)

	fresh := freshUncachedEstimate(exec)
	cold := exec.estimateMemoryUsage()
	warm := exec.estimateMemoryUsage()
	if cold != fresh || warm != fresh {
		t.Fatalf("plain estimate diverged: fresh=%d cold=%d warm=%d", fresh, cold, warm)
	}

	freshExtras := freshUncachedEstimate(exec, extras...)
	coldExtras := exec.estimateMemoryUsage(extras...)
	warmExtras := exec.estimateMemoryUsage(extras...)
	if coldExtras != freshExtras || warmExtras != freshExtras {
		t.Fatalf("extras estimate diverged: fresh=%d cold=%d warm=%d", freshExtras, coldExtras, warmExtras)
	}

	rows, _ := env.Get("rows")
	big, _ := env.Get("big")
	kwargs := map[string]Value{"payload": rows}
	freshRoots := freshUncachedCallRootsEstimate(exec, NewNil(), rows, []Value{big, rows}, kwargs, NewNil())
	gotRoots := exec.estimateMemoryUsageForCallRoots(NewNil(), rows, []Value{big, rows}, kwargs, NewNil())
	warmRoots := exec.estimateMemoryUsageForCallRoots(NewNil(), rows, []Value{big, rows}, kwargs, NewNil())
	if gotRoots != freshRoots || warmRoots != freshRoots {
		t.Fatalf("call-root estimate diverged: fresh=%d got=%d warm=%d", freshRoots, gotRoots, warmRoots)
	}

	baseWalkCacheDisabled.Store(true)
	defer baseWalkCacheDisabled.Store(false)
	disabled := exec.estimateMemoryUsage(extras...)
	if disabled != freshExtras {
		t.Fatalf("cache-disabled estimate diverged: fresh=%d disabled=%d", freshExtras, disabled)
	}
}

// TestBaseWalkMemoIsUsedAndInvalidated proves the memo mechanics directly: a
// second check reuses the committed graph bytes (observable by poisoning
// them), a mutation-epoch bump discards the memo, and a topology change (env
// push/pop) discards it too. Runs without t.Parallel so no concurrent test's
// mutations bump the shared epoch mid-assertion.
func TestBaseWalkMemoIsUsedAndInvalidated(t *testing.T) {
	exec, env := newEstimatorCacheExec()
	estimatorCacheShapes(env)

	fresh := freshUncachedEstimate(exec)
	cold := exec.estimateMemoryUsage()
	if cold != fresh {
		t.Fatalf("cold estimate %d != fresh %d", cold, fresh)
	}
	if exec.baseWalkCache == nil || !exec.baseWalkCache.valid {
		t.Fatalf("memo not committed by cold estimate")
	}

	const poison = 1 << 20
	exec.baseWalkCache.graphBytes += poison
	if got := exec.estimateMemoryUsage(); got != fresh+poison {
		t.Fatalf("poisoned memo not reused: got %d, want %d (memoized path not taken)", got, fresh+poison)
	}

	value.BumpMutationEpoch()
	if got := exec.estimateMemoryUsage(); got != fresh {
		t.Fatalf("epoch bump did not discard memo: got %d, want fresh %d", got, fresh)
	}

	exec.baseWalkCache.graphBytes += poison
	extraEnv := newEnv(env)
	extraEnv.Define("late", NewString(strings.Repeat("y", 128)))
	exec.pushEnv(extraEnv)
	freshPushed := freshUncachedEstimate(exec)
	if got := exec.estimateMemoryUsage(); got != freshPushed {
		t.Fatalf("env push did not discard memo: got %d, want %d", got, freshPushed)
	}
	exec.popEnv()
	if got := exec.estimateMemoryUsage(); got != fresh {
		t.Fatalf("env pop did not discard memo: got %d, want fresh %d", got, fresh)
	}
}

// TestBaseWalkMemoBuiltinDepthBypass pins the guard that closes the raw-write
// hazard: while a Go builtin that has not declared non-mutation is on the
// stack, its checks must neither reuse nor refresh the memo, because such a
// builtin mutates containers through raw slice/map writes the epoch cannot
// observe per-write.
//
// Dispatch counts an undeclared builtin in both builtinDepth and
// undeclaredBuiltinDepth, and it is the second that the memo consults, so the
// simulation raises both. Raising only builtinDepth would simulate a builtin
// that promised not to do the very write this test then performs.
func TestBaseWalkMemoBuiltinDepthBypass(t *testing.T) {
	exec, env := newEstimatorCacheExec()
	estimatorCacheShapes(env)

	fresh := freshUncachedEstimate(exec)
	if got := exec.estimateMemoryUsage(); got != fresh {
		t.Fatalf("cold estimate %d != fresh %d", got, fresh)
	}

	exec.builtinDepth++
	exec.undeclaredBuiltinDepth++
	defer func() {
		exec.builtinDepth--
		exec.undeclaredBuiltinDepth--
	}()

	// Simulate a builtin's raw in-place write with no epoch bump: the flat
	// array's first element grows by a large string.
	flat, _ := env.Get("flat")
	flat.Array()[0] = NewString(strings.Repeat("z", 8192))

	freshMutated := freshUncachedEstimate(exec)
	if freshMutated <= fresh {
		t.Fatalf("raw write did not grow the graph: before=%d after=%d", fresh, freshMutated)
	}
	if got := exec.estimateMemoryUsage(); got != freshMutated {
		t.Fatalf("in-builtin check reused stale memo: got %d, want %d", got, freshMutated)
	}
	if exec.baseWalkCache != nil && exec.baseWalkCache.valid {
		t.Fatalf("in-builtin check must not refresh the memo")
	}
}

// The other side of the same guard: a builtin that declared non-mutation is not
// counted, so a check inside it keeps the memo instead of re-walking. That is
// the whole benefit of the declaration for a member running a memory check of
// its own, and it is sound only because the declaration rules out exactly the
// raw write the test above performs -- so this one performs no write, and
// requires the memoized answer to equal the fresh one anyway.
func TestBaseWalkMemoKeptInsideDeclaredBuiltin(t *testing.T) {
	exec, env := newEstimatorCacheExec()
	estimatorCacheShapes(env)

	fresh := freshUncachedEstimate(exec)
	if got := exec.estimateMemoryUsage(); got != fresh {
		t.Fatalf("cold estimate %d != fresh %d", got, fresh)
	}

	// A declared builtin raises builtinDepth alone; undeclaredBuiltinDepth
	// stays at zero, which is what keeps the memo engaged.
	exec.builtinDepth++
	defer func() { exec.builtinDepth-- }()

	if got := exec.estimateMemoryUsage(); got != fresh {
		t.Fatalf("check inside a declared builtin returned %d, want the unchanged %d", got, fresh)
	}
	if exec.baseWalkCache == nil || !exec.baseWalkCache.valid {
		t.Fatal("a check inside a declared builtin discarded the memo; the declaration buys " +
			"nothing for a builtin that runs a memory check of its own")
	}
	_ = env
}

// baseWalkMemoMutation primes the memo, applies mutate, and requires the next
// memoizable check to equal a fresh unmemoized estimate. A mutator that fails
// to bump the mutation epoch (or the topology version) leaves the memo stale
// and fails here. grow reports whether the mutation must strictly increase the
// estimate, catching mutations that were invalidated but not actually applied.
func baseWalkMemoMutation(t *testing.T, name string, grow bool, setup func(*Execution, *Env) func()) {
	t.Run(name, func(t *testing.T) {
		exec, env := newEstimatorCacheExec()
		estimatorCacheShapes(env)
		mutate := setup(exec, env)

		before := exec.estimateMemoryUsage()
		if exec.baseWalkCache == nil || !exec.baseWalkCache.valid {
			t.Fatalf("memo not primed")
		}
		mutate()
		fresh := freshUncachedEstimate(exec)
		after := exec.estimateMemoryUsage()
		if after != fresh {
			t.Fatalf("post-mutation estimate stale: got %d, want fresh %d (before %d)", after, fresh, before)
		}
		if grow && after <= before {
			t.Fatalf("mutation did not grow the estimate: before=%d after=%d", before, after)
		}
	})
}

// TestBaseWalkMemoMutationMatrix drives every wrapper- and env-level mutation
// primitive the runtime uses (the raw eval/call-site writes and Go builtins are
// covered end to end by TestMemoryQuotaThresholdsUnchangedByMemo) and asserts
// estimate-after-mutation equals a fresh unmemoized estimate, including
// mutations through aliases into another binding's interior and through cycles.
func TestBaseWalkMemoMutationMatrix(t *testing.T) {
	bigStr := func() Value { return NewString(strings.Repeat("m", 16384)) }

	baseWalkMemoMutation(t, "array_set_elems_grow", true, func(_ *Execution, env *Env) func() {
		arr, _ := env.Get("flat")
		return func() { arr.SetArrayElems(append(arr.Array(), bigStr())) }
	})
	baseWalkMemoMutation(t, "array_set_elems_shrink", false, func(_ *Execution, env *Env) func() {
		arr, _ := env.Get("flat")
		return func() { arr.SetArrayElems(arr.Array()[:1]) }
	})
	baseWalkMemoMutation(t, "hash_set_typed", true, func(_ *Execution, env *Env) func() {
		rows, _ := env.Get("rows")
		row := rows.Array()[0]
		return func() { _ = row.HashSet(NewSymbol("blob"), bigStr()) }
	})
	baseWalkMemoMutation(t, "hash_set_legacy_promotes", true, func(_ *Execution, env *Env) func() {
		legacy, _ := env.Get("legacy")
		return func() { _ = legacy.HashSet(NewSymbol("blob"), bigStr()) }
	})
	baseWalkMemoMutation(t, "hash_delete_key", false, func(_ *Execution, env *Env) func() {
		rows, _ := env.Get("rows")
		row := rows.Array()[1]
		return func() { _, _, _ = row.HashDeleteKey(NewSymbol("tags")) }
	})
	baseWalkMemoMutation(t, "hash_clear_entries", false, func(_ *Execution, env *Env) func() {
		rows, _ := env.Get("rows")
		row := rows.Array()[2]
		return func() { row.HashClearEntries() }
	})
	baseWalkMemoMutation(t, "hash_set_defaults", true, func(_ *Execution, env *Env) func() {
		h, _ := env.Get("defaulted")
		return func() { h.SetHashDefaults(bigStr(), NewNil()) }
	})
	baseWalkMemoMutation(t, "hash_legacy_view_materialization", true, func(_ *Execution, env *Env) func() {
		rows, _ := env.Get("rows")
		row := rows.Array()[3]
		return func() { _ = row.Hash() }
	})
	baseWalkMemoMutation(t, "hash_reserve_typed_order", true, func(_ *Execution, env *Env) func() {
		rows, _ := env.Get("rows")
		row := rows.Array()[4]
		return func() { row.ReserveTypedHashOrder(64) }
	})
	baseWalkMemoMutation(t, "env_assign_existing", true, func(_ *Execution, env *Env) func() {
		return func() { env.Assign("big", NewArray([]Value{bigStr(), bigStr()})) }
	})
	baseWalkMemoMutation(t, "env_define_new", true, func(_ *Execution, env *Env) func() {
		return func() { env.Define("late_binding", bigStr()) }
	})
	baseWalkMemoMutation(t, "env_define_static", false, func(_ *Execution, env *Env) func() {
		return func() { env.DefineStatic("static_binding", NewInt(7)) }
	})
	baseWalkMemoMutation(t, "mutate_through_interior_alias", true, func(_ *Execution, env *Env) func() {
		// first_row aliases rows[0]; growing it must be visible through every
		// path that reaches it (rows, rows_alias, wrapped) on the next check.
		row, _ := env.Get("first_row")
		return func() { _ = row.HashSet(NewSymbol("alias_blob"), bigStr()) }
	})
	baseWalkMemoMutation(t, "mutate_through_cycle", true, func(_ *Execution, env *Env) func() {
		selfArr, _ := env.Get("self_arr")
		return func() { selfArr.SetArrayElems(append(selfArr.Array(), bigStr())) }
	})
	baseWalkMemoMutation(t, "module_registered", true, func(exec *Execution, _ *Env) func() {
		return func() {
			if exec.modules == nil {
				exec.modules = make(map[string]Value)
			}
			exec.bumpMutationEpoch()
			exec.modules["late"] = NewHash(map[string]Value{"exported": bigStr()})
		}
	})
	baseWalkMemoMutation(t, "reserved_scratch", true, func(exec *Execution, _ *Env) func() {
		return func() { exec.reserveLoopScratch(4096) }
	})
}

// memoQuotaThresholdScripts is the mutator battery for the end-to-end
// threshold-equivalence regression. Each script exercises one mutation path
// (in-place array mutators, hash mutators, element assignment, ivar and
// typed-accessor writes, alias and cycle mutations) under a real interpreter
// run whose per-statement and call-boundary checks flow through the memoized
// base walk.
//
// Each script ends with a large bare string literal: evaluating a literal
// bumps nothing, so the literal's own per-statement check is the peak
// allocation observed inside the mutation's staleness window. A mutation path
// that failed to invalidate the memo would make that peak check read a stale
// base (missing the mutation's growth, or still counting removed bytes) and
// shift the script's exact pass/fail quota away from the unmemoized run's.
// Mutations that must stay bump-free until that literal use literal payloads
// directly (index assignment, ivar writes); builtin-backed mutators are
// invalidated by dispatch itself, which this battery verifies end to end.
var memoQuotaThresholdScripts = func() map[string]string {
	payloadA := strings.Repeat("p", 2000)
	payloadB := strings.Repeat("q", 2000)
	peak := strings.Repeat("k", 3000)
	build := func(body string) string {
		return fmt.Sprintf("def run()\n%s\n  %q\nend", body, peak)
	}
	scripts := map[string]string{
		"array_push":         build(fmt.Sprintf("  a = [1]\n  a.push(%q)", payloadA)),
		"array_shovel":       build(fmt.Sprintf("  a = [1]\n  a << %q", payloadA)),
		"array_pop":          build(fmt.Sprintf("  a = [%q, %q]\n  a.pop", payloadA, payloadB)),
		"array_shift":        build(fmt.Sprintf("  a = [%q, %q]\n  a.shift", payloadA, payloadB)),
		"array_insert":       build(fmt.Sprintf("  a = [1]\n  a.insert(0, %q)", payloadA)),
		"array_fill":         build(fmt.Sprintf("  a = [1, 2, 3]\n  a.fill(%q)", payloadA)),
		"array_sort_bang":    build(fmt.Sprintf("  a = [%q, %q]\n  a.sort!", payloadB, payloadA)),
		"array_clear":        build(fmt.Sprintf("  a = [%q]\n  a.clear", payloadA)),
		"array_plus_append":  build(fmt.Sprintf("  a = [1]\n  a = a + [%q]", payloadA)),
		"array_unshift":      build(fmt.Sprintf("  a = [1]\n  a.unshift(%q)", payloadA)),
		"array_map_bang":     build(fmt.Sprintf("  a = [1, 2, 3]\n  a.map! { |v| %q }", payloadA)),
		"array_index_assign": build(fmt.Sprintf("  a = [1, 2, 3]\n  a[1] = %q", payloadA)),
		"hash_index_assign":  build(fmt.Sprintf("  h = {a: 1}\n  h[:b] = %q", payloadA)),
		"hash_store":         build(fmt.Sprintf("  h = {a: 1}\n  h.store(:b, %q)", payloadA)),
		"hash_delete":        build(fmt.Sprintf("  h = {a: %q, b: %q}\n  h.delete(:a)", payloadA, payloadB)),
		"hash_merge_bang":    build(fmt.Sprintf("  h = {a: 1}\n  h.merge!({b: %q})", payloadA)),
		"hash_clear":         build(fmt.Sprintf("  h = {a: %q}\n  h.clear", payloadA)),
		"hash_delete_if":     build(fmt.Sprintf("  h = {a: %q, b: %q}\n  h.delete_if { |k, v| k == :a }", payloadA, payloadB)),
		"nested_alias_mutation": build(fmt.Sprintf(
			"  outer = {rows: [[1, 2], [3, 4]]}\n  inner = outer[:rows][0]\n  inner.push(%q)", payloadA)),
		"self_referential_mutation": build(fmt.Sprintf("  a = []\n  a.push(a)\n  a.push(%q)", payloadA)),
		"typed_hash_rows": build(fmt.Sprintf(
			"  rows = []\n  i = 0\n  while i < 8\n    rows.push({id: i, name: \"row\" + i.to_s, tags: [\"a\", \"b\"]})\n    i = i + 1\n  end\n  rows[3][:blob] = %q", payloadA)),
		"string_growth": build(fmt.Sprintf("  s = %q\n  t = s.ljust(4000, \"y\")", payloadA)),
	}
	scripts["ivar_write"] = fmt.Sprintf(`class Box
  def initialize()
    @payload = nil
  end

  def fill()
    @payload = %q
    %q
  end
end

def run()
  b = Box.new
  b.fill
end`, payloadA, peak)
	scripts["typed_accessor_write"] = fmt.Sprintf(`class Box
  property payload
end

def run()
  b = Box.new
  b.payload = %q
  %q
end`, payloadA, peak)

	// Block-iteration region battery (see memory_blockregion.go). Each script
	// runs a block-driving builtin whose block body BOTH mutates state and
	// allocates the peak literal, so the peak's per-statement check runs on the
	// region base walk while a memoized prefix is live. A region that wrongly
	// suppressed the epoch bump for a write that escaped the block — an
	// outer-variable rebind, or a mutation of a prefix-reachable container —
	// would leave that prefix stale, shifting the memoized threshold below the
	// unmemoized run's and failing here. Writes confined to the block's own
	// scope are re-walked fresh every check, so they must NOT drift either.
	//
	// The peak sits inside the block, unlike the mutator battery above, because
	// the region base walk is engaged only while a block body drives the check.
	regionPeak := strings.Repeat("z", 3000)
	regionScripts := map[string]string{
		// Cumulatively grow an outer local from inside the block: resolves up the
		// parent chain to a prefix scope, which must bump so each later iteration's
		// checks see the grown binding. Cumulative growth (not a constant rebind)
		// makes a stale prefix detectably wrong: iteration k's correct prefix is
		// strictly larger than iteration k-1's.
		"region_each_outer_rebind": fmt.Sprintf(`def run()
  acc = "seed"
  [1, 2, 3, 4].each do |v|
    acc = acc + %q
    %q
  end
end`, payloadA, regionPeak),
		// Mutate an outer array captured by the block: the append goes through the
		// value package and must bump the prefix memo.
		"region_each_outer_push": fmt.Sprintf(`def run()
  buf = []
  [1, 2, 3, 4].each do |v|
    buf.push(%q)
    %q
  end
end`, payloadA, regionPeak),
		// Grow a block-local each iteration: confined to the block scope, which the
		// region re-walks fresh, so the estimate must track it without any bump.
		"region_each_block_local": fmt.Sprintf(`def run()
  [1, 2, 3, 4].each do |v|
    local = %q
    %q
  end
end`, payloadA, regionPeak),
		// map building a result while the block body allocates a peak transient.
		"region_map_peak": fmt.Sprintf(`def run()
  [1, 2, 3, 4].map do |v|
    tmp = %q
    %q
  end
end`, payloadA, regionPeak),
		// select filtering while the block allocates and reads an outer.
		"region_select_outer": fmt.Sprintf(`def run()
  outer = %q
  [1, 2, 3, 4].select do |v|
    combined = outer + %q
    combined.size > 0
  end
end`, payloadA, regionPeak),
		// reduce whose block cumulatively grows an outer alongside the accumulator.
		"region_reduce_outer_rebind": fmt.Sprintf(`def run()
  side = "s"
  [1, 2, 3, 4].reduce(0) do |accum, v|
    side = side + %q
    accum + v
  end
  %q
end`, payloadA, regionPeak),
		// Nested regions: a map inside an each, mutating an outer from the inner
		// block. The inner region's suffix must re-walk while the outer prefix
		// (including the outer collection) stays memoized.
		"region_nested_map_in_each": fmt.Sprintf(`def run()
  sink = []
  [[1, 2], [3, 4]].each do |row|
    row.map do |v|
      sink.push(%q)
      %q
    end
  end
end`, payloadA, regionPeak),
		// group_by whose block cumulatively grows an outer var while keying.
		"region_group_by_outer": fmt.Sprintf(`def run()
  tag = "t"
  [1, 2, 3, 4, 5, 6].group_by do |v|
    tag = tag + %q
    v %% 2
  end
  tag.size
end`, payloadA),
		// hash.each mutating an outer collection from the block body.
		"region_hash_each_outer_push": fmt.Sprintf(`def run()
  buf = []
  {a: 1, b: 2, c: 3}.each do |k, v|
    buf.push(%q)
    %q
  end
end`, payloadA, regionPeak),
		// Block body calls a script helper that cumulatively grows an outer local.
		// The helper's call frame is pushed inside the region, so acquireCallEnv
		// must mark it epoch-neutral before its pre-push argument binding; a peak
		// check follows so a stale prefix (or an undercharged grow) drifts the
		// threshold.
		"region_each_calls_helper": fmt.Sprintf(`def grow(acc)
  acc + %q
end

def run()
  acc = "seed"
  [1, 2, 3, 4].each do |v|
    acc = grow(acc)
    %q
  end
end`, payloadA, regionPeak),
		// Scalar accumulator in a block: the shape bumpEpochUnlessScalarRebind
		// makes bump-free. The rebind resolves past the region boundary to a prefix
		// scope, but old and new are both compact scalars, so the prefix's byte
		// total is unchanged and the memo may legally survive. The threshold must
		// still match the unmemoized run exactly -- suppressing a bump that did
		// change the estimate would drift it.
		"region_each_scalar_accumulator": fmt.Sprintf(`def run()
  total = 0
  [1, 2, 3, 4].each do |v|
    total = total + v
    %q
  end
  total
end`, regionPeak),
		// Scalar-to-payload transition inside a block. The binding starts compact
		// and is rebound to a large string, so the suppression must NOT apply: a
		// skipped bump here leaves the prefix missing the payload's bytes, which is
		// an undercount and shifts the memoized threshold below the unmemoized one.
		"region_each_scalar_then_payload": fmt.Sprintf(`def run()
  slot = 0
  [1, 2, 3, 4].each do |v|
    slot = %q
    %q
  end
  slot.size
end`, payloadA, regionPeak),
		// Payload-to-scalar transition: the reverse direction. Dropping the payload
		// shrinks the reachable graph, so a skipped bump would leave the memo
		// counting removed bytes and drift the threshold the other way.
		"region_each_payload_then_scalar": fmt.Sprintf(`def run()
  slot = %q
  [1, 2, 3, 4].each do |v|
    slot = 0
    %q
  end
  slot
end`, payloadA, regionPeak),
		// Scalar accumulator that promotes to a bignum. A bignum-backed Int carries
		// a heap payload, so committableScalar excludes it and the rebind must bump
		// once the value grows past the compact representation.
		"region_each_scalar_to_bignum": fmt.Sprintf(`def run()
  total = 1
  [1, 2, 3, 4].each do |v|
    total = total * 100000000000000000000
    %q
  end
  total
end`, regionPeak),
		// The same scalar rebind outside any block, exercising the ordinary
		// (non-region) base-walk memo rather than the region prefix memo.
		"scalar_rebind_no_region": fmt.Sprintf(`def run()
  total = 0
  i = 0
  while i < 4
    total = total + i
    i = i + 1
  end
  %q
end`, regionPeak),
		// A closure created in the block body captures the block scope and escapes
		// into an outer (prefix) binding, then a block-local it closes over grows.
		// Once the scope is reachable from the memoized prefix its later writes must
		// still be charged; if capture failed to revoke the scope's epoch neutrality
		// the suffix walk would deduplicate it against the prefix and miss the
		// growth, undercounting here.
		"region_each_escaping_closure": fmt.Sprintf(`def run()
  holder = nil
  [1, 2, 3, 4].each do |v|
    x = "s"
    holder = -> { x }
    x = %q
    %q
  end
  holder.call.size
end`, payloadA, regionPeak),
		// A helper called from the block binds a closure default that captures the
		// helper's own call frame during pre-push argument binding, then escapes it
		// into an outer (prefix) array before growing another frame local. The
		// capture must revoke the frame's neutrality AND survive the push: if
		// pushEnv re-neutralized the frame after the pre-push revocation, the later
		// local growth would skip its epoch bump while the frame is reachable from
		// the memoized prefix, so the suffix walk would deduplicate it and miss the
		// growth — undercounting here.
		"region_helper_closure_default_escape": fmt.Sprintf(`def helper(sink, seed, f = -> { seed })
  sink.push(f)
  grown = seed + %q
  %q
end

def run()
  sink = []
  [1, 2, 3, 4].each do |v|
    helper(sink, "seed")
    %q
  end
  sink.size
end`, payloadA, regionPeak, regionPeak),
	}
	maps.Copy(scripts, regionScripts)
	return scripts
}()

func memoQuotaRun(t *testing.T, source string, quota int) error {
	t.Helper()
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 20, MemoryQuotaBytes: quota}, source)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	return err
}

// memoMinimalPassingQuota bisects the smallest memory quota the script passes
// under. Pass/fail is monotone in the quota (every check compares one estimate
// against it), so the boundary is exact and any estimate change anywhere in
// the run moves it.
func memoMinimalPassingQuota(t *testing.T, source string) int {
	t.Helper()
	const upper = 1 << 22
	if err := memoQuotaRun(t, source, upper); err != nil {
		t.Fatalf("script failed under generous quota: %v", err)
	}
	if err := memoQuotaRun(t, source, 1); err == nil {
		t.Fatalf("script passed under 1-byte quota; bisection has no boundary")
	} else if !strings.Contains(err.Error(), "memory quota exceeded") {
		t.Fatalf("expected memory quota error at 1 byte, got: %v", err)
	}
	lo, hi := 2, upper
	for lo < hi {
		mid := lo + (hi-lo)/2
		if memoQuotaRun(t, source, mid) == nil {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// TestMemoryQuotaThresholdsUnchangedByMemo bisects the exact pass/fail memory
// quota for every mutator script with the base-walk memo enabled and disabled
// and requires identical thresholds: the memo may only change how fast the
// estimate is computed, never its value at any check site. This is the
// end-to-end mutation matrix — a missed epoch bump on any interpreter mutation
// path shifts the memoized threshold below the unmemoized one and fails here.
func TestMemoryQuotaThresholdsUnchangedByMemo(t *testing.T) {
	for name, source := range memoQuotaThresholdScripts {
		t.Run(name, func(t *testing.T) {
			memoized := memoMinimalPassingQuota(t, source)

			baseWalkCacheDisabled.Store(true)
			unmemoized := memoMinimalPassingQuota(t, source)
			baseWalkCacheDisabled.Store(false)

			if memoized != unmemoized {
				t.Fatalf("quota threshold drifted: memoized=%d unmemoized=%d", memoized, unmemoized)
			}
		})
	}
}
