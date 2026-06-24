package runtime

import (
	"fmt"
	"math"
	"reflect"
	"unsafe"
)

const (
	estimatedValueBytes        = int(unsafe.Sizeof(Value{}))
	estimatedStringHeaderBytes = 16
	estimatedSliceBaseBytes    = 24
	estimatedMapBaseBytes      = 48
	estimatedMapEntryBytes     = 32
	estimatedEnvBytes          = int(unsafe.Sizeof(Env{}))
	estimatedInstanceBytes     = 16
	estimatedBlockBytes        = 24
	estimatedCallFrameBytes    = 48
	estimatedModuleContextSize = 24
	// estimatedTrackingMapEntryBytes is the live footprint of one entry in the
	// hashBuildAccumulator's keyValueBytes bookkeeping map (a map[string]int): the
	// map bucket overhead, a string header for the key (a distinct header from the
	// output map's, even though both alias the same backing key bytes), and the
	// inline int value. The key payload bytes are shared with the output map's key
	// and already charged there, so they are not re-counted here.
	estimatedTrackingMapEntryBytes = estimatedMapEntryBytes + estimatedStringHeaderBytes + int(unsafe.Sizeof(int(0)))
)

type memoryEstimator struct {
	seenFrozen    *Env
	seenEnvs      map[*Env]struct{}
	seenMaps      map[uintptr]struct{}
	seenSlices    map[uintptr]struct{}
	seenStrings   map[stringIdentity]struct{}
	seenClasses   map[*ClassDef]struct{}
	seenInstances map[*Instance]struct{}
	seenBlocks    map[*Block]struct{}
}

type stringIdentity struct {
	ptr uintptr
	len int
}

func newMemoryEstimator() *memoryEstimator {
	return &memoryEstimator{}
}

func (est *memoryEstimator) reset() {
	est.seenFrozen = nil
	clear(est.seenEnvs)
	clear(est.seenMaps)
	clear(est.seenSlices)
	clear(est.seenStrings)
	clear(est.seenClasses)
	clear(est.seenInstances)
	clear(est.seenBlocks)
}

func (exec *Execution) memoryEstimatorForCheck() *memoryEstimator {
	est := &exec.memoryEst
	est.reset()
	return est
}

func (exec *Execution) checkMemory() error {
	return exec.checkMemoryWith()
}

func (exec *Execution) checkMemoryWith(extras ...Value) error {
	if exec.memoryQuota <= 0 {
		return nil
	}

	used := exec.estimateMemoryUsage(extras...)
	if used > exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, exec.memoryQuota)
	}
	return nil
}

func (exec *Execution) checkMemoryWithCallRoots(receiver Value, args []Value, kwargs map[string]Value, block Value) error {
	if exec.memoryQuota <= 0 {
		return nil
	}

	used := exec.estimateMemoryUsageForCallRoots(receiver, args, kwargs, block)
	if used > exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, exec.memoryQuota)
	}
	return nil
}

// checkProjectedStringBytes rejects allocations that would exceed the memory
// quota before the string is built. Builtins that grow a string by a
// user-controlled amount (such as the padding helpers) use it to fail fast
// instead of materializing a huge buffer that the post-call check would only
// catch after the allocation already happened. payloadBytes is the byte length
// of the string that would be produced.
func (exec *Execution) checkProjectedStringBytes(payloadBytes int) error {
	if exec.memoryQuota <= 0 {
		return nil
	}

	est := exec.memoryEstimatorForCheck()
	used := exec.estimateMemoryUsageBase(est)
	est.reset()

	used = saturatingAdd(used, estimatedValueBytes+estimatedStringHeaderBytes)
	used = saturatingAdd(used, payloadBytes)
	if used > exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, exec.memoryQuota)
	}
	return nil
}

// checkProjectedInterpolatedValue rejects an interpolation step that would exceed
// the memory quota before the rendered value is streamed into the builder. It
// charges three things that are all live at the peak of the write: the
// execution's reachable roots (the base), the interpolated value's own footprint,
// and the string the rendering is being streamed into (its header plus
// payloadBytes of rendered output).
//
// The value's footprint matters because the interpolated expression may produce
// a temporary that is not reachable from any environment — a function return, or
// an array/hash literal constructed inline. Such a temporary lives only on the Go
// call stack while WriteStringTo copies its rendering, so estimateMemoryUsageBase
// never sees it, yet it is real memory held alongside the growing builder. Without
// charging it, base+output and base+value could each fit the quota while
// base+value+output exceeds it, letting a huge temporary stream past the limit.
//
// val is charged through the same estimator that walks the base, so a value that
// IS reachable from an environment (an existing variable interpolated directly) is
// deduplicated against the base and contributes only its already-counted footprint
// once, leaving the small-interpolation fast path unchanged.
func (exec *Execution) checkProjectedInterpolatedValue(val Value, payloadBytes int) error {
	if exec.memoryQuota <= 0 {
		return nil
	}

	est := exec.memoryEstimatorForCheck()
	used := exec.estimateMemoryUsageBase(est)
	used = saturatingAdd(used, est.value(val))
	est.reset()

	used = saturatingAdd(used, estimatedValueBytes+estimatedStringHeaderBytes)
	used = saturatingAdd(used, payloadBytes)
	if used > exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, exec.memoryQuota)
	}
	return nil
}

// checkProjectedIntArrayBytes rejects allocations that would exceed the memory
// quota before the array is built. Builtins that preallocate an array of int
// values sized by a user-controlled count (such as the range materialization
// helpers) use it to fail fast instead of reserving a huge backing array that
// the per-element check would only catch after the allocation already happened.
// count is the number of int values the array would hold; each int value
// contributes only the base Value size.
func (exec *Execution) checkProjectedIntArrayBytes(count int) error {
	if exec.memoryQuota <= 0 {
		return nil
	}

	est := exec.memoryEstimatorForCheck()
	used := exec.estimateMemoryUsageBase(est)
	est.reset()

	used = saturatingAdd(used, estimatedValueBytes+estimatedSliceBaseBytes)
	used = saturatingAdd(used, saturatingMul(count, estimatedValueBytes))
	if used > exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, exec.memoryQuota)
	}
	return nil
}

// checkProjectedHashBytes rejects allocations that would exceed the memory quota
// before a derived map is built. Hash transform helpers (such as merge, except,
// compact, and remap_keys) materialize an output map sized to their inputs; for
// large receivers that backing map can dwarf the quota, and the statement-level
// check would only observe it after the allocation already happened. count is the
// number of entries the output map would hold. Each entry contributes the map's
// per-entry overhead plus a key header and a value slot; the keys and values are
// references shared with the receiver and arguments, whose payloads are already
// counted in the call-root usage below, so only the new map's structural
// footprint is projected here.
//
// The receiver, arguments, and block are the call roots that hold the transform's
// inputs alive while the output map is built. When the transform runs on an
// ephemeral receiver or argument (for example a hash literal or capability return
// invoked immediately, `{ ... }.compact` or `h.merge(load_defaults)`), those
// inputs are reachable only through these roots and are not part of
// estimateMemoryUsageBase, so the projection must count them or it would
// under-report the peak and admit a transform that doubles the live footprint.
// The estimator de-duplicates by backing pointer, so a root that overlaps the
// base (a named local) or another root (`h.merge(h)`) is counted once.
func (exec *Execution) checkProjectedHashBytes(count int, receiver Value, args []Value, kwargs map[string]Value, block Value) error {
	return exec.checkProjectedHashTransformBytes(count, 0, receiver, args, kwargs, block)
}

// checkProjectedHashTransformBytes rejects a map-producing hash transform before
// it allocates either its output map or the sorted-key scratch buffer(s) that
// drive an ordered walk. outputEntries is the number of entries the result map
// would hold; scratchBytes is the heap footprint of the scratch key slices (see
// sortedKeyBufferBytes), which the caller sums per-buffer so the inline-stack-
// buffer threshold is applied to each independently.
//
// The output map's fixed overhead is always charged, even when outputEntries is
// zero: a transform that produces a hash (merge, select, transform_values, ...)
// allocates a real empty map for an empty result. Pure iterators that build no
// map (each, each_key, each_value) use checkProjectedHashWalkBytes instead, which
// omits that overhead.
//
// Both allocations coexist at peak: the scratch list of every key is live while
// the output map fills, so they are charged together against the same call-root
// baseline. The buffered keys alias the receiver's map keys, already counted in
// the call-root usage, so only the scratch slices' headers (not the key bytes)
// are added here. Without this the scratch list -- one header per entry, outside
// the output-map projection -- could allocate past the quota on a large receiver
// before any later check observed it.
func (exec *Execution) checkProjectedHashTransformBytes(outputEntries, scratchBytes int, receiver Value, args []Value, kwargs map[string]Value, block Value) error {
	if exec.memoryQuota <= 0 {
		return nil
	}

	used := exec.projectedHashBaseBytes(receiver, args, kwargs, block)
	perEntry := estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes
	used = saturatingAdd(used, saturatingMul(outputEntries, perEntry))
	used = saturatingAdd(used, scratchBytes)
	if used > exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, exec.memoryQuota)
	}
	return nil
}

// checkProjectedHashWalkBytes rejects a pure hash iterator (each, each_key,
// each_value) before it allocates its sorted-key scratch buffer. These iterators
// return the receiver and build no derived map, so their only allocation beyond
// the live call roots is the scratch key list (see sortedKeyBufferBytes); they
// must not be charged an output map they never create. scratchBytes is the heap
// footprint of that key list. Charging a phantom empty map here would falsely
// reject a quota that exactly fits the receiver, block, and scratch.
func (exec *Execution) checkProjectedHashWalkBytes(scratchBytes int, receiver Value, args []Value, kwargs map[string]Value, block Value) error {
	if exec.memoryQuota <= 0 {
		return nil
	}

	used := saturatingAdd(exec.hashCallRootBytes(receiver, args, kwargs, block), scratchBytes)
	if used > exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, exec.memoryQuota)
	}
	return nil
}

// arrayBuildAccumulator charges the memory of an array assembled element by
// element against the quota without re-walking the whole prefix on each append.
// Builtins that grow a Go-local result slice from values they cannot bound up
// front (such as Array#fill's block form, where each block call can return an
// arbitrarily large value, or Array#filter_map, which keeps arbitrary truthy
// block results) use it so accumulated payloads count toward the quota during
// construction, not only after the builtin returns.
//
// checkProjectedIntArrayBytes is enough for results whose every element is an
// inlined scalar, because there the slot array is the entire allocation. It does
// not account for the payloads reachable from each element (string bytes, nested
// collections), so a fill block returning many quota-sized strings would slip
// past it until the post-call check. This accumulator closes that gap: it keeps
// a baseline of everything live at the start of the build plus a running total of
// each kept element's payload, walking only the new element on each append.
//
// The baseline includes the builtin's live call roots (receiver, args, kwargs,
// block), not just exec's reachable roots. While a builtin runs, those roots are
// held on the Go call stack and are invisible to estimateMemoryUsageBase, yet
// they are still live memory the result accumulates on top of — exactly what the
// pre-call checkCallMemoryRoots charges. Seeding them here keeps the incremental
// check consistent with that pre-call check, so a transform whose receiver or
// captured block already nears the quota cannot accumulate an unbounded result
// that only the post-call check would catch.
type arrayBuildAccumulator struct {
	exec    *Execution
	est     *memoryEstimator
	base    int
	payload int
}

// newArrayBuildAccumulator snapshots the execution's current live memory —
// exec's reachable roots plus the builtin's live call roots (receiver, args,
// kwargs, block) — as the baseline for an incremental array build. It uses its
// own estimator rather than the execution's shared one so nested evaluation (a
// block call, say) cannot reset the seen-set mid-build; that estimator persists
// across add calls so a value aliased by an earlier element or already reachable
// from the baseline is counted once, matching the real shared backing.
//
// Pass the same receiver/args/kwargs/block the builtin received so the baseline
// reflects what checkCallMemoryRoots charged before the call: a nil receiver,
// nil kwargs, and nil block are skipped, mirroring estimateMemoryUsageForCallRoots.
func newArrayBuildAccumulator(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) *arrayBuildAccumulator {
	acc := &arrayBuildAccumulator{exec: exec}
	if exec.memoryQuota <= 0 {
		return acc
	}
	acc.est = newMemoryEstimator()
	acc.base = exec.estimateMemoryUsageBase(acc.est)

	if receiver.Kind() != KindNil {
		acc.base = saturatingAdd(acc.base, acc.est.value(receiver))
	}
	for _, arg := range args {
		acc.base = saturatingAdd(acc.base, acc.est.value(arg))
	}
	for _, kwarg := range kwargs {
		acc.base = saturatingAdd(acc.base, acc.est.value(kwarg))
	}
	if !block.IsNil() {
		acc.base = saturatingAdd(acc.base, acc.est.value(block))
	}
	return acc
}

// add charges a newly appended element and rejects the build if the result's
// projected memory exceeds the quota. backingCap is the capacity of the result's
// backing slice after the append; its slot array is charged from that capacity
// while only the element's payload beyond its slot is added to the running total,
// so the slot is never double counted.
//
// Elements aliased by a baseline root (for example filter_map returning an
// element of its receiver unchanged) are deduplicated by the persistent
// estimator, so their backing is charged once, exactly as the post-call check
// would.
func (acc *arrayBuildAccumulator) add(val Value, backingCap int) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}

	acc.payload = saturatingAdd(acc.payload, acc.est.valuePayload(val))

	backing := saturatingAdd(estimatedValueBytes+estimatedSliceBaseBytes, saturatingMul(backingCap, estimatedValueBytes))
	used := saturatingAdd(saturatingAdd(acc.base, backing), acc.payload)
	if used > acc.exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, acc.exec.memoryQuota)
	}
	return nil
}

// hashBuildAccumulator charges the memory of an output map assembled entry by
// entry against the quota without re-walking the whole map on each insertion.
// Hash transforms whose block returns fresh heap values (transform_values and
// transform_keys, where each block call can yield an arbitrarily large string
// or nested collection) use it so accumulated payloads count toward the quota
// during construction, not only after the builtin returns.
//
// checkProjectedHashBytes alone is enough for blockless transforms whose values
// are references shared with the receiver, because there the output map's
// payloads are already resident and the projection only needs the new map's
// structural slots. It cannot bound a block that synthesizes new values: those
// live solely in the Go-local result map, unreachable from any execution root
// until the builtin returns, so many individually-under-quota results could
// accumulate well past the quota before the post-call check observes them. This
// accumulator closes that gap: it snapshots everything live when the build
// starts (including the call roots, so an ephemeral receiver is counted) and
// then per inserted entry walks only that entry, charging the map bucket, the
// key, and the value's payload the same way the estimator would for the
// finished map.
type hashBuildAccumulator struct {
	exec *Execution
	est  *memoryEstimator
	// base is the live footprint snapshotted when the build started; built is the
	// running byte total charged for the output as it is assembled.
	base  int
	built int
	// keyValueBytes records, per key currently present in the output map, the value
	// bytes charged for that key's stored value. add consults it so a key written
	// more than once (a merge conflict block, or a transform_keys block that maps
	// several input keys onto the same output key) charges a replacement as the net
	// swap of the new value for the old, not as a fresh entry. Without this, built
	// would grow monotonically by a full entry per write and over-count values that
	// the map no longer holds, falsely tripping the quota on valid colliding builds.
	//
	// This map is itself live memory held alongside the output hash while the build
	// runs, with the same cardinality as the output (one entry per distinct output
	// key). add charges its structural footprint into built as each distinct key is
	// first inserted (see estimatedTrackingMapEntryBytes), so a block-driven
	// transform that keeps many distinct keys cannot allocate an unaccounted O(n)
	// bookkeeping map past the quota.
	keyValueBytes map[string]int
}

// newHashBuildAccumulator snapshots the execution's current live memory plus the
// transform's call roots as the baseline for an incremental hash build. It uses
// its own estimator rather than the execution's shared one so nested evaluation
// (a transform block, say) cannot reset the seen-set mid-build; that estimator
// persists across add calls so a value aliased by an earlier entry, a call root,
// or the baseline is counted once, matching the real shared backing. The empty
// map's structural overhead is folded into the baseline so add only charges the
// per-entry growth.
func newHashBuildAccumulator(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) *hashBuildAccumulator {
	acc := newRootSeededHashBuildAccumulator(exec, receiver, args, kwargs, block)
	if acc.exec.memoryQuota > 0 {
		// The output is a single map, so fold its empty-map overhead into the
		// baseline; add then charges only the per-entry growth.
		acc.base = saturatingAdd(acc.base, estimatedValueBytes+estimatedMapBaseBytes)
	}
	return acc
}

// newRootSeededHashBuildAccumulator seeds an accumulator with the live execution
// memory plus the call roots as the baseline, without any output-map overhead.
// newHashBuildAccumulator builds on it and folds in the output map's fixed cost
// so the per-entry charges add only their incremental growth.
func newRootSeededHashBuildAccumulator(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) *hashBuildAccumulator {
	acc := &hashBuildAccumulator{exec: exec}
	if exec.memoryQuota <= 0 {
		return acc
	}
	acc.est = newMemoryEstimator()
	base := exec.estimateMemoryUsageBase(acc.est)
	if receiver.Kind() != KindNil {
		base = saturatingAdd(base, acc.est.value(receiver))
	}
	for _, arg := range args {
		base = saturatingAdd(base, acc.est.value(arg))
	}
	for _, kwarg := range kwargs {
		base = saturatingAdd(base, acc.est.value(kwarg))
	}
	if !block.IsNil() {
		base = saturatingAdd(base, acc.est.value(block))
	}
	acc.base = base
	return acc
}

// reserveScratch folds a fixed scratch allocation into the baseline so it is held
// against the quota for the build's entire lifetime, and rejects the build if the
// reservation alone already overflows. Block-driven hash transforms materialize a
// sorted-key scratch slice (see sortedKeyBufferBytes) that stays live the whole
// time the output map fills, so its bytes coexist with every accumulated entry at
// peak. The up-front projection charges this scratch once before any fresh block
// result exists, but the accumulator's running budget (base + built) does not,
// so without reserving it here the combined output+scratch peak could exceed the
// quota by the scratch size before the post-call check observed it. scratchBytes
// is the heap footprint the caller passed to its projection, so the two views of
// the budget reserve the same bytes.
func (acc *hashBuildAccumulator) reserveScratch(scratchBytes int) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}
	acc.base = saturatingAdd(acc.base, scratchBytes)
	return acc.checkQuota()
}

// add charges a key write to the output map and rejects the build if the
// projected map memory exceeds the quota.
//
// A first write for a key charges a full entry: the bucket overhead, the key
// header and payload, and the value, exactly as the estimator counts them for a
// finished map. The estimator deduplicates keys and values already reachable
// from the baseline or an earlier entry so shared backings are counted once.
//
// A repeated write for a key (the output map already holds it) is a replacement:
// the bucket, key header, and key payload already exist, so re-charging them
// would over-count. The map only swaps the stored value, so add charges just the
// net change between the new value's bytes and the old value's recorded bytes,
// keeping built a measure of the map's live footprint rather than a monotonic sum
// of every value ever written. This matters for merge conflict blocks and for
// transform_keys blocks that collapse many input keys onto one output key, where
// monotonic accumulation would falsely trip the quota on valid builds.
//
// A first write also charges the keyValueBytes bookkeeping map's own footprint:
// its empty-map overhead on the very first insert, then one tracking entry per
// distinct key. That map is live alongside the output for the build's duration, so
// counting it keeps a transform that retains many distinct keys from allocating an
// O(n) tracking map outside the quota.
func (acc *hashBuildAccumulator) add(key string, val Value) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}

	valueBytes := acc.est.value(val)
	if prevBytes, replaced := acc.keyValueBytes[key]; replaced {
		acc.keyValueBytes[key] = valueBytes
		return acc.chargeDelta(valueBytes - prevBytes)
	}

	tracking := estimatedTrackingMapEntryBytes
	if acc.keyValueBytes == nil {
		acc.keyValueBytes = make(map[string]int)
		tracking = saturatingAdd(tracking, estimatedMapBaseBytes)
	}
	acc.keyValueBytes[key] = valueBytes
	entry := estimatedMapEntryBytes + estimatedStringHeaderBytes + acc.est.stringPayloadSize(key)
	return acc.charge(saturatingAdd(saturatingAdd(entry, valueBytes), tracking))
}

func (acc *hashBuildAccumulator) charge(delta int) error {
	acc.built = saturatingAdd(acc.built, delta)
	return acc.checkQuota()
}

// checkQuota rejects the build when the live baseline plus the rebuilt output
// exceeds the quota.
func (acc *hashBuildAccumulator) checkQuota() error {
	used := saturatingAdd(acc.base, acc.built)
	if used > acc.exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, acc.exec.memoryQuota)
	}
	return nil
}

// chargeDelta adjusts built by a signed delta, used when a key replacement swaps
// a stored value for a different-sized one. A negative delta shrinks built toward
// zero (the output map never holds fewer than zero bytes); a positive delta is
// charged like any other growth so a larger replacement can still trip the quota.
func (acc *hashBuildAccumulator) chargeDelta(delta int) error {
	if delta >= 0 {
		return acc.charge(delta)
	}
	acc.built += delta
	if acc.built < 0 {
		acc.built = 0
	}
	return nil
}

// hashCallRootBytes estimates the live footprint a hash transform holds before it
// reserves any output: exec's reachable roots plus the call roots (receiver,
// args, kwargs, block). It excludes the output map's overhead so callers that
// build no derived map (the pure iterators) are not charged a map they never
// allocate; callers that do build one fold the empty-map overhead in themselves.
func (exec *Execution) hashCallRootBytes(receiver Value, args []Value, kwargs map[string]Value, block Value) int {
	est := exec.memoryEstimatorForCheck()
	used := exec.estimateMemoryUsageBase(est)
	if receiver.Kind() != KindNil {
		used = saturatingAdd(used, est.value(receiver))
	}
	for _, arg := range args {
		used = saturatingAdd(used, est.value(arg))
	}
	for _, kwarg := range kwargs {
		used = saturatingAdd(used, est.value(kwarg))
	}
	if !block.IsNil() {
		used = saturatingAdd(used, est.value(block))
	}
	est.reset()

	return used
}

// projectedHashBaseBytes estimates the live footprint a hash transform holds
// before it reserves its output map: the call-root usage plus the empty map's
// structural overhead. maxProjectedHashEntries builds on it so the entry budget
// and the byte check agree on the same baseline. Callers that build no output map
// (the pure iterators) use hashCallRootBytes directly instead.
func (exec *Execution) projectedHashBaseBytes(receiver Value, args []Value, kwargs map[string]Value, block Value) int {
	return saturatingAdd(exec.hashCallRootBytes(receiver, args, kwargs, block), estimatedValueBytes+estimatedMapBaseBytes)
}

// maxProjectedHashEntries returns the largest output-map entry count that
// checkProjectedHashTransformBytes would still accept for the given call roots
// and scratch budget, or math.MaxInt when no memory quota is enforced. Counting
// helpers that must deduplicate keys across inputs (such as the merge union
// count) use it to cap their tracking set at the quota-derived budget: once the
// distinct-key total passes this ceiling the transform is certain to be
// rejected, so they can stop allocating and report an over-budget count instead
// of building a tracking table sized to the rejected result.
//
// scratchBytes is the heap footprint of any sorted-key scratch buffers the
// transform materializes alongside its output (see mergeSortScratchBytes). It is
// subtracted from the byte budget before deriving the entry cap so this ceiling
// agrees with the final checkProjectedHashTransformBytes, which charges the same
// scratch: without it the cap would admit entries the projection's actual budget
// (quota minus base minus scratch) cannot, letting a doomed merge grow its
// dedup set past the bytes the quota allows.
func (exec *Execution) maxProjectedHashEntries(scratchBytes int, receiver Value, args []Value, kwargs map[string]Value, block Value) int {
	if exec.memoryQuota <= 0 {
		return math.MaxInt
	}
	used := saturatingAdd(exec.projectedHashBaseBytes(receiver, args, kwargs, block), scratchBytes)
	if used >= exec.memoryQuota {
		return 0
	}
	perEntry := estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes
	return (exec.memoryQuota - used) / perEntry
}

func (exec *Execution) estimateMemoryUsage(extras ...Value) int {
	est := exec.memoryEstimatorForCheck()

	total := exec.estimateMemoryUsageBase(est)
	for _, extra := range extras {
		total += est.value(extra)
	}

	est.reset()
	return total
}

func (exec *Execution) estimateMemoryUsageForCallRoots(receiver Value, args []Value, kwargs map[string]Value, block Value) int {
	est := exec.memoryEstimatorForCheck()

	total := exec.estimateMemoryUsageBase(est)

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

	est.reset()
	return total
}

func (exec *Execution) estimateMemoryUsageBase(est *memoryEstimator) int {
	total := 0

	total += est.env(exec.root)
	for _, env := range exec.envStack {
		total += est.env(env)
	}
	for _, mod := range exec.modules {
		total += est.value(mod)
	}
	for _, group := range exec.activeTaskGroups {
		total += group.retainedSnapshotMemory(est)
		total += group.retainedResultMemory(est)
	}
	if globals := taskLazyGlobalsFromContext(exec.Context()); globals != nil {
		total += globals.retainedSourceMemory(est)
		total += globals.retainedCloneMemory(est)
	}

	total += len(exec.callStack) * estimatedCallFrameBytes
	total += len(exec.receiverStack) * estimatedValueBytes
	total += len(exec.validatedCapabilityArgs) * estimatedStringHeaderBytes
	for _, method := range exec.validatedCapabilityArgs {
		total += len(method)
	}
	if exec.moduleLoading != nil {
		total += estimatedMapBaseBytes + len(exec.moduleLoading)*estimatedMapEntryBytes
		for name := range exec.moduleLoading {
			total += estimatedStringHeaderBytes + len(name)
		}
	}
	if exec.capabilityContracts != nil {
		total += estimatedMapBaseBytes + len(exec.capabilityContracts)*estimatedMapEntryBytes
	}
	if exec.capabilityContractScopes != nil {
		total += estimatedMapBaseBytes + len(exec.capabilityContractScopes)*estimatedMapEntryBytes
		seenScopes := make(map[*capabilityContractScope]struct{}, len(exec.capabilityContractScopes))
		for _, scope := range exec.capabilityContractScopes {
			if scope == nil {
				continue
			}
			if _, seen := seenScopes[scope]; seen {
				continue
			}
			seenScopes[scope] = struct{}{}
			total += estimatedMapBaseBytes + len(scope.knownBuiltins)*estimatedMapEntryBytes
		}
	}
	if exec.capabilityContractsByName != nil {
		total += estimatedMapBaseBytes + len(exec.capabilityContractsByName)*estimatedMapEntryBytes
		for name := range exec.capabilityContractsByName {
			total += estimatedStringHeaderBytes + len(name)
		}
	}
	total += estimatedSliceBaseBytes + len(exec.moduleLoadStack)*estimatedStringHeaderBytes
	for _, key := range exec.moduleLoadStack {
		total += len(key)
	}
	total += estimatedSliceBaseBytes + len(exec.moduleStack)*estimatedModuleContextSize
	for _, ctx := range exec.moduleStack {
		total += estimatedStringHeaderBytes*3 + len(ctx.key) + len(ctx.path) + len(ctx.root)
	}

	return total
}

func (est *memoryEstimator) env(env *Env) int {
	if env == nil {
		return 0
	}
	if env.frozen {
		// The engine's frozen builtin proto terminates every env chain,
		// so it is revisited on each walk; a single-slot cache replaces
		// the map insert the seen-set would pay per estimate. Frozen
		// envs hold only statically accounted bindings and no parent.
		if est.seenFrozen == env {
			return 0
		}
		est.seenFrozen = env
		return estimatedEnvBytes + staticBindingsBytes(env)
	}
	if _, seen := est.seenEnvs[env]; seen {
		return 0
	}
	if est.seenEnvs == nil {
		est.seenEnvs = make(map[*Env]struct{})
	}
	est.seenEnvs[env] = struct{}{}

	size := estimatedEnvBytes + staticBindingsBytes(env)
	if env.values != nil {
		size += estimatedMapBaseBytes + len(env.values)*estimatedMapEntryBytes
	}
	if len(env.arrayAppendBuffers) > 0 {
		size += estimatedMapBaseBytes + len(env.arrayAppendBuffers)*estimatedMapEntryBytes
		for name, buffer := range env.arrayAppendBuffers {
			size += estimatedStringHeaderBytes + len(name)
			size += est.slice(buffer)
		}
	}
	for i := range int(env.inlineLen) {
		binding := env.inline[i]
		size += len(binding.name)
		size += est.inlineBindingValue(binding.value)
	}
	for name, val := range env.values {
		size += estimatedStringHeaderBytes + len(name)
		size += est.mapBindingValue(val)
	}
	size += est.env(env.parent)
	return size
}

func staticBindingsBytes(env *Env) int {
	if env.statics == nil {
		return 0
	}
	return estimatedMapBaseBytes + int(env.staticBytes)
}

func (est *memoryEstimator) inlineBindingValue(val Value) int {
	if _, ok := lazyValue(val); ok {
		return 0
	}
	return est.valuePayload(val)
}

func (est *memoryEstimator) mapBindingValue(val Value) int {
	if _, ok := lazyValue(val); ok {
		return estimatedValueBytes
	}
	return est.value(val)
}

func (est *memoryEstimator) valuePayload(val Value) int {
	size := est.value(val) - estimatedValueBytes
	if size < 0 {
		return 0
	}
	return size
}

func (est *memoryEstimator) value(val Value) int {
	size := estimatedValueBytes

	switch val.Kind() {
	case KindString, KindSymbol:
		str := val.String()
		size += estimatedStringHeaderBytes
		size += est.stringPayloadSize(str)
	case KindArray:
		size += est.slice(val.Array())
	case KindHash, KindObject:
		size += est.hash(val.Hash())
	case KindClass:
		cl := valueClass(val)
		if cl == nil {
			return size
		}
		if _, seen := est.seenClasses[cl]; seen {
			return size
		}
		if est.seenClasses == nil {
			est.seenClasses = make(map[*ClassDef]struct{})
		}
		est.seenClasses[cl] = struct{}{}
		size += est.hash(cl.ClassVars)
	case KindInstance:
		inst := valueInstance(val)
		if inst == nil {
			return size
		}
		if _, seen := est.seenInstances[inst]; seen {
			return size
		}
		if est.seenInstances == nil {
			est.seenInstances = make(map[*Instance]struct{})
		}
		est.seenInstances[inst] = struct{}{}
		size += estimatedInstanceBytes
		size += est.hash(inst.Ivars)
	case KindBlock:
		blk := valueBlock(val)
		if blk == nil {
			return size
		}
		if _, seen := est.seenBlocks[blk]; seen {
			return size
		}
		if est.seenBlocks == nil {
			est.seenBlocks = make(map[*Block]struct{})
		}
		est.seenBlocks[blk] = struct{}{}
		size += estimatedBlockBytes + estimatedSliceBaseBytes + len(blk.Params)*estimatedStringHeaderBytes
		for _, param := range blk.Params {
			size += estimatedParamBytes(param)
		}
		size += estimatedStringHeaderBytes*3 + len(blk.moduleKey) + len(blk.modulePath) + len(blk.moduleRoot)
		size += est.env(blk.Env)
	case KindFunction, KindBuiltin:
		// Functions and builtins are compile-time/static artifacts for memory quotas.
	}

	return size
}

func estimatedParamBytes(param Param) int {
	size := len(param.Name)
	if param.Target != nil {
		size += estimatedParamTargetBytes(param.Target)
	}
	return size
}

func estimatedParamTargetBytes(target Expression) int {
	switch t := target.(type) {
	case *Identifier:
		return len(t.Name)
	case *DestructureTarget:
		size := 0
		for _, element := range t.Elements {
			size += estimatedParamTargetBytes(element.Target)
		}
		return size
	default:
		return 0
	}
}

func (est *memoryEstimator) stringPayloadSize(str string) int {
	if len(str) == 0 {
		return 0
	}

	key := stringIdentity{
		ptr: uintptr(unsafe.Pointer(unsafe.StringData(str))),
		len: len(str),
	}
	if _, seen := est.seenStrings[key]; seen {
		return 0
	}
	if est.seenStrings == nil {
		est.seenStrings = make(map[stringIdentity]struct{})
	}
	est.seenStrings[key] = struct{}{}
	return len(str)
}

func (est *memoryEstimator) slice(values []Value) int {
	size := estimatedSliceBaseBytes + cap(values)*estimatedValueBytes
	if cap(values) == 0 {
		return size
	}

	// Deduplicate aliased backings — including empty slices that retained
	// capacity, whose cap*estimatedValueBytes backing is real memory and must
	// be counted only once across aliases (e.g. a partition's empty result side
	// shared with another binding). sliceBackingIdentity reads the data pointer
	// directly via unsafe.SliceData, so the identity is stable even under
	// coverage instrumentation; the earlier re-slice form &values[:1][0]
	// intermittently failed to dedup empty slices in the coverage CI job.
	id := sliceBackingIdentity(values)
	if id != 0 {
		if _, seen := est.seenSlices[id]; seen {
			return 0
		}
		if est.seenSlices == nil {
			est.seenSlices = make(map[uintptr]struct{})
		}
		est.seenSlices[id] = struct{}{}
	}

	if len(values) == 0 {
		return size
	}

	for _, val := range values {
		size += est.value(val)
	}
	return size
}

func sliceBackingIdentity(values []Value) uintptr {
	if cap(values) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(unsafe.SliceData(values)))
}

func (est *memoryEstimator) hash(values map[string]Value) int {
	id := reflect.ValueOf(values).Pointer()
	if id != 0 {
		if _, seen := est.seenMaps[id]; seen {
			return 0
		}
		if est.seenMaps == nil {
			est.seenMaps = make(map[uintptr]struct{})
		}
		est.seenMaps[id] = struct{}{}
	}

	size := estimatedMapBaseBytes + len(values)*estimatedMapEntryBytes
	for key, val := range values {
		size += estimatedStringHeaderBytes + len(key)
		size += est.value(val)
	}
	return size
}
