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
	// hashBuildAccumulator's storedValues bookkeeping map (a map[string]Value): the
	// map bucket overhead, a string header for the key (a distinct header from the
	// output map's, even though both alias the same backing key bytes), and the
	// inline Value the entry stores. The key payload bytes and the stored Value's
	// own payload are shared with the output map and already charged there, so they
	// are not re-counted here.
	estimatedTrackingMapEntryBytes = estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes
	// estimatedRefTrackingEntryBytes is the live footprint of one entry in the
	// hashBuildAccumulator's valueRefs bookkeeping map (a map[backingID]int): the
	// map bucket overhead plus the inline backingID key and the int reference count.
	// It is folded into a backing's charged bytes when its reference count first
	// rises from zero and credited back when it falls to zero, so the O(distinct
	// backings) tracking map is itself accounted against the quota.
	estimatedRefTrackingEntryBytes = estimatedMapEntryBytes + int(unsafe.Sizeof(backingID{})) + int(unsafe.Sizeof(int(0)))
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
	// storedValues records, per key currently present in the output map, the value
	// stored for that key. add consults it so a key written more than once (a merge
	// conflict block, or a transform_keys block that maps several input keys onto
	// the same output key) is charged as a replacement: the old value's backings are
	// released and the new value's backings are charged, rather than a fresh entry.
	// Without this, built would grow monotonically by a full entry per write and
	// over-count values the map no longer holds, falsely tripping the quota on valid
	// colliding builds.
	//
	// This map is itself live memory held alongside the output hash while the build
	// runs, with the same cardinality as the output (one entry per distinct output
	// key). add charges its structural footprint into built as each distinct key is
	// first inserted (see estimatedTrackingMapEntryBytes), so a block-driven
	// transform that keeps many distinct keys cannot allocate an unaccounted O(n)
	// bookkeeping map past the quota.
	storedValues map[string]Value
	// valueRefs reference-counts the payload backings (string bytes, slice and map
	// structures) that the output map's values keep live but the baseline does not
	// already account for. Charging a value adds its newly-live backings to built
	// and increments their refcount; releasing a replaced value decrements those
	// refcounts and subtracts only the backings that drop to zero references — those
	// no longer reachable from any live slot.
	//
	// This reference counting is what keeps built from ever dropping below the output
	// map's true live footprint on a replacement. A naive net-swap delta computed
	// from the persistent estimator's stateful dedup under-counts whenever the new
	// value shares a backing with the value it replaces (a merge block returning the
	// `old` value, or wrapping it in a fresh container): the estimator dedups that
	// shared payload to zero for the new value, so subtracting the old value's full
	// recorded bytes would drop built by a payload the map still holds, letting later
	// inserts materialize past the quota. Refcounting only releases a backing once no
	// live slot references it, so a still-reachable payload is never subtracted.
	valueRefs map[backingID]int
}

// backingID identifies a heap backing the output map's values keep live. The kind
// tag keeps a string payload, a slice backing, and a map backing distinct even
// when their pointers happen to coincide (an empty slice and an interned string,
// say), so reference counts never alias across structurally different backings.
type backingID struct {
	kind ValueKind
	ptr  uintptr
	// length disambiguates string backings that share a data pointer but cover
	// different byte spans (a substring of a larger string), mirroring how the
	// estimator's stringIdentity keys on pointer and length together.
	length int
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
// header and payload, the value slot, and the value's payload backings, exactly
// as the estimator counts them for a finished map. Payload backings already
// reachable from the baseline are not re-counted; backings shared across the
// output's own values are counted once and reference-counted so they survive a
// replacement of one sharing slot.
//
// A repeated write for a key (the output map already holds it) is a replacement:
// the bucket, key header, key payload, and value slot already exist, so
// re-charging them would over-count. The map only swaps the stored value, so add
// releases the old value's payload backings and charges the new value's,
// subtracting only the backings the swap leaves unreachable. This keeps built a
// measure of the map's live footprint rather than a monotonic sum of every value
// ever written, and — critically — never drops built below the live footprint
// when the new value shares a backing with the value it replaces (a merge block
// returning the old value). It matters for merge conflict blocks and for
// transform_keys blocks that collapse many input keys onto one output key, where
// monotonic accumulation would falsely trip the quota on valid builds.
//
// A first write also charges the storedValues bookkeeping map's own footprint:
// its empty-map overhead on the very first insert, then one tracking entry per
// distinct key. That map is live alongside the output for the build's duration, so
// counting it keeps a transform that retains many distinct keys from allocating an
// O(n) tracking map outside the quota.
func (acc *hashBuildAccumulator) add(key string, val Value) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}

	if old, replaced := acc.storedValues[key]; replaced {
		// Charge the new payload before releasing the old so a backing the two share
		// keeps a positive refcount throughout: its bytes are never momentarily
		// subtracted and re-added, which keeps the net delta exact when the new value
		// simply returns or wraps the old. chargeValuePayload returns a positive
		// footprint delta and releaseValuePayload a non-positive one, so their sum is
		// the signed net change to the output map's live footprint.
		added := acc.chargeValuePayload(val)
		freed := acc.releaseValuePayload(old)
		acc.storedValues[key] = val
		return acc.charge(added + freed)
	}

	tracking := estimatedTrackingMapEntryBytes
	if acc.storedValues == nil {
		acc.storedValues = make(map[string]Value)
		tracking = saturatingAdd(tracking, estimatedMapBaseBytes)
	}
	acc.storedValues[key] = val
	entry := estimatedMapEntryBytes + estimatedStringHeaderBytes + acc.est.stringPayloadSize(key)
	payload := saturatingAdd(estimatedValueBytes, acc.chargeValuePayload(val))
	return acc.charge(saturatingAdd(saturatingAdd(entry, payload), tracking))
}

// charge applies a signed footprint delta to built and rejects the build if the
// running total then exceeds the quota. A replacement releases the old value's
// payload and charges the new one, so delta can be negative; saturatingAdd guards
// only non-negative operands, so a negative delta is added directly (it can only
// shrink built, never overflow upward) and clamped at zero, while a non-negative
// delta saturates against MaxInt.
func (acc *hashBuildAccumulator) charge(delta int) error {
	if delta < 0 {
		acc.built += delta
		if acc.built < 0 {
			acc.built = 0
		}
	} else {
		acc.built = saturatingAdd(acc.built, delta)
	}
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

// chargeValuePayload reference-counts the payload backings val keeps live that the
// baseline does not already hold, returning the (non-negative) bytes that became
// newly live: the backings whose refcount rose from zero. The value slot itself is
// charged by the caller; this counts only what hangs off the slot.
func (acc *hashBuildAccumulator) chargeValuePayload(val Value) int {
	return acc.walkValuePayload(val, +1, make(map[backingID]struct{}))
}

// releaseValuePayload reference-counts down the payload backings of a value being
// removed from the output map, returning the (non-positive) footprint delta: the
// negated bytes of the backings whose refcount fell back to zero. A backing still
// referenced by another live slot keeps a positive count and contributes nothing,
// so a shared payload is never subtracted while it remains reachable.
func (acc *hashBuildAccumulator) releaseValuePayload(val Value) int {
	return acc.walkValuePayload(val, -1, make(map[backingID]struct{}))
}

// walkValuePayload traverses the payload reachable from val and adjusts the
// reference count of each heap backing by sign (+1 to charge, -1 to release). It
// returns the net bytes by which the live footprint changed: the sum of backings
// crossing from zero to one reference (charged) minus those crossing from one to
// zero (freed).
//
// visited records the backings already touched during this single charge or
// release walk so each is reference-counted at most once per top-level value. A
// stored value's graph can reach the same backing through several paths, or even
// form a Go-level cycle (a = [0]; a[0] = a, or obj.Hash()[k] = obj built by
// in-place index assignment), which a transform block can return. Without the
// per-walk visited set a cyclic backing would either recurse forever or, with an
// ad-hoc "already counted, skip" guard, charge and release asymmetrically: the
// self-edge keeps the count positive so release never recurses and the backing
// leaks. Deduplicating by visited makes charge and release mirror-symmetric for
// every shape -- a self-cycle, a mutual cycle a<->b, or a cycle nested under a
// fresh container -- so a cyclic value is charged exactly once and released fully,
// leaving no orphaned valueRefs entry behind.
//
// Backings already accounted by the baseline are skipped: the persistent
// estimator's seen-sets, populated when the accumulator snapshotted the call
// roots, are consulted read-only so a payload shared with the receiver or another
// root is never double-counted here. String, slice, and map backings are
// reference-counted by identity so a value shared across output slots is counted
// once and survives the replacement of any single sharing slot.
//
// Object-identity kinds whose payload the estimator walks through nested
// environments (instances, classes, blocks) are charged once as a permanent
// contribution rather than reference-counted: enumerating and releasing their
// nested backings would duplicate the entire estimator traversal. Charging them
// permanently can only over-count an evicted closure or instance, never
// under-count a live one, which keeps the sandbox-containment guarantee intact for
// the exotic case of such a value being stored in a transform output and later
// replaced.
func (acc *hashBuildAccumulator) walkValuePayload(val Value, sign int, visited map[backingID]struct{}) int {
	switch val.Kind() {
	case KindString, KindSymbol:
		str := val.String()
		if len(str) == 0 {
			return 0
		}
		ptr := uintptr(unsafe.Pointer(unsafe.StringData(str)))
		if acc.baseHasString(str, ptr) {
			return 0
		}
		id := backingID{kind: KindString, ptr: ptr, length: len(str)}
		if _, seen := visited[id]; seen {
			return 0
		}
		visited[id] = struct{}{}
		return acc.adjustRef(id, sign, estimatedStringHeaderBytes+len(str))
	case KindArray:
		return acc.walkSlicePayload(val.Array(), sign, visited)
	case KindHash, KindObject:
		return acc.walkHashPayload(val.Hash(), sign, visited)
	default:
		// Scalars contribute only their value slot, charged by the caller. Instances,
		// classes, blocks, functions, and builtins are charged permanently below.
		return acc.chargeOpaquePayload(val, sign)
	}
}

// walkSlicePayload reference-counts a slice backing and recurses into its
// elements. The backing's structural bytes (its base plus one value slot per
// capacity slot) are counted with the slice's identity so they live and die with
// it; element payloads beyond their slots are reference-counted independently so a
// nested string shared with another slot survives this slice's release.
//
// A backing already visited in this walk (a shared sub-graph reached twice, or a
// cycle pointing back to itself) is skipped without re-adjusting its count or
// recursing, so the structural bytes and every element are charged or released
// exactly once per top-level value regardless of how many internal paths reach it.
func (acc *hashBuildAccumulator) walkSlicePayload(values []Value, sign int, visited map[backingID]struct{}) int {
	id := backingID{kind: KindArray, ptr: sliceBackingIdentity(values)}
	if id.ptr != 0 && acc.baseHasSlice(id.ptr) {
		return 0
	}
	structural := saturatingAdd(estimatedSliceBaseBytes, saturatingMul(cap(values), estimatedValueBytes))
	total := 0
	if id.ptr == 0 {
		// A zero-capacity slice has no shared backing to reference-count; charge its
		// (empty) structural bytes directly so the sign still applies.
		total = sign * structural
	} else {
		if _, seen := visited[id]; seen {
			return 0
		}
		visited[id] = struct{}{}
		total = acc.adjustRef(id, sign, structural)
	}
	for _, elem := range values {
		total += acc.walkValuePayload(elem, sign, visited)
	}
	return total
}

// walkHashPayload reference-counts a map backing and recurses into its values. The
// backing's structural bytes (its base plus one entry per key, with key headers
// and payloads) are counted with the map's identity; value payloads beyond their
// slots are reference-counted independently.
//
// As in walkSlicePayload, a backing already visited in this walk is skipped, so a
// map reachable by several paths or one that points back at itself (a self-cycle
// built by obj.Hash()[k] = obj) is charged and released exactly once.
func (acc *hashBuildAccumulator) walkHashPayload(values map[string]Value, sign int, visited map[backingID]struct{}) int {
	ptr := reflect.ValueOf(values).Pointer()
	if ptr != 0 && acc.baseHasMap(ptr) {
		return 0
	}
	structural := estimatedMapBaseBytes + len(values)*estimatedMapEntryBytes
	for key := range values {
		structural += estimatedStringHeaderBytes + len(key)
	}
	total := 0
	if ptr == 0 {
		total = sign * structural
	} else {
		id := backingID{kind: KindHash, ptr: ptr}
		if _, seen := visited[id]; seen {
			return 0
		}
		visited[id] = struct{}{}
		total = acc.adjustRef(id, sign, structural)
	}
	for _, elem := range values {
		total += acc.walkValuePayload(elem, sign, visited)
	}
	return total
}

// chargeOpaquePayload charges an object-identity value (instance, class, block,
// function, builtin) as a permanent contribution the first time it is stored,
// measured by the estimator so its nested footprint is counted exactly once. It is
// never released: such values are not reference-counted, so a replacement that
// evicts one keeps its charge, which can only over-count and so preserves the
// never-under-count guarantee. Functions and builtins measure as zero payload, so
// they contribute nothing.
func (acc *hashBuildAccumulator) chargeOpaquePayload(val Value, sign int) int {
	if sign < 0 {
		return 0
	}
	payload := acc.est.value(val) - estimatedValueBytes
	if payload <= 0 {
		return 0
	}
	return payload
}

// adjustRef changes the reference count of a backing by sign and returns the
// signed bytes by which the live footprint changed: +(bytes + tracking) when the
// count rises from zero (the backing becomes live and gains a valueRefs entry),
// -(bytes + tracking) when it falls back to zero (the backing is freed and its
// entry removed), 0 otherwise. The tracking term accounts for the valueRefs map's
// own per-backing footprint so the O(distinct backings) bookkeeping map is charged
// against the quota alongside the payload it tracks.
func (acc *hashBuildAccumulator) adjustRef(id backingID, sign, bytes int) int {
	if acc.valueRefs == nil {
		acc.valueRefs = make(map[backingID]int)
	}
	prev := acc.valueRefs[id]
	count := prev + sign
	if count < 0 {
		count = 0
	}
	if count == 0 {
		delete(acc.valueRefs, id)
	} else {
		acc.valueRefs[id] = count
	}
	switch {
	case prev == 0 && count > 0:
		return saturatingAdd(bytes, estimatedRefTrackingEntryBytes)
	case prev > 0 && count == 0:
		return -saturatingAdd(bytes, estimatedRefTrackingEntryBytes)
	default:
		return 0
	}
}

// baseHasString reports whether a string backing was already counted by the
// baseline. It consults the persistent estimator's string seen-set read-only, so
// a payload shared with the receiver or another call root (already folded into
// base) is not re-counted by the reference-counting walk.
func (acc *hashBuildAccumulator) baseHasString(str string, ptr uintptr) bool {
	_, seen := acc.est.seenStrings[stringIdentity{ptr: ptr, len: len(str)}]
	return seen
}

// baseHasSlice reports whether a slice backing was already counted by the
// baseline, consulting the persistent estimator's slice seen-set read-only. The
// seen-set keys on the same sliceBackingIdentity the estimator uses, so the two
// views agree on which backings the base already holds.
func (acc *hashBuildAccumulator) baseHasSlice(ptr uintptr) bool {
	_, seen := acc.est.seenSlices[ptr]
	return seen
}

// baseHasMap reports whether a map backing was already counted by the baseline,
// consulting the persistent estimator's map seen-set read-only.
func (acc *hashBuildAccumulator) baseHasMap(ptr uintptr) bool {
	_, seen := acc.est.seenMaps[ptr]
	return seen
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
