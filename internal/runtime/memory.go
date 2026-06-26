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
	estimatedStringHeaderBytes = 16
	estimatedSliceBaseBytes    = 24
	estimatedMapBaseBytes      = 48
	estimatedMapEntryBytes     = 32
	estimatedEnvBytes          = int(unsafe.Sizeof(Env{}))
	estimatedInstanceBytes     = 16
	estimatedBlockBytes        = 24
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

	if exec.hashCallRootBytes(receiver, args, kwargs, block) > exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, exec.memoryQuota)
	}
	return nil
}

// maxCollapsedPairBytes returns the heap footprint of the largest transient
// two-element [key, value] array a collapsed-pair hash walk allocates over
// entries (Hash#each or `for pair in hash` with a single-parameter block). Only
// one pair is live at a time -- the previous entry's pair is unreferenced once the
// next overwrites the slot it is bound to -- so reserving the largest one for the
// walk's lifetime conservatively bounds the transient and lets every in-body check,
// the iterator preflight, and the per-call bind charge all see it without
// re-walking the receiver per entry.
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
	entries := receiver.Hash()
	if len(entries) == 0 {
		return 0
	}
	est := newMemoryEstimator()
	est.value(receiver)
	maxBytes := 0
	for key, value := range entries {
		pair := NewArray([]Value{NewSymbol(key), value})
		if bytes := est.probe(pair); bytes > maxBytes {
			maxBytes = bytes
		}
	}
	return maxBytes
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

// reserveSlots rejects the build up front when a backing slice of slotCount Value
// slots would already overflow the quota on top of the baseline (exec's reachable
// roots plus the call roots) and the payload accumulated so far. Builtins that can
// derive a large lower bound on the result length before emitting it — such as a
// range selector in Array#values_at expanding to a billion padded positions — use
// it to fail fast instead of charging the same slots one append at a time. It
// charges only the slot array, not per-element payloads (those are added by add as
// each element is appended), so it never rejects a result add would accept.
func (acc *arrayBuildAccumulator) reserveSlots(slotCount int) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}

	if used := acc.projected(slotCount); used > acc.exec.memoryQuota {
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

// hashBuildAccumulator charges the memory of an output map assembled entry by
// entry against the quota without re-walking the whole map on each insertion.
// Hash transforms whose block returns fresh heap values (transform_values and
// transform_keys, where each block call can yield an arbitrarily large string
// or nested collection, and the merge conflict block) use it so accumulated
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
// n-slot backing is live from the first block call -- before any result has been
// charged. The accumulator therefore reserves that backing in the baseline up
// front via reserveBacking, then charges each add only the entry's key/value
// PAYLOAD beyond the structural slot already reserved. Without the reservation a
// large early block result would be checked against only the slots inserted so
// far rather than the whole live backing, letting backing + early result exceed
// the quota until later entries (or the post-call check) caught it.
//
// Each add is O(size of the inserted value) and the total is O(sum of inserted
// result sizes), not O(n^2): the estimator walks only the newly inserted value,
// never the accumulated prefix.
type hashBuildAccumulator struct {
	exec *Execution
	// est is a results-only estimator: it is never seeded with the base or call
	// roots, so it deduplicates backings shared across block results but charges a
	// result's full footprint even when it aliases a baseline container. base is the
	// live footprint snapshotted when the build started (exec's reachable roots, the
	// call roots, the output map's empty overhead, and -- once reserveBacking is
	// called -- the preallocated n-slot backing); built is the running byte total
	// charged for the per-entry key/value payloads as the output is assembled.
	est   *memoryEstimator
	base  int
	built int
}

// newHashBuildAccumulator snapshots the execution's current live memory plus the
// transform's call roots as the baseline for an incremental hash build, then folds
// the output map's empty overhead into that baseline. Callers that preallocate the
// output with make(map[string]Value, capacity) must then call reserveBacking so the
// whole capacity backing is held against the quota before any block runs; add then
// charges only the per-entry payloads beyond the reserved structural slots. The
// accumulator's results-only estimator is a fresh estimator that is deliberately
// NOT seeded with the base or call roots: it deduplicates backings shared across
// block results but never against the baseline, so an in-place-mutated receiver
// container returned by a block is charged at its full current size rather than
// treated as already accounted.
func newHashBuildAccumulator(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) *hashBuildAccumulator {
	acc := &hashBuildAccumulator{exec: exec}
	if exec.memoryQuota <= 0 {
		return acc
	}
	// Measure the baseline through a throwaway estimator so the results-only
	// estimator stays empty: the base must be counted, but the results estimator
	// must not dedup later block results against the call roots it walked.
	acc.base = exec.estimateMemoryUsageForCallRoots(receiver, args, kwargs, block)
	// The output is a single hash, so fold its empty-map overhead plus the
	// hashData wrapper every KindHash allocates into the baseline; add then
	// charges only the per-entry growth.
	acc.base = saturatingAdd(acc.base, estimatedValueBytes+estimatedMapBaseBytes+estimatedHashDataBytes)
	acc.est = newMemoryEstimator()
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

// reserveBacking folds the structural footprint of a preallocated output map into
// the baseline so the whole backing is held against the quota from the first block
// call, and rejects the build if the reservation alone already overflows. Hash
// transforms allocate their output with make(map[string]Value, capacity), so all
// capacity slots are live before any block result is charged; without reserving
// them, an early add would be checked against only the slots filled so far rather
// than the full backing, letting a large early result plus the whole backing slip
// past the quota until later entries (or the post-call check) caught it.
//
// capacity is the slot count passed to make; the empty map's base overhead is
// already folded into the baseline by newHashBuildAccumulator, so only the
// per-slot structure is reserved here. The key and value PAYLOADS are charged
// incrementally by add/addSynthesizedKey, which after this reservation add only
// the payload beyond the structural slot already counted, so nothing is double
// counted: base ends at call roots + empty map + capacity*slot, and built ends at
// the sum of per-entry payloads.
func (acc *hashBuildAccumulator) reserveBacking(capacity int) error {
	if acc.exec.memoryQuota <= 0 {
		return nil
	}
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
// held in the baseline by reserveBacking, so add charges only PAYLOADS beyond it.
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

// addSynthesizedKey charges a key write whose KEY is a fresh block-synthesized
// string but whose VALUE is a receiver value already counted in the baseline
// (transform_keys yields a new key per entry while keeping the original value).
// The entry's structural slot (map bucket, key header, value slot) is already held
// in the baseline by reserveBacking, and the value's reachable payload is already
// folded into acc.base via the call roots, so the only fresh allocation to charge
// is the synthesized key's PAYLOAD beyond its header. It goes through the
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

// checkQuota rejects the build when the live baseline plus the accumulated output
// exceeds the quota.
func (acc *hashBuildAccumulator) checkQuota() error {
	used := saturatingAdd(acc.base, acc.built)
	if used > acc.exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, acc.exec.memoryQuota)
	}
	return nil
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

// blockBindsRest reports whether any of the block's parameters destructure a value
// and collect a rest, the only binding shape AssignDestructure materializes into a
// fresh, source-sized backing slice. Used to skip the per-call bind charge for the
// common parameter shapes that allocate nothing fresh.
func blockBindsRest(blk *Block) bool {
	for i := range blk.Params {
		if targetCollectsRest(blk.Params[i].Target) {
			return true
		}
	}
	return false
}

func targetCollectsRest(target Expression) bool {
	destructure, ok := target.(*DestructureTarget)
	if !ok {
		return false
	}
	for _, element := range destructure.Elements {
		if element.Rest {
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
// around its entry map, deduplicated on the wrapper's identity so two values
// that share the same wrapper count it once. It returns 0 for KindObject (a
// bare map with no wrapper) and for any value whose wrapper identity is
// unavailable, mirroring how est.hash dedups the entry map on its own pointer.
func (est *memoryEstimator) hashWrapperBytes(val Value) int {
	if val.Kind() != KindHash {
		return 0
	}
	id := hashIdentity(val)
	if id == 0 {
		return estimatedHashDataBytes
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
	return estimatedHashDataBytes
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
