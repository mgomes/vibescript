package vibes

import (
	"fmt"
	"reflect"
	"unsafe"
)

const (
	estimatedValueBytes        = 24
	estimatedStringHeaderBytes = 16
	estimatedSliceBaseBytes    = 24
	estimatedMapBaseBytes      = 48
	estimatedMapEntryBytes     = 32
	estimatedEnvBytes          = 16
	estimatedInstanceBytes     = 16
	estimatedBlockBytes        = 24
	estimatedCallFrameBytes    = 32
	estimatedModuleContextSize = 24
)

type memoryEstimator struct {
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
	return &memoryEstimator{
		seenEnvs:      make(map[*Env]struct{}),
		seenMaps:      make(map[uintptr]struct{}),
		seenSlices:    make(map[uintptr]struct{}),
		seenStrings:   make(map[stringIdentity]struct{}),
		seenClasses:   make(map[*ClassDef]struct{}),
		seenInstances: make(map[*Instance]struct{}),
		seenBlocks:    make(map[*Block]struct{}),
	}
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
		return fmt.Errorf("memory quota exceeded (%d bytes)", exec.memoryQuota)
	}
	return nil
}

func (exec *Execution) estimateMemoryUsage(extras ...Value) int {
	est := newMemoryEstimator()
	total := 0

	total += est.env(exec.root)
	for _, env := range exec.envStack {
		total += est.env(env)
	}
	for _, mod := range exec.modules {
		total += est.value(mod)
	}

	total += len(exec.callStack) * estimatedCallFrameBytes
	total += len(exec.receiverStack) * estimatedValueBytes
	total += estimatedMapBaseBytes + len(exec.moduleLoading)*estimatedMapEntryBytes
	for name := range exec.moduleLoading {
		total += estimatedStringHeaderBytes + len(name)
	}
	total += estimatedMapBaseBytes + len(exec.capabilityContracts)*estimatedMapEntryBytes
	total += estimatedMapBaseBytes + len(exec.capabilityContractScopes)*estimatedMapEntryBytes
	total += estimatedMapBaseBytes + len(exec.capabilityContractsByName)*estimatedMapEntryBytes
	for name := range exec.capabilityContractsByName {
		total += estimatedStringHeaderBytes + len(name)
	}
	total += estimatedSliceBaseBytes + len(exec.moduleLoadStack)*estimatedStringHeaderBytes
	for _, key := range exec.moduleLoadStack {
		total += len(key)
	}
	total += estimatedSliceBaseBytes + len(exec.moduleStack)*estimatedModuleContextSize
	for _, ctx := range exec.moduleStack {
		total += estimatedStringHeaderBytes*3 + len(ctx.key) + len(ctx.path) + len(ctx.root)
	}
	for _, extra := range extras {
		total += est.value(extra)
	}

	return total
}

func (est *memoryEstimator) env(env *Env) int {
	if env == nil {
		return 0
	}
	if _, seen := est.seenEnvs[env]; seen {
		return 0
	}
	est.seenEnvs[env] = struct{}{}

	size := estimatedEnvBytes + estimatedMapBaseBytes + len(env.values)*estimatedMapEntryBytes
	for name, val := range env.values {
		size += estimatedStringHeaderBytes + len(name)
		size += est.value(val)
	}
	size += est.env(env.parent)
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
		cl := val.Class()
		if cl == nil {
			return size
		}
		if _, seen := est.seenClasses[cl]; seen {
			return size
		}
		est.seenClasses[cl] = struct{}{}
		size += est.hash(cl.ClassVars)
	case KindInstance:
		inst := val.Instance()
		if inst == nil {
			return size
		}
		if _, seen := est.seenInstances[inst]; seen {
			return size
		}
		est.seenInstances[inst] = struct{}{}
		size += estimatedInstanceBytes
		size += est.hash(inst.Ivars)
	case KindBlock:
		blk := val.Block()
		if blk == nil {
			return size
		}
		if _, seen := est.seenBlocks[blk]; seen {
			return size
		}
		est.seenBlocks[blk] = struct{}{}
		size += estimatedBlockBytes + estimatedSliceBaseBytes + len(blk.Params)*estimatedStringHeaderBytes
		for _, param := range blk.Params {
			size += len(param.Name)
		}
		size += estimatedStringHeaderBytes*3 + len(blk.moduleKey) + len(blk.modulePath) + len(blk.moduleRoot)
		size += est.env(blk.Env)
	case KindFunction, KindBuiltin:
		// Functions and builtins are compile-time/static artifacts for memory quotas.
	}

	return size
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
	est.seenStrings[key] = struct{}{}
	return len(str)
}

func (est *memoryEstimator) slice(values []Value) int {
	size := estimatedSliceBaseBytes + cap(values)*estimatedValueBytes
	if cap(values) == 0 {
		return size
	}

	id := reflect.ValueOf(values).Pointer()
	if id != 0 {
		if _, seen := est.seenSlices[id]; seen {
			return 0
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

func (est *memoryEstimator) hash(values map[string]Value) int {
	id := reflect.ValueOf(values).Pointer()
	if id != 0 {
		if _, seen := est.seenMaps[id]; seen {
			return 0
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
