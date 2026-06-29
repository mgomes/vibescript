package runtime

import (
	"fmt"
	"math"
	"reflect"
	"unsafe"

	"github.com/mgomes/vibescript/vibes/value"
)

const (
	estimatedValueBytes        = int(unsafe.Sizeof(Value{}))
	estimatedIntBytes          = int(unsafe.Sizeof(int(0)))
	estimatedRuneBytes         = int(unsafe.Sizeof(rune(0)))
	estimatedStringHeaderBytes = 16
	estimatedSliceBaseBytes    = 24
	estimatedMapBaseBytes      = 48
	estimatedMapEntryBytes     = 32
	estimatedEnvBytes          = int(unsafe.Sizeof(Env{}))
	estimatedInstanceBytes     = 16
	estimatedBlockBytes        = 24
	estimatedBuiltinBytes      = int(unsafe.Sizeof(Builtin{}))
	estimatedCallFrameBytes    = 48
	estimatedModuleContextSize = 24
)

// estimatedHashDataBytes is the heap footprint of the hashData wrapper every
// KindHash value allocates around its entry map to carry Ruby-style default
// metadata (a default value and/or a default proc). The entry map and default
// payloads are charged separately; this is the wrapper struct itself, which the
// estimate would otherwise miss for workloads that retain many small hashes (an
// array of Hash.new or empty literals).
const estimatedHashDataBytes = value.HashDataBytes

const (
	estimatedHashLookupKeyBytes = int(unsafe.Sizeof(value.HashLookupKey{}))
	estimatedHashEntryBytes     = int(unsafe.Sizeof(HashEntry{}))
)

// estimatedMapEntryStructuralBytes is the per-entry structural footprint a
// map[string]Value reserves for one slot regardless of what its key and value
// point at: the bucket overhead, the key string header, and the value slot. It
// is what make(map[string]Value, n) charges per capacity slot beyond the empty
// map's base overhead; the key bytes and value payloads are charged on top by
// whoever inserts them. The hash projections and the incremental build
// accumulator share this constant so the up-front and running budgets reserve
// the same per-entry structure.
const estimatedMapEntryStructuralBytes = estimatedMapEntryBytes + estimatedStringHeaderBytes + estimatedValueBytes

type memoryEstimator struct {
	seenFrozen    *Env
	seenEnvs      map[*Env]struct{}
	seenMaps      map[uintptr]struct{}
	seenHashData  map[uintptr]struct{}
	seenSlices    map[uintptr]struct{}
	seenStrings   map[stringIdentity]struct{}
	seenClasses   map[*ClassDef]struct{}
	seenInstances map[*Instance]struct{}
	seenBlocks    map[*Block]struct{}
	seenBuiltins  map[*Builtin]struct{}

	// journal, when non-nil, records every seen-set entry a walk newly inserts so
	// the walk can be rolled back to the estimator's prior state. It backs the
	// per-call charged-root probe (see blockBindCharge.begin), which measures a
	// transient root's marginal footprint against the persistent call-root estimator
	// without permanently committing that root: committing it would let a later
	// root that reuses the transient's freed backing be falsely deduplicated, an
	// under-count that could escape the memory quota. A walk records nothing when
	// journal is nil, which is the common path.
	journal *estimatorJournal
}

// estimatorJournal records the seen-set keys a single probe walk inserts so the
// probe can be undone, restoring the estimator to its pre-probe state. Each slice
// holds only the keys this probe added (entries the estimator already contained
// before the probe are not recorded and stay committed), so rollback removes
// exactly the probe's own additions and is O(values walked by the probe). The
// single-slot frozen cache is not journaled per-write; probe captures its
// pre-probe value and rollback restores it directly.
type estimatorJournal struct {
	envs      []*Env
	maps      []uintptr
	hashData  []uintptr
	slices    []uintptr
	strings   []stringIdentity
	classes   []*ClassDef
	instances []*Instance
	blocks    []*Block
	builtins  []*Builtin
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
	est.journal = nil
	clear(est.seenEnvs)
	clear(est.seenMaps)
	clear(est.seenHashData)
	clear(est.seenSlices)
	clear(est.seenStrings)
	clear(est.seenClasses)
	clear(est.seenInstances)
	clear(est.seenBlocks)
	clear(est.seenBuiltins)
}

// probe walks val against the estimator's current seen-set to measure val's
// marginal footprint (the bytes not already deduplicated against everything the
// estimator has committed), then rolls the estimator back to its pre-probe state
// so nothing val touched is retained. It backs the per-call charged-root probe in
// blockBindCharge.begin, where a transient root (a reduce accumulator that changes
// every call) must deduplicate against the committed call roots (the receiver)
// without being committed itself -- so the NEXT call's accumulator cannot falsely
// deduplicate against this one's possibly-freed backing.
func (est *memoryEstimator) probe(val Value) int {
	prevFrozen := est.seenFrozen
	est.journal = &estimatorJournal{}
	journal := est.journal
	size := est.value(val)
	est.journal = nil
	journal.rollback(est, prevFrozen)
	return size
}

// rollback removes from est exactly the seen-set entries the journal recorded,
// restoring the single-slot frozen cache to the value it held before the probe.
func (j *estimatorJournal) rollback(est *memoryEstimator, prevFrozen *Env) {
	est.seenFrozen = prevFrozen
	for _, env := range j.envs {
		delete(est.seenEnvs, env)
	}
	for _, id := range j.maps {
		delete(est.seenMaps, id)
	}
	for _, id := range j.hashData {
		delete(est.seenHashData, id)
	}
	for _, id := range j.slices {
		delete(est.seenSlices, id)
	}
	for _, key := range j.strings {
		delete(est.seenStrings, key)
	}
	for _, cl := range j.classes {
		delete(est.seenClasses, cl)
	}
	for _, inst := range j.instances {
		delete(est.seenInstances, inst)
	}
	for _, blk := range j.blocks {
		delete(est.seenBlocks, blk)
	}
	for _, builtin := range j.builtins {
		delete(est.seenBuiltins, builtin)
	}
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

func (exec *Execution) checkMemoryWithCallRoots(callee, receiver Value, args []Value, kwargs map[string]Value, block Value) error {
	if exec.memoryQuota <= 0 {
		return nil
	}

	used := exec.estimateMemoryUsageForCallRoots(callee, receiver, args, kwargs, block)
	if used > exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, exec.memoryQuota)
	}
	return nil
}

// checkAccumulatorWithCallRoots rejects a fold step whose running accumulator,
// together with the builtin's live call roots and any extra Go-local values that
// coexist with it at the step's peak, would exceed the memory quota. Builtins
// that grow a single Go-local accumulator value from a non-rooted receiver —
// Array#sum building a string or array total — use it instead of the plain
// checkMemoryWith(accumulator). The receiver, args, and block stay live on the Go
// call stack for the builtin's whole run yet are invisible to
// estimateMemoryUsageBase, so a check that charged only the accumulator could
// admit a peak (call roots + accumulator) that exceeds the quota until the
// builtin returns. Charging the accumulator and the call roots through one
// deduplicating estimator keeps the running check consistent with the pre-call
// checkCallMemoryRoots: an accumulator that aliases the receiver or an argument
// is counted once, matching the real shared backing.
//
// liveExtras are additional values that are live on the Go call stack alongside
// the new accumulator at the step's allocation peak but are not reachable from
// any call root. Array#sum passes both the prior total and the contribution it
// just produced: arraySumAdd builds the next accumulator from a fresh copy of the
// old total and the contribution, so the old total, the contribution, and the new
// accumulator all coexist at the peak. The prior total is the critical case once
// it has grown across iterations into a large string or array reachable only from
// that Go-local — the base walk never sees it. Without charging both extras, a
// quota above call roots + new accumulator but below call roots + old total +
// contribution + new accumulator would admit a step whose true peak exceeds the
// limit. Each extra is charged through the same deduplicating estimator, so an
// extra that aliases a receiver element or the accumulator itself is counted once,
// matching the real shared backing.
func (exec *Execution) checkAccumulatorWithCallRoots(accumulator, receiver Value, args []Value, kwargs map[string]Value, block Value, liveExtras ...Value) error {
	if exec.memoryQuota <= 0 {
		return nil
	}

	est := exec.memoryEstimatorForCheck()
	used := exec.estimateMemoryUsageBase(est)
	used = saturatingAdd(used, est.value(accumulator))
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
	for _, extra := range liveExtras {
		used = saturatingAdd(used, est.value(extra))
	}
	est.reset()

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

func (exec *Execution) checkProjectedStringBytesWithCallRoots(payloadBytes int, receiver Value, args []Value, kwargs map[string]Value, block Value) error {
	if exec.memoryQuota <= 0 {
		return nil
	}

	used := exec.estimateMemoryUsageForCallRoots(NewNil(), receiver, args, kwargs, block)
	used = saturatingAdd(used, estimatedValueBytes+estimatedStringHeaderBytes)
	used = saturatingAdd(used, payloadBytes)
	if used > exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, exec.memoryQuota)
	}
	return nil
}

func (exec *Execution) checkProjectedStringBytesAndScratchWithCallRoots(payloadBytes, scratchBytes int, receiver Value, args []Value, kwargs map[string]Value, block Value) error {
	if exec.memoryQuota <= 0 {
		return nil
	}

	used := exec.estimateMemoryUsageForCallRoots(NewNil(), receiver, args, kwargs, block)
	used = saturatingAdd(used, scratchBytes)
	used = saturatingAdd(used, estimatedValueBytes+estimatedStringHeaderBytes)
	used = saturatingAdd(used, payloadBytes)
	if used > exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, exec.memoryQuota)
	}
	return nil
}

// checkProjectedValueRendering rejects a step that renders a value into a fresh
// string (or streams it into a builder) before that string is built, when the
// peak would exceed the memory quota. It charges three things that are all live at
// the peak of the write: the execution's reachable roots (the base), the rendered
// value's own footprint, and the result string (its header plus payloadBytes of
// rendered output). It backs both string interpolation and the `inspect` builtin,
// which share the shape "value stays live while its rendering materializes."
//
// The value's footprint matters because the rendered expression may produce a
// temporary that is not reachable from any environment — a function return, an
// array/hash literal constructed inline, or a receiver like `[big].inspect`. Such
// a temporary lives only on the Go call stack while its rendering is copied, so
// estimateMemoryUsageBase never sees it, yet it is real memory held alongside the
// new string. Without charging it, base+output and base+value could each fit the
// quota while base+value+output exceeds it, letting a huge temporary render past
// the limit.
//
// val is charged through the same estimator that walks the base, so a value that
// IS reachable from an environment (an existing variable rendered directly) is
// deduplicated against the base and contributes only its already-counted footprint
// once, leaving the small-render fast path unchanged.
func (exec *Execution) checkProjectedValueRendering(val Value, payloadBytes int) error {
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
	return exec.checkProjectedIntArrayBytesWithLive(count, 0, NewNil())
}

// checkProjectedIntArrayBytesWithLive is checkProjectedIntArrayBytes for a
// projection that must also account for memory that is already allocated but not
// reachable from any environment root, so estimateMemoryUsageBase cannot see it.
// Destructuring assignment uses it: while assignDestructure runs, the evaluated
// right-hand side (liveRoot) is held only on the Go call stack — a function or
// capability return, or an array literal — and a defensive snapshot of it
// (liveSlots) may have been copied into another Go-local slice. Both are live at
// the peak of the array this check guards (a named rest's captured window), yet
// neither is reachable from an environment, so the base walk misses them.
//
// liveRoot is the live right-hand-side value, charged through the same estimator
// that walks the base so a right-hand side that IS reachable from an environment
// (an existing variable destructured directly) deduplicates against the base and
// contributes only its already-counted footprint once. A nil liveRoot (the
// builtin callers, which have no off-stack right-hand side) charges nothing.
// liveSlots is the snapshot's slot count; its slot array is charged structurally
// because the snapshot shares element payloads with liveRoot, which already
// charged them. Charging all three projects the true peak (base + right-hand side
// + snapshot + array), which the per-statement check would otherwise miss because
// the right-hand side and snapshot are gone by the time control returns from the
// assignment.
func (exec *Execution) checkProjectedIntArrayBytesWithLive(count, liveSlots int, liveRoot Value) error {
	if exec.memoryQuota <= 0 {
		return nil
	}

	est := exec.memoryEstimatorForCheck()
	used := exec.estimateMemoryUsageBase(est)
	if liveRoot.Kind() != KindNil {
		used = saturatingAdd(used, est.value(liveRoot))
	}
	est.reset()

	if liveSlots > 0 {
		used = saturatingAdd(used, estimatedValueBytes+estimatedSliceBaseBytes)
		used = saturatingAdd(used, saturatingMul(liveSlots, estimatedValueBytes))
	}
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
	used = saturatingAdd(used, saturatingMul(outputEntries, estimatedMapEntryStructuralBytes))
	used = saturatingAdd(used, scratchBytes)
	if used > exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, exec.memoryQuota)
	}
	return nil
}

// hashTransformBufferBytes returns the Go-local heap footprint a block-driven hash
// transform (select, reject, transform_keys, transform_values, and the
// block-conflict merge) holds for its whole walk: the output map it fills plus any
// sorted-key scratch slices. outputEntries is the largest entry count the result
// map could reach; scratchBytes is the scratch slices' footprint (see
// sortedKeyBufferBytes). The empty-map base overhead is always included because the
// transform allocates a real map even for an empty result.
//
// These buffers live only on the Go call stack while the block runs, so they are
// invisible to estimateMemoryUsageBase. Callers reserve this through
// reserveLoopScratch BEFORE building the block-call runner so the runner's
// bind-charge baseline already includes them: otherwise a rest-collecting
// destructure block (|k, (head, *tail)|) would charge its fresh tail backing
// against a baseline that omits the output map and scratch, letting
// receiver+out+scratch and receiver+tail each fit the quota while the real peak
// receiver+out+scratch+tail exceeds it. It mirrors what checkProjectedHashTransformBytes
// charges so the up-front reservation and the projection agree byte-for-byte.
func hashTransformBufferBytes(outputEntries, scratchBytes int) int {
	bytes := estimatedValueBytes + estimatedMapBaseBytes + estimatedHashDataBytes
	bytes = saturatingAdd(bytes, saturatingMul(outputEntries, estimatedMapEntryStructuralBytes))
	return saturatingAdd(bytes, scratchBytes)
}

// checkProjectedHashWalkBytes rejects a pure hash iterator (each, each_key,
// each_value, and the `for k, v in hash` statement) before it walks the receiver.
// These iterators return the receiver and build no derived map, so they must not
// be charged an output map they never create; charging a phantom empty map here
// would falsely reject a quota that exactly fits the receiver and block.
//
// The scratch these iterators materialize stays live while their body runs, so
// callers reserve it through reserveLoopScratch before this check rather than
// passing it here: the reservation folds the scratch into the live baseline
// (hashCallRootBytes measures it), which both charges it at this preflight and
// keeps every memory check inside the body aware of it. A collapsed-pair walk
// reserves its largest transient [key, value] pair (maxCollapsedPairBytes) the
// same way, so the symbol-key payload the estimator bills is captured exactly
// without re-walking the receiver per entry (the walk stays O(n) in the receiver
// size).
func (exec *Execution) checkProjectedHashWalkBytes(receiver Value, args []Value, kwargs map[string]Value, block Value) error {
	if exec.memoryQuota <= 0 {
		return nil
	}

	if used := exec.hashCallRootBytes(receiver, args, kwargs, block); used > exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, exec.memoryQuota)
	}
	return nil
}

// checkReservedLoopScratch rejects a hash walk or transform after it has folded
// Go-local buffers into reservedScratchBytes, but before the caller allocates or
// walks those buffers. It charges the current reserved scratch together with the
// call roots, matching the baseline every later memory check will observe.
func (exec *Execution) checkReservedLoopScratch(receiver Value, args []Value, kwargs map[string]Value, block Value) error {
	if exec.memoryQuota <= 0 {
		return nil
	}

	if used := exec.hashCallRootBytes(receiver, args, kwargs, block); used > exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, exec.memoryQuota)
	}
	return nil
}

// maxCollapsedPairBytes returns the heap footprint of the largest transient
// two-element [key, value] array a collapsed-pair hash walk allocates over
// entries (Hash#each or `for pair in hash` with a single-parameter block). Only
// one pair is live at a time -- the previous entry's pair is unreferenced once the
// next overwrites the slot it is bound to -- so reserving the largest one for the
// walk's lifetime conservatively bounds the transient when body checks cannot see
// the Go-stack receiver.
//
// Each pair is PROBED against a single estimator seeded once with the receiver, so
// the value the pair references deduplicates against the receiver (it aliases the
// receiver's own entry) and is charged only its reference slot, while the array
// structure and the symbol key's string payload -- which the estimator bills on top
// of the structure -- are charged in full. probe rolls the estimator back after each
// pair, so the receiver is walked exactly once and every pair is measured against the
// same baseline: the scan is O(n) in the number of entries, not O(n^2).
//
// Reserving this exact maximum (rather than a fixed structural constant) closes the
// escape where a constant omitting the symbol payload let a quota between
// receiver+structure and receiver+full-pair pass the preflight while the body check,
// blind to the Go-stack receiver, also passed -- letting the true peak exceed the
// quota. Because the reservation is folded into the live baseline, it also keeps the
// pair charged alongside any fresh rest backing a destructuring block binds, so the
// combined peak (receiver + pair + rest) is bounded, not just each term alone.
//
// It returns 0 when no memory quota is enforced, skipping the per-entry probe scan
// the reservation would otherwise pay for a budget nothing checks.
func (exec *Execution) maxCollapsedPairBytes(receiver Value) int {
	if exec.memoryQuota <= 0 {
		return 0
	}
	est := newMemoryEstimator()
	est.value(receiver)
	return maxCollapsedPairBytesWithEstimator(receiver, est)
}

func maxCollapsedPairBytesWithEstimator(receiver Value, est *memoryEstimator) int {
	if hashHasTypedEntries(receiver) {
		if receiver.HashLen() == 0 {
			return 0
		}
		maxBytes := 0
		var entryBuf [smallHashKeyBufferSize]HashEntry
		for _, entry := range receiver.HashEntriesInto(entryBuf[:]) {
			pair := NewArray([]Value{entry.Key, entry.Value})
			if bytes := est.probe(pair); bytes > maxBytes {
				maxBytes = bytes
			}
		}
		return maxBytes
	}
	entries := receiver.Hash()
	if len(entries) == 0 {
		return 0
	}
	maxBytes := 0
	for key, value := range entries {
		pair := NewArray([]Value{NewSymbol(key), value})
		if bytes := est.probe(pair); bytes > maxBytes {
			maxBytes = bytes
		}
	}
	return maxBytes
}

func (exec *Execution) valueReachableFromLiveBase(value, block Value) bool {
	if exec.memoryQuota <= 0 || value.Kind() == KindNil {
		return false
	}
	est := exec.memoryEstimatorForCheck()
	exec.estimateMemoryUsageBase(est)
	if !block.IsNil() {
		est.value(block)
	}
	return est.probe(value) <= estimatedValueBytes
}

func (exec *Execution) checkCollapsedPairBytesWithLiveBase(receiver, block Value) error {
	if exec.memoryQuota <= 0 {
		return nil
	}
	est := exec.memoryEstimatorForCheck()
	used := exec.estimateMemoryUsageBase(est)
	if !block.IsNil() {
		used = saturatingAdd(used, est.value(block))
	}
	if used = saturatingAdd(used, maxCollapsedPairBytesWithEstimator(receiver, est)); used > exec.memoryQuota {
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
	result  *memoryEstimator
	base    int
	payload int

	// Call roots retained so checkTransient can re-seed a throwaway estimator
	// with the same baseline the build was snapshotted against, deduplicating a
	// transient yielded value against memory already charged in base.
	receiver Value
	args     []Value
	kwargs   map[string]Value
	block    Value
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
	acc := &arrayBuildAccumulator{
		exec:     exec,
		receiver: receiver,
		args:     args,
		kwargs:   kwargs,
		block:    block,
	}
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

// reserveScratch folds a fixed scratch allocation into the baseline so it is held
// against the quota for the build's entire lifetime, and rejects the build if the
// reservation alone already overflows. Builders that keep a Go-local scratch
// buffer live while the result accumulates (String#scan holds the engine's whole
// [][]int match table the entire time it materializes per-match result elements
// from it) reserve that buffer here so its bytes coexist with every accumulated
// element at peak. Without the reservation a build could keep both the scratch and
// the growing result live and exceed the quota by the scratch size before the
// per-element check observed it. scratchBytes is the heap footprint of that live
// buffer.
func (acc *arrayBuildAccumulator) reserveScratch(scratchBytes int) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}
	acc.base = saturatingAdd(acc.base, scratchBytes)
	if acc.base > acc.exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, acc.exec.memoryQuota)
	}
	return nil
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
// would. Scratch buffers held live for the build's duration are charged via
// reserveScratch, which folds them into the baseline so they are counted
// alongside the growing result rather than separately.
func (acc *arrayBuildAccumulator) add(val Value, backingCap int) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}

	acc.payload = saturatingAdd(acc.payload, acc.est.valuePayload(val))

	if used := acc.projected(backingCap); used > acc.exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, acc.exec.memoryQuota)
	}
	return nil
}

// addToReservedBacking charges an appended element when the caller has already
// reserved the result array's value and backing slice through reserveLoopScratch.
// The reservation is part of acc.base, so this method adds only retained element
// payloads and avoids charging an empty array backing a second time.
func (acc *arrayBuildAccumulator) addToReservedBacking(val Value) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}

	acc.payload = saturatingAdd(acc.payload, acc.est.valuePayload(val))
	if used := saturatingAdd(acc.base, acc.payload); used > acc.exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, acc.exec.memoryQuota)
	}
	return nil
}

// addConservative charges a block-produced result without deduplicating it
// against the build baseline. That keeps in-place mutations of receiver-owned
// containers visible to the quota while still deduplicating shared backings
// across retained results.
func (acc *arrayBuildAccumulator) addConservative(val Value, backingCap int) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}

	if acc.result == nil {
		acc.result = newMemoryEstimator()
	}
	acc.payload = saturatingAdd(acc.payload, acc.result.valuePayload(val))

	if used := acc.projected(backingCap); used > acc.exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, acc.exec.memoryQuota)
	}
	return nil
}

// checkTransient rejects the build when a freshly allocated value yielded to the
// block (and live only for that call) would push the peak footprint over the
// quota. Builders that synthesize a per-iteration argument the result does not
// retain — hash.map_with_index allocates a fresh [key, value] pair to yield —
// use it so the live pair is charged alongside the accumulating result, matching
// how each_with_index charges its yielded pair before invoking the block.
//
// The transient is measured against a throwaway estimator re-seeded with the
// build's call roots, so memory already counted in base (the receiver value the
// pair wraps) deduplicates away and only the transient's fresh allocation is
// added. Using a throwaway estimator rather than the persistent results-only one
// keeps the transient's backing out of the seen-set: it is freed before the next
// iteration, and recording it could let a later value reusing that address be
// dedup'd to nothing. backingCap is the result backing's current capacity so the
// peak charges base, the result slots, the payloads so far, and the live
// transient together.
func (acc *arrayBuildAccumulator) checkTransient(transient Value, backingCap int) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}

	est := acc.exec.memoryEstimatorForCheck()
	defer est.reset()
	acc.exec.estimateMemoryUsageBase(est)
	if acc.receiver.Kind() != KindNil {
		est.value(acc.receiver)
	}
	for _, arg := range acc.args {
		est.value(arg)
	}
	for _, kwarg := range acc.kwargs {
		est.value(kwarg)
	}
	if !acc.block.IsNil() {
		est.value(acc.block)
	}

	transientBytes := est.value(transient)
	if used := saturatingAdd(acc.projected(backingCap), transientBytes); used > acc.exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, acc.exec.memoryQuota)
	}
	return nil
}

// reserveSlots rejects the build up front when a backing slice of slotCount Value
// slots would already overflow the quota on top of the baseline (exec's reachable
// roots plus the call roots) and the payload accumulated so far. Builtins that can
// derive a large lower bound on the result length before emitting it — such as a
// range selector in Array#values_at expanding to a billion padded positions — use
// it to fail fast instead of charging the same slots one append at a time. It
// charges only the slot array, not per-element payloads (those are added by add as
// each element is appended), so it never rejects a result add would accept.
func (acc *arrayBuildAccumulator) reserveSlots(slotCount int) error {
	return acc.reserveSlotArrays(slotCount)
}

// reserveSlotArrays rejects a build when several result arrays will be live
// together, such as Array#pop returning both the remaining and removed arrays.
func (acc *arrayBuildAccumulator) reserveSlotArrays(slotCounts ...int) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}

	used := saturatingAdd(acc.base, acc.payload)
	for _, slotCount := range slotCounts {
		backing := saturatingAdd(estimatedValueBytes+estimatedSliceBaseBytes, saturatingMul(slotCount, estimatedValueBytes))
		used = saturatingAdd(used, backing)
	}
	if used > acc.exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, acc.exec.memoryQuota)
	}
	return nil
}

// projected returns the build's live footprint if its backing slice held slotCount
// Value slots: the baseline (exec's reachable roots plus the call roots), the slot
// array sized to slotCount, and the payloads accumulated so far. add and
// reserveSlots share it so the per-append and up-front checks charge the slot array
// identically.
func (acc *arrayBuildAccumulator) projected(slotCount int) int {
	backing := saturatingAdd(estimatedValueBytes+estimatedSliceBaseBytes, saturatingMul(slotCount, estimatedValueBytes))
	return saturatingAdd(saturatingAdd(acc.base, backing), acc.payload)
}

func (acc *arrayBuildAccumulator) retainedPayloadBytes() int {
	return acc.payload
}

// hashBuildAccumulator charges the memory of an output map assembled entry by
// entry against the quota without re-walking the whole map on each insertion.
// Hash transforms whose block returns fresh heap values (transform_values and
// transform_keys, where each block call can yield an arbitrarily large string or
// nested collection, and the merge conflict block) use it so accumulated
// payloads count toward the quota during construction, not only after the
// builtin returns.
//
// checkProjectedHashBytes alone is enough for blockless transforms whose values
// are references shared with the receiver, because there the output map's
// payloads are already resident and the projection only needs the new map's
// structural slots. It cannot bound a block that synthesizes new values: those
// live solely in the Go-local result map, unreachable from any execution root
// until the builtin returns, so many individually-under-quota results could
// accumulate well past the quota before the post-call check observes them.
//
// The accumulator charges block results conservatively. It keeps a results-only
// estimator that is NOT seeded with the build's base or call roots, so each
// inserted entry is charged its full current footprint as the estimator would
// measure it. Two results that share a backing are still deduplicated against
// each other (the estimator's seen-sets persist across add calls), but a result
// is never deduplicated against the base, so a block that mutates a
// receiver-owned container in place and returns it is charged at its full
// current size rather than dedup'd to nothing against the backing the baseline
// already saw. This can only over-count -- a block returning an unchanged value
// shared with the base or another result is counted again -- and so never
// under-counts the live result footprint, which keeps the sandbox bound sound by
// construction even under in-place mutation. The over-count is intentional and
// documented (see changelog.d/608 and docs/hashes.md); the array-side equivalent
// is tracked in #787.
//
// The output map is preallocated with make(map[string]Value, n), so its full
// n-slot backing -- and its empty-map overhead -- is live from the first block
// call, before any result has been charged. The caller therefore reserves that
// whole output map plus any sorted-key scratch through reserveLoopScratch
// (hashTransformBufferBytes) BEFORE building the accumulator, so the reservation is
// folded into the live baseline every check reads (estimateMemoryUsageBase),
// including the accumulator's own base measured here. add then charges each entry
// only the key/value PAYLOAD beyond the structural slot already reserved. Routing
// the reservation through reserveLoopScratch (rather than a private accumulator
// field) is what also lets the block-call runner's bind-charge baseline see the
// output map and scratch, so a rest-collecting destructure block cannot charge its
// fresh backing against a baseline that omits them.
//
// Each add is O(size of the inserted value) and the total is O(sum of inserted
// result sizes), not O(n^2): the estimator walks only the newly inserted value,
// never the accumulated prefix.
type hashBuildAccumulator struct {
	exec *Execution
	// est is a results-only estimator: it is never seeded with the base or call
	// roots, so it deduplicates backings shared across block results but charges a
	// result's full footprint even when it aliases a baseline container. base is the
	// live footprint snapshotted when the build started: exec's reachable roots, the
	// call roots, and the output map plus scratch the caller reserved through
	// reserveLoopScratch before the accumulator was built. built is the running byte
	// total charged for the per-entry key/value payloads as the output is assembled.
	est     *memoryEstimator
	baseEst *memoryEstimator
	base    int
	built   int

	// Call roots retained so checkTransient can re-seed a throwaway estimator
	// with the same baseline the build was snapshotted against, deduplicating a
	// transient block result against memory already charged in base.
	receiver Value
	args     []Value
	kwargs   map[string]Value
	block    Value
}

// newHashBuildAccumulator snapshots the execution's current live memory plus the
// transform's call roots as the baseline for an incremental hash build. Callers
// reserve the preallocated output map (its empty overhead plus every capacity slot)
// and any sorted-key scratch through reserveLoopScratch BEFORE building the
// accumulator, so those bytes are already folded into the live baseline this
// snapshot reads; add then charges only the per-entry payloads beyond the reserved
// structural slots. The accumulator's results-only estimator is a fresh estimator
// that is deliberately NOT seeded with the base or call roots: it deduplicates
// backings shared across block results but never against the baseline, so an
// in-place-mutated receiver container returned by a block is charged at its full
// current size rather than treated as already accounted.
func newHashBuildAccumulator(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) *hashBuildAccumulator {
	acc := &hashBuildAccumulator{
		exec:     exec,
		receiver: receiver,
		args:     args,
		kwargs:   kwargs,
		block:    block,
	}
	if exec.memoryQuota <= 0 {
		return acc
	}
	// Measure the baseline through a dedicated estimator so the results-only
	// estimator stays empty: the base must be counted, but the results estimator
	// must not dedup later block results against the call roots it walked. The
	// baseline estimator is retained for recursive transforms whose results are
	// runtime-built containers that may still share unchanged leaves with the
	// receiver. The baseline already includes the output map and scratch the caller
	// reserved via reserveLoopScratch (estimateMemoryUsageBase reads the
	// reservation), so the empty-map overhead is not folded in again here.
	acc.baseEst = newMemoryEstimator()
	acc.base = exec.estimateMemoryUsageBase(acc.baseEst)
	if receiver.Kind() != KindNil {
		acc.base = saturatingAdd(acc.base, acc.baseEst.value(receiver))
	}
	for _, arg := range args {
		acc.base = saturatingAdd(acc.base, acc.baseEst.value(arg))
	}
	for _, kwarg := range kwargs {
		acc.base = saturatingAdd(acc.base, acc.baseEst.value(kwarg))
	}
	if !block.IsNil() {
		acc.base = saturatingAdd(acc.base, acc.baseEst.value(block))
	}
	acc.est = newMemoryEstimator()
	return acc
}

type hashLiteralBuildAccumulator struct {
	exec          *Execution
	est           *memoryEstimator
	base          int
	retained      int
	replacing     bool
	keyPayloads   map[string]int
	valuePayloads map[string]int
}

// newHashLiteralBuildAccumulator snapshots the current execution roots for a
// hash literal. Literal values are plain expression results, not block callbacks
// that may mutate baseline containers in place, so an alias such as
// `big = ...; {a: big}` should be charged like the final hash: the new map
// structure and key bytes are fresh, while big's backing remains counted once.
func newHashLiteralBuildAccumulator(exec *Execution) *hashLiteralBuildAccumulator {
	acc := &hashLiteralBuildAccumulator{exec: exec}
	if exec.memoryQuota <= 0 {
		return acc
	}

	acc.est = newMemoryEstimator()
	acc.base = exec.estimateMemoryUsageBase(acc.est)
	acc.base = saturatingAdd(acc.base, estimatedValueBytes+estimatedMapBaseBytes+estimatedHashDataBytes)
	return acc
}

func (acc *hashLiteralBuildAccumulator) reserveBacking(capacity int) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}
	acc.base = saturatingAdd(acc.base, saturatingMul(capacity, estimatedMapEntryStructuralBytes))
	return acc.checkQuota()
}

func (acc *hashLiteralBuildAccumulator) addDistinctEntry(key string, val Value) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}

	acc.retained = saturatingAdd(acc.retained, acc.est.valuePayload(val))
	acc.retained = saturatingAdd(acc.retained, acc.est.stringPayloadSize(key))
	return acc.checkQuota()
}

// replaceEntry switches duplicate-key literals to per-key retained accounting.
// Distinct-key literals stay on the seeded-estimator fast path; once a duplicate
// appears, retained charges must become subtractable so overwritten values stop
// contributing after the replacement.
func (acc *hashLiteralBuildAccumulator) replaceEntry(key string, val Value, current map[string]Value) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}
	if !acc.replacing {
		acc.rebuildRetainedEntries(current)
	}

	keyPayload, valuePayload := acc.entryPayloads(key, val)
	incoming := saturatingAdd(keyPayload, valuePayload)
	if used := saturatingAdd(saturatingAdd(acc.base, acc.retained), incoming); used > acc.exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, acc.exec.memoryQuota)
	}

	prior := saturatingAdd(acc.keyPayloads[key], acc.valuePayloads[key])
	acc.retained -= prior
	if acc.retained < 0 {
		acc.retained = 0
	}
	acc.retained = saturatingAdd(acc.retained, incoming)
	if acc.keyPayloads == nil {
		acc.keyPayloads = make(map[string]int)
		acc.valuePayloads = make(map[string]int)
	}
	acc.keyPayloads[key] = keyPayload
	acc.valuePayloads[key] = valuePayload
	return acc.checkQuota()
}

func (acc *hashLiteralBuildAccumulator) rebuildRetainedEntries(current map[string]Value) {
	acc.retained = 0
	acc.keyPayloads = make(map[string]int, len(current))
	acc.valuePayloads = make(map[string]int, len(current))
	for key, val := range current {
		keyPayload, valuePayload := acc.entryPayloads(key, val)
		acc.keyPayloads[key] = keyPayload
		acc.valuePayloads[key] = valuePayload
		acc.retained = saturatingAdd(acc.retained, saturatingAdd(keyPayload, valuePayload))
	}
	acc.est = nil
	acc.replacing = true
}

func (acc *hashLiteralBuildAccumulator) entryPayloads(key string, val Value) (int, int) {
	est := newMemoryEstimator()
	acc.exec.estimateMemoryUsageBase(est)
	valuePayload := est.valuePayload(val)
	keyPayload := est.stringPayloadSize(key)
	return keyPayload, valuePayload
}

func (acc *hashLiteralBuildAccumulator) checkQuota() error {
	used := saturatingAdd(acc.base, acc.retained)
	if used > acc.exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, acc.exec.memoryQuota)
	}
	return nil
}

// reserveBacking folds the structural footprint of a preallocated output map
// into the baseline so the whole backing is held against the quota from the
// first block call, and rejects the build if the reservation alone already
// overflows. Use it for hash builds that have not already reserved their output
// map through reserveLoopScratch. Hash transforms reserve output and scratch
// before constructing the accumulator so the block bind-charge baseline can see
// those Go-local buffers; array.to_h's block form has no transform scratch and
// reserves its output map here instead.
//
// capacity is the slot count passed to make. The key and value PAYLOADS are
// charged incrementally by add/addSynthesizedKey, which after this reservation
// add only the payload beyond the structural slot already counted, so nothing is
// double counted: base ends at call roots + empty map + capacity*slot, and built
// ends at the sum of per-entry payloads.
func (acc *hashBuildAccumulator) reserveBacking(capacity int) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}
	acc.base = saturatingAdd(acc.base, estimatedValueBytes+estimatedMapBaseBytes+estimatedHashDataBytes)
	acc.base = saturatingAdd(acc.base, saturatingMul(capacity, estimatedMapEntryStructuralBytes))
	return acc.checkQuota()
}

// add charges a write whose VALUE is a block result to the output map and rejects
// the build if the projected map memory exceeds the quota. Use it where the block
// produces the VALUE (transform_values, the merge conflict block) and the key is a
// receiver or argument key kept unchanged; transform_keys, whose block produces
// the KEY while the value stays a receiver value, uses addSynthesizedKey instead.
//
// The entry's structural slot (map bucket, key header, value slot) is already
// held in the baseline by the caller's reserveLoopScratch reservation, so add
// charges only PAYLOADS beyond it.
// The key is a receiver/argument key whose payload is already counted in the
// baseline via the call roots, so it contributes nothing further. Only the
// block-returned value's payload goes through the results-only estimator, so a
// backing shared across two block results is counted once but a result that
// aliases a baseline container is counted at full size rather than deduplicated to
// nothing. Routing the key through the estimator would record its backing in the
// seen-set and risk a later block result that aliases it being dedup'd away.
//
// Charging per write (rather than per distinct key) means a key overwritten by a
// later write -- a merge conflict block folding several colliding arguments -- is
// counted once per occurrence. That is a conservative over-count of the final
// map's footprint, the intentional tradeoff that makes the bound sound under
// in-place mutation: built only ever grows, so it can never drop below the live
// footprint and let a later insert materialize past the quota.
func (acc *hashBuildAccumulator) add(val Value) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}

	acc.built = saturatingAdd(acc.built, acc.est.valuePayload(val))
	return acc.checkQuota()
}

// addBaselineDeduped charges a runtime-built value while deduplicating unchanged
// leaves already reachable from the transform's call roots. Use it for recursive
// transforms such as deep_transform_keys, where the runtime synthesizes fresh
// container structure but carries original leaf values through unchanged. Do not
// use it for arbitrary block results: a block can mutate a baseline container in
// place and return it, and those results need add's conservative unseeded walk.
func (acc *hashBuildAccumulator) addBaselineDeduped(val Value) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}

	acc.built = saturatingAdd(acc.built, acc.baseEst.valuePayload(val))
	return acc.checkQuota()
}

// addSynthesizedKey charges a key write whose KEY is a fresh block-synthesized
// string but whose VALUE is a receiver value already counted in the baseline
// (transform_keys yields a new key per entry while keeping the original value).
// The entry's structural slot (map bucket, key header, value slot) is already held
// in the baseline by the caller's reserveLoopScratch reservation, and the value's
// reachable payload is already folded into acc.base via the call roots, so the only
// fresh allocation to charge is the synthesized key's PAYLOAD beyond its header. It
// goes through the
// results-only estimator so a key string shared across block results is counted
// once; the value never goes through the estimator, since routing it would record
// its backing in the seen-set and let a later block result aliasing it be dedup'd
// to nothing -- the exact under-count the results-only estimator exists to
// prevent. The synthesized key payload is the only unbounded fresh allocation the
// up-front structural projection cannot see, so charging it incrementally keeps
// the build within the quota during the loop.
func (acc *hashBuildAccumulator) addSynthesizedKey(key string) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}

	acc.built = saturatingAdd(acc.built, acc.est.stringPayloadSize(key))
	return acc.checkQuota()
}

// checkTransient rejects the build when a freshly allocated value returned by a
// block, but not retained as part of the output map, would push the peak
// footprint over the quota. Array#to_h's block form uses it for the temporary
// two-element pair returned by each block call: the output map backing is already
// live and reserved in base, while the pair array itself is only live until its
// key and value are extracted.
func (acc *hashBuildAccumulator) checkTransient(transient Value) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}

	est := acc.exec.memoryEstimatorForCheck()
	defer est.reset()
	acc.exec.estimateMemoryUsageBase(est)
	if acc.receiver.Kind() != KindNil {
		est.value(acc.receiver)
	}
	for _, arg := range acc.args {
		est.value(arg)
	}
	for _, kwarg := range acc.kwargs {
		est.value(kwarg)
	}
	if !acc.block.IsNil() {
		est.value(acc.block)
	}

	used := saturatingAdd(saturatingAdd(acc.base, acc.built), est.value(transient))
	if used > acc.exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, acc.exec.memoryQuota)
	}
	return nil
}

// checkQuota rejects the build when the live baseline plus the accumulated output
// exceeds the quota.
func (acc *hashBuildAccumulator) checkQuota() error {
	used := saturatingAdd(acc.base, acc.built)
	if used > acc.exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, acc.exec.memoryQuota)
	}
	return nil
}

func (acc *hashBuildAccumulator) retainedPayloadBytes() int {
	return acc.built
}

// blockBindCharge charges the fresh memory a block's destructuring parameters
// allocate when they are bound. A destructuring parameter that collects a rest
// (|(k, *tail)|, or the nested |(k, (head, *tail))|) makes AssignDestructure copy
// the collected window into a fresh backing slice (make+copy) before binding it.
// That copy is sized to the SOURCE, not to anything the iterator preflighted: over
// a hash whose values are arrays, |(k, (head, *tail))| binds tail to a fresh copy
// of the whole value array, a backing the per-entry [key, value] pair reservation
// does not bound. With an empty (or trivial) block body the body's own memory
// checks never observe that backing, and the iterator's receiver lives only on the
// Go call stack (invisible to estimateMemoryUsageBase), so the fresh copy could
// escape the sandbox quota entirely. This charge closes that gap in the one place
// every block iterator shares (the param-binding path), so it covers Hash#each,
// array.each/map/select, reduce, and any other block call alike.
//
// rootEst is a PERSISTENT estimator seeded ONCE (at construction) with the live call
// roots: exec's reachable roots (the env stack and exec.root) plus the receiver,
// kwargs, callArgs, and block. It is the same single walk estimateMemoryUsageForCallRoots
// performs, so the receiver deduplicates against the env (a named local passed to
// arr.each is counted once, not twice) and baseline records that one walk's total.
// Crucially it is NOT re-walked per entry: re-walking the receiver or the env on
// every iteration is the O(n^2) trap that previously dominated CI. The env held in
// rootEst stays live for the whole loop (the iterator runs mid-expression), so the
// committed backings are never freed and a later probe cannot falsely deduplicate
// against a reused address.
//
// Per-entry growth in outer scope is still caught by the body's own per-statement
// checks, which walk the live env (the bound rest included) on each statement. The
// snapshot baseline (the receiver the body checks cannot see, plus the fresh rest)
// and the body checks (state the body grows, which the pre-loop snapshot misses)
// together bound the live footprint.
//
// Each call resets a SEPARATE per-call estimator (est) and seeds it with that call's
// arguments (the [key, value] pair for Hash#each, the destructured element for array
// iterators, the (acc, item) pair for reduce), so a fresh rest backing whose ELEMENTS
// alias the yielded data deduplicates those payloads to zero and only the backing's
// genuinely new slots are charged. est is reset every call so its seen-set never
// grows across a long loop, keeping the charge O(the data bound this entry).
type blockBindCharge struct {
	exec     *Execution
	est      *memoryEstimator
	rootEst  *memoryEstimator
	baseline int
	built    int
}

// newBlockBindCharge snapshots the live call roots as the baseline for charging a
// block's destructured bindings, or returns nil when no charge is needed: either no
// memory quota is enforced or the block has no rest-collecting destructure parameter
// (the only binding shape AssignDestructure materializes into a fresh, source-sized
// backing slice). A plain or non-rest destructure parameter binds references already
// counted in the call roots, so it allocates nothing fresh and needs no charge.
//
// callArgs are the iterator's POSITIONAL call roots (the other hashes a block-driven
// hash.merge folds in, a grep pattern, the host arguments a capability CallBlock
// drives a block with). Like the receiver they live only on the Go call stack during
// the loop, invisible to estimateMemoryUsageBase, so they are walked into the
// persistent rootEst here: charging only the fresh rest copy against a baseline that
// omits them lets a quota fit (roots + rest) and (receiver + rest) separately while
// the real peak (receiver + args + rest) exceeds it. They are the FIXED backings held
// for the whole loop; the per-entry yielded values and the per-call charged roots are
// handled by begin instead.
func newBlockBindCharge(exec *Execution, blk *Block, receiver Value, callArgs []Value, kwargs map[string]Value, block Value) *blockBindCharge {
	if exec.memoryQuota <= 0 || !blockBindsRest(blk) {
		return nil
	}
	rootEst := newMemoryEstimator()
	baseline := exec.estimateMemoryUsageBase(rootEst)
	if receiver.Kind() != KindNil {
		baseline = saturatingAdd(baseline, rootEst.value(receiver))
	}
	for _, arg := range callArgs {
		baseline = saturatingAdd(baseline, rootEst.value(arg))
	}
	for _, kwarg := range kwargs {
		baseline = saturatingAdd(baseline, rootEst.value(kwarg))
	}
	if !block.IsNil() {
		baseline = saturatingAdd(baseline, rootEst.value(block))
	}
	return &blockBindCharge{
		exec:     exec,
		est:      newMemoryEstimator(),
		rootEst:  rootEst,
		baseline: baseline,
	}
}

// begin prepares the charge for one block call: it resets the per-call estimator and
// seeds it with the call's arguments so any payload a freshly bound rest backing
// shares with them (the receiver's own data, reached through the yielded pair or
// element) deduplicates to zero. Only the backing's new slots remain to be charged.
// Seeding walks just this call's arguments, never the whole receiver, so it stays
// O(the data this entry yields).
//
// chargedRoots are per-call values that live only in the iterator's Go frame and
// evolve every call, so they cannot be folded into the one-time baseline -- the
// reduce accumulator (acc_0 is the seed, acc_n is the previous call's block result).
// Each is PROBED against the persistent rootEst, which already holds the receiver, so
// a no-seed accumulator that is the receiver's first element deduplicates to its
// structural slots only and is NOT charged a second copy of the receiver's data --
// the double-charge this guards against. The probe walks only the charged root
// (bounded by its size) and rolls back, so the rootEst is not permanently grown and
// the next call's accumulator cannot falsely deduplicate against this one's freed
// backing. begin returns the quota error if the charged roots alone exceed the
// budget, so a reduce whose live peak is receiver + accumulator is rejected before
// the block body runs. The charged roots are also seeded into the per-call estimator
// so a rest backing that copies part of the accumulator deduplicates against it.
func (c *blockBindCharge) begin(args []Value, chargedRoots ...Value) error {
	if c == nil {
		return nil
	}
	c.est.reset()
	c.built = 0
	for _, root := range chargedRoots {
		c.built = saturatingAdd(c.built, c.rootEst.probe(root))
		if saturatingAdd(c.baseline, c.built) > c.exec.memoryQuota {
			return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, c.exec.memoryQuota)
		}
		c.est.value(root)
	}
	for _, arg := range args {
		c.est.value(arg)
	}
	return nil
}

// charge adds a freshly bound leaf value to the running estimate and rejects the
// call when the live baseline plus every value bound so far this call exceeds the
// quota. The estimator returns each leaf's marginal footprint -- a value that
// aliases the seeded arguments contributes only its structural slots, since its
// payload deduplicates against the seed -- so a rest backing is charged its real
// fresh footprint while a pass-through binding charges essentially nothing.
func (c *blockBindCharge) charge(value Value) error {
	if c == nil {
		return nil
	}
	c.built = saturatingAdd(c.built, c.est.value(value))
	if saturatingAdd(c.baseline, c.built) > c.exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, c.exec.memoryQuota)
	}
	return nil
}

// projectRestWindow rejects a destructure rest backing of count Value slots
// BEFORE assignDestructure allocates it. assignDestructure copies the collected
// window into a fresh make([]Value, count) backing before binding it; without this
// preflight a quota smaller than one such backing would let the copy materialize
// (a single huge tail, |(head, *tail)| over a [[huge...]], with an empty block
// body whose own checks never run) and the post-bind charge would only observe the
// over-budget array after it already escaped. Projecting the window's structural
// footprint -- a slice header plus count Value slots, the same shape the estimator
// charges a fresh slice backing -- on top of this call's baseline and everything
// already bound this call mirrors the check-before-materialize discipline the
// standalone assignDestructure path uses (checkProjectedIntArrayBytesWithLive).
//
// The window's elements alias the yielded value (already seeded into the per-call
// estimator and counted in the baseline through the receiver and call roots), so
// only the backing's genuinely new slots are projected, not a second copy of the
// element payloads. The post-bind charge still runs on the bound array afterward to
// account for its real, dedup-aware footprint; this is the pre-allocation gate.
func (c *blockBindCharge) projectRestWindow(count int) error {
	if c == nil {
		return nil
	}
	window := saturatingAdd(estimatedValueBytes+estimatedSliceBaseBytes, saturatingMul(count, estimatedValueBytes))
	if saturatingAdd(saturatingAdd(c.baseline, c.built), window) > c.exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, c.exec.memoryQuota)
	}
	return nil
}

// destructureCharge returns the rest-window preflight assignDestructure consults
// before allocating a named rest's backing slice. A nil charge (no memory quota or
// a block with no rest-collecting parameter) admits every window, matching the host
// AssignDestructure path that runs outside a quota. The liveSlots and liveRoot
// arguments the destructurer threads are unused here: block parameters bind only
// identifiers and nested destructures (never an index or member write), so the
// snapshot path that produces a live Go-stack slot array never runs during block
// binding -- the only off-baseline memory is the rest backing this preflight gates.
func (c *blockBindCharge) destructureCharge() destructureCharge {
	if c == nil {
		return destructureCharge{check: noopDestructureCheck}
	}
	return destructureCharge{check: func(count, _ int, _ Value) error {
		return c.projectRestWindow(count)
	}}
}

// blockBindsRest reports whether any of the block's parameters destructure a value
// and collect a named rest, the only binding shape AssignDestructure materializes
// into a fresh, source-sized backing slice. Used to skip the per-call bind charge
// for the common parameter shapes that allocate nothing fresh.
func blockBindsRest(blk *Block) bool {
	for i := range blk.Params {
		if targetCollectsRest(blk.Params[i].Target) {
			return true
		}
	}
	return false
}

// targetCollectsRest reports whether a destructure target collects a rest into a
// fresh backing slice. An anonymous rest (a bare "*", whose element Target is nil)
// is skipped here: assignDestructure discards its window without materializing an
// array, so charging its bindings would seed the estimator with the whole yielded
// value for a backing that never exists -- regressing the |(head, *)| fast path
// over large nested rows. Only a rest element with a non-nil target, or a nested
// destructure that itself collects one, allocates the slice this charge gates.
func targetCollectsRest(target Expression) bool {
	destructure, ok := target.(*DestructureTarget)
	if !ok {
		return false
	}
	for _, element := range destructure.Elements {
		if element.Rest && element.Target != nil {
			return true
		}
		if targetCollectsRest(element.Target) {
			return true
		}
	}
	return false
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
// before it reserves its output map: the call-root usage plus the empty hash's
// structural overhead (the empty map plus the hashData wrapper every KindHash
// allocates). maxProjectedHashEntries builds on it so the entry budget and the
// byte check agree on the same baseline. Callers that build no output map (the
// pure iterators) use hashCallRootBytes directly instead.
func (exec *Execution) projectedHashBaseBytes(receiver Value, args []Value, kwargs map[string]Value, block Value) int {
	return saturatingAdd(exec.hashCallRootBytes(receiver, args, kwargs, block), estimatedValueBytes+estimatedMapBaseBytes+estimatedHashDataBytes)
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
	return (exec.memoryQuota - used) / estimatedMapEntryStructuralBytes
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

func (exec *Execution) estimateMemoryUsageForCallRoots(callee, receiver Value, args []Value, kwargs map[string]Value, block Value) int {
	est := exec.memoryEstimatorForCheck()

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

	est.reset()
	return total
}

// reserveLoopScratch folds a Go-local scratch allocation into the live memory
// baseline so it is charged by every memory check for as long as it is held,
// then returns the bytes actually reserved so releaseLoopScratch can subtract
// exactly that amount. A hash-walking loop (the `for k, v in hash` statement and
// Hash#each / each_key / each_value) materializes a sorted-key scratch slice that
// stays live on the Go stack while its body runs. The body executes arbitrary
// code with its own memory checks, but those checks measure only exec's reachable
// roots and so never see this scratch slice; without reserving it, a body that
// allocates near the quota could pass its own checks while the true peak (roots +
// scratch + body allocation) exceeds the quota by the scratch size. Folding the
// scratch into estimateMemoryUsageBase for the loop's duration makes every check
// inside the body account for it.
//
// The return value is the delta applied, which equals scratchBytes unless the
// reservation saturates at math.MaxInt; releaseLoopScratch must be passed that
// delta so nested reservations stay perfectly balanced even under saturation.
func (exec *Execution) reserveLoopScratch(scratchBytes int) int {
	reserved := saturatingAdd(exec.reservedScratchBytes, scratchBytes)
	delta := reserved - exec.reservedScratchBytes
	exec.reservedScratchBytes = reserved
	return delta
}

// releaseLoopScratch returns reserved scratch bytes to the baseline once the loop
// that held them has finished. delta is the value reserveLoopScratch returned.
func (exec *Execution) releaseLoopScratch(delta int) {
	exec.reservedScratchBytes -= delta
}

func (exec *Execution) estimateMemoryUsageBase(est *memoryEstimator) int {
	total := exec.reservedScratchBytes

	total += est.env(exec.root)
	for _, env := range exec.envStack {
		total += est.env(env)
	}
	for _, mod := range exec.modules {
		total += est.value(mod)
	}
	for _, group := range exec.activeTaskGroups {
		total += group.retainedSnapshotMemory(est)
		total += group.jobPayloadMemory(est)
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
	if est.journal != nil {
		est.journal.envs = append(est.journal.envs, env)
	}

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
	if env.hasCallBlock {
		// A call frame's supplied block lives in a hidden slot rather than a
		// named binding, but for an escaped closure or default proc it can be
		// the only reference to a block that closes over large data. Charge its
		// payload (the block struct and its captured env) so the quota still
		// accounts for it; the value header is already part of estimatedEnvBytes,
		// and a frame that received no block charges nothing.
		size += est.valuePayload(env.callBlock)
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
	case KindHash:
		if entries, ok := hashStringMapIfMaterialized(val); ok {
			size += est.hash(entries)
		}
		// A KindHash wraps its entry map in a hashData struct to carry optional
		// Ruby-style default metadata; that wrapper is a real per-hash heap
		// allocation outside the entry map, so it counts toward the quota too.
		// Charged once per distinct wrapper identity so two values sharing the
		// same hashData are not double counted. Objects use a bare map with no
		// wrapper, so hashWrapperBytes returns 0 for them.
		size += est.hashWrapperBytes(val)
		// A KindHash may retain Ruby-style default metadata (a default value
		// and/or a default proc) outside its entry map. Those payloads are
		// reachable state — a script can hold a large array or string solely
		// through a Hash.new(big) default — so they count toward the quota too.
		// Objects never carry defaults, so these accessors return nil for them.
		size += est.valuePayload(hashDefaultValue(val))
		size += est.valuePayload(hashDefaultProc(val))
	case KindObject:
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
		if est.journal != nil {
			est.journal.classes = append(est.journal.classes, cl)
		}
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
		if est.journal != nil {
			est.journal.instances = append(est.journal.instances, inst)
		}
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
		if est.journal != nil {
			est.journal.blocks = append(est.journal.blocks, blk)
		}
		size += estimatedBlockBytes + estimatedSliceBaseBytes + len(blk.Params)*estimatedStringHeaderBytes
		for _, param := range blk.Params {
			size += estimatedParamBytes(param)
		}
		size += estimatedSliceBaseBytes + len(blk.ImplicitParams)*estimatedStringHeaderBytes
		for _, param := range blk.ImplicitParams {
			size += len(param)
		}
		size += estimatedStringHeaderBytes*3 + len(blk.moduleKey) + len(blk.modulePath) + len(blk.moduleRoot)
		size += est.env(blk.Env)
	case KindFunction:
		// Functions are compile-time/static artifacts for memory quotas.
	case KindBuiltin:
		// Static stdlib builtins are singletons reachable once, so they stay
		// free. A builtin that closes over runtime values, though, is a
		// dynamically allocated probe a script can mint in a loop (for example
		// pushing `1.eql?` or `obj.equal?` into an array): each member access
		// allocates a fresh *Builtin plus its CapturedValues backing. Charge that
		// per-probe structure — the Builtin struct and the slice backing — so the
		// quota accounts for the allocation itself, not just its captured
		// payloads (which are effectively zero for scalar receivers). Then charge
		// the captured payloads on top. Dedup by builtin pointer guards against
		// revisiting the same builtin; recursing through est.value dedups each
		// captured value against any independently reachable copy via the existing
		// seen* maps, so a receiver that is also reachable elsewhere is charged
		// only once.
		builtin := valueBuiltin(val)
		if builtin == nil || len(builtin.CapturedValues) == 0 {
			return size
		}
		if _, seen := est.seenBuiltins[builtin]; seen {
			return size
		}
		if est.seenBuiltins == nil {
			est.seenBuiltins = make(map[*Builtin]struct{})
		}
		est.seenBuiltins[builtin] = struct{}{}
		if est.journal != nil {
			est.journal.builtins = append(est.journal.builtins, builtin)
		}
		size = saturatingAdd(size, estimatedBuiltinBytes)
		size = saturatingAdd(size, sliceStructuralBytes(builtin.CapturedValues))
		for _, captured := range builtin.CapturedValues {
			size = saturatingAdd(size, est.valuePayload(captured))
		}
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
	if est.journal != nil {
		est.journal.strings = append(est.journal.strings, key)
	}
	return len(str)
}

// sliceStructuralBytes is the heap footprint of a slice backing excluding the
// payloads reachable from its elements: the slice base plus one Value slot per
// capacity slot. The element payloads are added on top by recursing into each
// element.
func sliceStructuralBytes(values []Value) int {
	return saturatingAdd(estimatedSliceBaseBytes, saturatingMul(cap(values), estimatedValueBytes))
}

// mapStructuralBytes is the heap footprint of a map backing excluding the
// payloads reachable from its values: the map base, one bucket per entry, a key
// header and key bytes per entry, and one Value slot per entry. The value
// payloads are added on top by recursing into each value.
//
// The per-entry Value slot is part of the structural cost (it exists for every
// entry regardless of what the value points at), so a map of scalar values is
// charged its full slot footprint here even though the recursion contributes no
// further payload for those scalars.
func mapStructuralBytes(values map[string]Value) int {
	size := estimatedMapBaseBytes + len(values)*(estimatedMapEntryBytes+estimatedValueBytes)
	for key := range values {
		size += estimatedStringHeaderBytes + len(key)
	}
	return size
}

func (est *memoryEstimator) slice(values []Value) int {
	size := sliceStructuralBytes(values)
	if cap(values) == 0 {
		return size
	}

	// Deduplicate aliased backings — including empty slices that retained
	// capacity, whose cap*estimatedValueBytes backing is real memory worth
	// counting only once across aliases (e.g. a partition's empty result side
	// shared with another binding). This dedup is best-effort: a non-empty
	// slice's backing pointer (via sliceBackingIdentity/unsafe.SliceData) is
	// stable and reliably deduplicates, but a ZERO-LENGTH slice's backing
	// identity is not reproducible across Go build configurations (observed
	// flaking under coverage, race, and goroutine-leak-profile builds). When an
	// empty slice's identity is unstable the dedup simply does not fire and the
	// backing is counted again — an over-count that is conservative and
	// sandbox-safe (it never under-counts, so the memory bound still holds).
	id := sliceBackingIdentity(values)
	if id != 0 {
		if _, seen := est.seenSlices[id]; seen {
			return 0
		}
		if est.seenSlices == nil {
			est.seenSlices = make(map[uintptr]struct{})
		}
		est.seenSlices[id] = struct{}{}
		if est.journal != nil {
			est.journal.slices = append(est.journal.slices, id)
		}
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

// hashWrapperBytes charges the hashData wrapper a KindHash value allocates
// around its entry map, plus any typed-key entry map retained by that wrapper.
// It is deduplicated on the wrapper's identity so aliases count the extra hash
// state once. It returns 0 for KindObject, which uses a bare map.
func (est *memoryEstimator) hashWrapperBytes(val Value) int {
	if val.Kind() != KindHash {
		return 0
	}
	id := hashIdentity(val)
	if id == 0 {
		return saturatingAdd(estimatedHashDataBytes, est.typedHashEntriesBytes(val))
	}
	if _, seen := est.seenHashData[id]; seen {
		return 0
	}
	if est.seenHashData == nil {
		est.seenHashData = make(map[uintptr]struct{})
	}
	est.seenHashData[id] = struct{}{}
	if est.journal != nil {
		est.journal.hashData = append(est.journal.hashData, id)
	}
	return saturatingAdd(estimatedHashDataBytes, est.typedHashEntriesBytes(val))
}

func (est *memoryEstimator) typedHashEntriesBytes(val Value) int {
	if !hashHasTypedEntries(val) {
		return 0
	}

	var entryBuf [smallHashKeyBufferSize]TypedHashEntry
	entries := val.TypedHashEntriesInto(entryBuf[:])
	size := estimatedMapBaseBytes
	for _, entry := range entries {
		size = saturatingAdd(size, estimatedMapEntryBytes+estimatedHashLookupKeyBytes+estimatedHashEntryBytes)
		size = saturatingAdd(size, entry.LookupKey.ExtraPayloadBytes())
		size = saturatingAdd(size, est.valuePayload(entry.Entry.Key))
		size = saturatingAdd(size, est.valuePayload(entry.Entry.Value))
	}
	return size
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
		if est.journal != nil {
			est.journal.maps = append(est.journal.maps, id)
		}
	}

	size := mapStructuralBytes(values)
	for _, val := range values {
		size += est.valuePayload(val)
	}
	return size
}
