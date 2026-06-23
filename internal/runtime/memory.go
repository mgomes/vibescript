package runtime

import (
	"fmt"
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

// incrementalArrayCharger charges the elements of a locally accumulating result
// array against the memory quota one element at a time, in O(sum of element
// sizes) total rather than re-walking the whole accumulated array on every
// append (which is O(n^2) over the loop). Builtins like array.filter_map build a
// result slice that is unreachable from exec's roots while the loop runs, so it
// is invisible to step()'s slow-path checkMemory() and is only walked by the
// post-call check after the whole receiver has been traversed; without an
// incremental charge a block returning an individually quota-sized value per
// element could pile up far beyond the quota before that check ever ran.
//
// The charger walks the base roots once into a persistent estimator (so each
// element is deduplicated against the base and against earlier elements exactly
// as the single-pass post-call check would), then per element walks only that
// element and adds its size to a running total. Reusing one estimator means the
// charger never charges for backing storage the post-call check would have
// deduplicated, so it never rejects a result the post-call check would accept,
// and counting every element's payload means it never under-counts.
type incrementalArrayCharger struct {
	exec      *Execution
	est       *memoryEstimator
	baseBytes int
	itemBytes int
}

// newIncrementalArrayCharger returns a charger seeded with the current base
// memory usage, or nil when the memory quota is disabled (in which case callers
// skip charging entirely). The returned charger owns its estimator; the caller
// must call release when finished so the estimator's seen-sets are cleared.
func (exec *Execution) newIncrementalArrayCharger() *incrementalArrayCharger {
	if exec.memoryQuota <= 0 {
		return nil
	}
	est := newMemoryEstimator()
	return &incrementalArrayCharger{
		exec:      exec,
		est:       est,
		baseBytes: exec.estimateMemoryUsageBase(est),
	}
}

// add charges one element appended to a result array of the given backing
// capacity and reports an error when the projected total exceeds the quota. cap
// is the capacity of the result slice after the append so the array's backing
// overhead is accounted for as it grows, matching the post-call slice estimate.
func (c *incrementalArrayCharger) add(item Value, capacity int) error {
	if c == nil {
		return nil
	}
	c.itemBytes = saturatingAdd(c.itemBytes, c.est.value(item))
	used := saturatingAdd(c.baseBytes, estimatedValueBytes+estimatedSliceBaseBytes)
	used = saturatingAdd(used, saturatingMul(capacity, estimatedValueBytes))
	used = saturatingAdd(used, c.itemBytes)
	if used > c.exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, c.exec.memoryQuota)
	}
	return nil
}

// release clears the estimator's seen-sets so its backing maps can be reused or
// garbage collected. It is safe to call on a nil charger.
func (c *incrementalArrayCharger) release() {
	if c == nil {
		return
	}
	c.est.reset()
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
