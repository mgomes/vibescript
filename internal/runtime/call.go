package runtime

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// blockGivenInCurrentCall reports whether the call that owns env was supplied a
// block, mirroring Ruby's block_given?. It returns false at the top level and in
// calls that received no block. The block is read from the enclosing call
// frame's dedicated slot, so a script binding cannot shadow the predicate.
func blockGivenInCurrentCall(env *Env) bool {
	block, ok := env.lookupCallBlock()
	return ok && block.Kind() != KindNil
}

func valueCanContainBuiltins(val Value) bool {
	switch val.Kind() {
	case KindBuiltin, KindArray, KindHash, KindObject, KindClass, KindInstance, KindFunction, KindBlock:
		return true
	default:
		return false
	}
}

func cloneBuiltinSet(src map[*Builtin]struct{}) map[*Builtin]struct{} {
	if len(src) == 0 {
		return make(map[*Builtin]struct{})
	}
	out := make(map[*Builtin]struct{}, len(src))
	for builtin := range src {
		out[builtin] = struct{}{}
	}
	return out
}

// revokedCapabilityBuiltin returns a builtin that fails closed when invoked. The
// inbound rebinder substitutes it for a per-call capability grant a re-entering
// closure captured, so a closure that copied a capability into a local cannot
// reach the originating call's capability from a call that never granted it.
func revokedCapabilityBuiltin(name string) Value {
	return NewBuiltin(name, func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewNil(), fmt.Errorf("capability %s was not granted to this call", name)
	})
}

func (exec *Execution) autoInvokeIfNeeded(expr Expression, val, receiver Value) (Value, error) {
	switch val.Kind() {
	case KindFunction:
		fn := valueFunction(val)
		if fn != nil && len(fn.Params) == 0 {
			return exec.invokeCallable(val, receiver, nil, nil, NewNil(), expr.Pos())
		}
	case KindBuiltin:
		builtin := valueBuiltin(val)
		if builtin != nil && builtin.AutoInvoke {
			return exec.invokeCallable(val, receiver, nil, nil, NewNil(), expr.Pos())
		}
	}
	return val, nil
}

func memberReceiverAutoInvokes(property string) bool {
	return property != "call"
}

func (exec *Execution) invokeCallable(callee, receiver Value, args []Value, kwargs map[string]Value, block Value, pos Position) (Value, error) {
	if err := exec.checkContext(); err != nil {
		return NewNil(), err
	}

	switch callee.Kind() {
	case KindFunction:
		result, err := exec.callFunction(valueFunction(callee), receiver, args, kwargs, block, pos)
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				return NewNil(), exec.localJumpErrorAt(pos, "break cannot cross call boundary")
			}
			if errors.Is(err, errLoopNext) {
				return NewNil(), exec.localJumpErrorAt(pos, "next cannot cross call boundary")
			}
			return NewNil(), err
		}
		return result, nil
	case KindBlock:
		if len(kwargs) > 0 {
			for name := range kwargs {
				return NewNil(), exec.errorAt(pos, "unexpected keyword argument %s", name)
			}
		}
		if !block.IsNil() {
			return NewNil(), exec.errorAt(pos, "block.call does not accept a block")
		}
		result, err := exec.CallBlock(callee, args)
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				return NewNil(), exec.localJumpErrorAt(pos, "break cannot cross call boundary")
			}
			if errors.Is(err, errLoopNext) {
				return NewNil(), exec.localJumpErrorAt(pos, "next cannot cross call boundary")
			}
			return NewNil(), err
		}
		return result, nil
	case KindBuiltin:
		builtin := valueBuiltin(callee)
		scope := exec.capabilityContractScopes[builtin]
		var preCallKnownBuiltins map[*Builtin]struct{}
		if scope != nil && len(scope.contracts) > 0 {
			preCallKnownBuiltins = cloneBuiltinSet(scope.knownBuiltins)
			preCallScanner := newCapabilityContractScanner()
			preCallScanner.ambientEnvs = ambientEnvSet(exec.root)
			if valueCanContainBuiltins(receiver) {
				preCallScanner.collectBuiltins(receiver, preCallKnownBuiltins)
			}
			// A script-supplied block is a closure separate from args/kwargs.
			// Now that block environments are traversed for contract binding,
			// snapshot any builtins it already captured so a capability that
			// returns or stores the same block doesn't treat them as newly
			// published and bind its contract to them.
			if valueCanContainBuiltins(block) {
				preCallScanner.collectBuiltins(block, preCallKnownBuiltins)
			}
			for _, arg := range args {
				if !valueCanContainBuiltins(arg) {
					continue
				}
				preCallScanner.collectBuiltins(arg, preCallKnownBuiltins)
			}
			for _, kwarg := range kwargs {
				if !valueCanContainBuiltins(kwarg) {
					continue
				}
				preCallScanner.collectBuiltins(kwarg, preCallKnownBuiltins)
			}
			for _, root := range scope.roots {
				if !valueCanContainBuiltins(root) {
					continue
				}
				preCallScanner.collectBuiltins(root, preCallKnownBuiltins)
			}
		}
		contract, hasContract := exec.capabilityContracts[builtin]
		argsValidated := false
		if hasContract && contract.ValidateArgs != nil {
			if err := contract.ValidateArgs(args, kwargs, block); err != nil {
				return NewNil(), exec.wrapError(err, pos)
			}
			argsValidated = true
		}

		var popValidatedArgs func()
		if argsValidated {
			popValidatedArgs = exec.pushValidatedCapabilityArgs(builtin.Name)
		}
		result, err := builtin.Fn(exec, receiver, args, kwargs, block)
		if popValidatedArgs != nil {
			popValidatedArgs()
		}
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				return NewNil(), exec.localJumpErrorAt(pos, "break cannot cross call boundary")
			}
			if errors.Is(err, errLoopNext) {
				return NewNil(), exec.localJumpErrorAt(pos, "next cannot cross call boundary")
			}
			if ctxErr := exec.checkContext(); ctxErr != nil {
				return NewNil(), ctxErr
			}
			return NewNil(), exec.wrapError(err, pos)
		}
		if err := exec.checkContext(); err != nil {
			return NewNil(), err
		}
		if hasContract && contract.ValidateReturn != nil && !contract.ReturnValidatedByBuiltin {
			if err := contract.ValidateReturn(result); err != nil {
				return NewNil(), exec.wrapError(err, pos)
			}
		}
		if scope != nil && len(scope.contracts) > 0 {
			postCallScanner := newCapabilityContractScanner()
			postCallScanner.excluded = preCallKnownBuiltins
			postCallScanner.ambientEnvs = ambientEnvSet(exec.root)
			// Capability methods can lazily publish additional builtins at runtime
			// (e.g. through factory return values or receiver mutation). Re-scan
			// these values so future calls still enforce declared contracts.
			postCallScanner.bindContracts(result, scope, exec.capabilityContracts, exec.capabilityContractScopes)
			if receiver.Kind() != KindNil {
				postCallScanner.bindContracts(receiver, scope, exec.capabilityContracts, exec.capabilityContractScopes)
			}
			// Methods can mutate sibling scope roots via captured references; refresh
			// all adapter roots so newly exposed builtins also get bound.
			for _, root := range scope.roots {
				postCallScanner.bindContracts(root, scope, exec.capabilityContracts, exec.capabilityContractScopes)
			}
			// Methods can also publish builtins by mutating positional or keyword
			// argument objects supplied by script code.
			for _, arg := range args {
				if !valueCanContainBuiltins(arg) {
					continue
				}
				postCallScanner.bindContracts(arg, scope, exec.capabilityContracts, exec.capabilityContractScopes)
			}
			for _, kwarg := range kwargs {
				if !valueCanContainBuiltins(kwarg) {
					continue
				}
				postCallScanner.bindContracts(kwarg, scope, exec.capabilityContracts, exec.capabilityContractScopes)
			}
		}
		return result, nil
	default:
		return NewNil(), exec.errorAt(pos, "attempted to call non-callable value")
	}
}

func (exec *Execution) callFunction(fn *ScriptFunction, receiver Value, args []Value, kwargs map[string]Value, block Value, pos Position) (Value, error) {
	return exec.callFunctionWithReturnValidation(fn, receiver, args, kwargs, block, pos, true)
}

func (exec *Execution) callFunctionIgnoringReturn(fn *ScriptFunction, receiver Value, args []Value, kwargs map[string]Value, block Value, pos Position) (Value, error) {
	return exec.callFunctionWithReturnValidation(fn, receiver, args, kwargs, block, pos, false)
}

func (exec *Execution) callFunctionWithReturnValidation(fn *ScriptFunction, receiver Value, args []Value, kwargs map[string]Value, block Value, pos Position, validateReturn bool) (Value, error) {
	callEnv := newEnvWithCapacity(fn.Env, len(fn.Params)+1)
	if receiver.Kind() != KindNil {
		callEnv.Define("self", receiver)
	}
	callEnv.setCallBlock(block)
	if err := exec.bindFunctionArgs(fn, callEnv, args, kwargs, pos); err != nil {
		return NewNil(), err
	}
	exec.pushEnv(callEnv)
	if err := exec.checkMemory(); err != nil {
		exec.popEnv()
		return NewNil(), err
	}
	exec.popEnv()
	if err := exec.pushFrame(fn.Name, pos, exec.currentSourceScript(), fn.owner); err != nil {
		return NewNil(), err
	}

	ctx := moduleContext{}
	if fn.owner != nil {
		ctx = moduleContext{
			key:    fn.owner.moduleKey,
			path:   fn.owner.modulePath,
			root:   fn.owner.moduleRoot,
			script: fn.owner,
		}
	}
	exec.pushModuleContext(ctx)
	exec.pushReceiver(receiver)
	val, returned, err := exec.evalStatements(fn.Body, callEnv)
	if err != nil && !isLoopControlSignal(err) {
		err = exec.wrapError(err, pos)
	}
	exec.popReceiver()
	exec.popModuleContext()
	exec.popFrame()
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkContext(); err != nil {
		return NewNil(), err
	}
	if validateReturn && fn.ReturnTy != nil {
		normalized, err := normalizeValueForType(val, fn.ReturnTy, typeContext{
			owner:    fn.owner,
			env:      fn.Env,
			fallback: exec.root,
			exec:     exec,
		})
		if err != nil {
			if isHostControlSignal(err) {
				return NewNil(), err
			}
			if isNormalizationLimitError(err) {
				return NewNil(), exec.wrapError(err, pos)
			}
			return NewNil(), exec.errorAt(pos, "%s", formatReturnTypeMismatch(fn.Name, err))
		}
		val = normalized
	}
	if returned {
		return val, nil
	}
	return val, nil
}

type callFunctionRebinder struct {
	script        *Script
	root          *Env
	callClasses   map[string]*ClassDef
	callEnums     map[string]*EnumDef
	seenFunctions map[*ScriptFunction]*ScriptFunction
	seenInstances map[*Instance]Value
	seenArrays    map[sliceIdentity]Value
	// seenHashes caches rebound KindHash values keyed on the source hash's wrapper
	// identity. A hash reachable through several paths in the inbound graph rebinds
	// to one wrapper and keeps its identity, so a bound predicate rebound to that
	// same wrapper still reports identity against the rebound receiver. Keying on
	// the entry map alone would rebuild a fresh wrapper per path and break identity,
	// since hash identity is the wrapper.
	seenHashes map[uintptr]Value
	// seenHashEntries caches the rebound entry map keyed on the source hash's entry
	// map pointer. Two distinct hash wrappers may intentionally share one mutable
	// entry map (a host can build `a := NewHash(shared); b := NewHash(shared)`);
	// index assignment mutates that map in place, so a callee that does `a[:x] = 1`
	// must see the write through b. The wrapper cache cannot preserve this -- the
	// two wrappers have distinct identities -- so the entry-map cache lets both
	// rebound wrappers point at one cloned entry map and keep the aliasing.
	seenHashEntries map[uintptr]map[string]Value
	seenMaps        map[uintptr]map[string]Value
	seenBlocks      map[*Block]Value
	seenEnvs        map[*Env]*Env
	// seenBoundBuiltins caches the rebound clone of a receiver-bound predicate
	// (a bound eql?/equal?) keyed on the source builtin pointer. Rebinding such a
	// builtin reconstructs a fresh *Builtin around the rebound receiver, so the
	// same source builtin reached through two paths (two globals, two array slots)
	// would otherwise produce two distinct clones. equal? compares builtins by
	// backing pointer, so those distinct clones would wrongly report not-identical;
	// caching the clone keeps aliases of one bound predicate identical across the
	// host boundary.
	seenBoundBuiltins map[*Builtin]Value
}

func newCallFunctionRebinder(script *Script, root *Env, callClasses map[string]*ClassDef, callEnums map[string]*EnumDef) *callFunctionRebinder {
	return &callFunctionRebinder{
		script:      script,
		root:        root,
		callClasses: callClasses,
		callEnums:   callEnums,
	}
}

func (r *callFunctionRebinder) rebindValue(val Value) Value {
	switch val.Kind() {
	case KindBuiltin:
		builtin := valueBuiltin(val)
		if builtin == nil {
			return val
		}
		// A receiver-bound predicate (a bound eql?/equal?) rebinds to the rebound
		// clone of its captured receiver. The receiver flows through the same
		// rebinder, so it dedups with the same receiver appearing elsewhere in the
		// inbound graph and the rebound predicate reports identity against it. The
		// clone is reserved and cached before the receiver rebinds, so a receiver
		// graph that reaches the predicate bound to it (for example `[p, a]` where
		// `a[0]` is the same `p = a.eql?`) dedups against the reserved clone instead
		// of minting a second one the outer call would then overwrite — which would
		// make the callee observe arg[0].equal?(arg[1][0]) == false even though the
		// inbound graph held one predicate object. Left unchanged, the predicate
		// would keep comparing against the receiver's pre-rebind wrapper while the
		// receiver passed alongside rebinds to a fresh one.
		if builtin.BoundReceiver != nil {
			if clone, ok := r.seenBoundBuiltins[builtin]; ok {
				return clone
			}
			clone, clonedCell := builtin.BoundReceiver.reserve()
			if r.seenBoundBuiltins == nil {
				r.seenBoundBuiltins = make(map[*Builtin]Value)
			}
			r.seenBoundBuiltins[builtin] = clone
			reboundReceiver := r.rebindValue(builtin.BoundReceiver.receiver.value)
			setBoundReceiver(valueBuiltin(clone), clonedCell, reboundReceiver)
			return clone
		}
		// A capability copied into a local (for example `cap = jobs` captured by a
		// Hash.new default proc) would otherwise survive re-rooting and stay
		// callable, letting a missing-key lookup invoke a capability the re-entering
		// call never granted -- the ambient root is re-rooted but a local snapshot
		// bypasses that lookup. Revoke the captured grant so invoking it fails
		// closed; a free reference to the live capability global still resolves
		// through the re-rooted ambient root. All other builtins are preserved
		// unchanged.
		if !builtin.Capability {
			return val
		}
		return revokedCapabilityBuiltin(builtin.Name)
	case KindInstance:
		inst := valueInstance(val)
		if inst == nil || inst.Class == nil || inst.Class.owner != r.script {
			return val
		}
		if clone, ok := r.seenInstances[inst]; ok {
			return clone
		}
		reboundClass, ok := r.callClasses[inst.Class.Name]
		if !ok {
			return val
		}
		clonedIvars := make(map[string]Value, len(inst.Ivars))
		cloned := NewInstance(&Instance{Class: reboundClass, Ivars: clonedIvars})
		if r.seenInstances == nil {
			r.seenInstances = make(map[*Instance]Value)
		}
		r.seenInstances[inst] = cloned
		for name, ivar := range inst.Ivars {
			clonedIvars[name] = r.rebindValue(ivar)
		}
		return cloned
	case KindClass:
		classDef := valueClass(val)
		if classDef == nil || classDef.owner != r.script {
			return val
		}
		if rebound, ok := r.callClasses[classDef.Name]; ok {
			return NewClass(rebound)
		}
		return val
	case KindEnum:
		enumDef := valueEnum(val)
		if enumDef == nil || enumDef.owner != r.script {
			return val
		}
		if rebound, ok := r.callEnums[enumDef.Name]; ok {
			return NewEnum(rebound)
		}
		return val
	case KindEnumValue:
		member := valueEnumValue(val)
		if member == nil || member.Enum == nil || member.Enum.owner != r.script {
			return val
		}
		if reboundEnum, ok := r.callEnums[member.Enum.Name]; ok {
			if reboundMember, ok := reboundEnum.Members[member.Name]; ok {
				return NewEnumValue(reboundMember)
			}
			if reboundMember, ok := reboundEnum.MembersByKey[member.Symbol]; ok {
				return NewEnumValue(reboundMember)
			}
		}
		return val
	case KindFunction:
		fn := valueFunction(val)
		if fn == nil || fn.owner != r.script || fn.Env == r.root {
			return val
		}
		if clone, ok := r.seenFunctions[fn]; ok {
			return NewFunction(clone)
		}
		clone := cloneFunctionForEnv(fn, r.root)
		if r.seenFunctions == nil {
			r.seenFunctions = make(map[*ScriptFunction]*ScriptFunction)
		}
		r.seenFunctions[fn] = clone
		return NewFunction(clone)
	case KindBlock:
		// A block (e.g. a hash default proc) that escaped a prior call and is
		// passed back in must resolve globals, capabilities, per-call function
		// clones, and builtins against the live call root, not the stale snapshot
		// captured when it escaped -- otherwise a missing-key lookup could read a
		// previous call's globals or invoke a capability the current call never
		// granted. Re-root only the ambient root of its captured environment onto
		// the current call, preserving any local frames the block legitimately
		// closed over (e.g. a `prefix` parameter of the function that produced the
		// hash). Block parameters (e.g. the hash and key) bind at call time and
		// are unaffected.
		blk := valueBlock(val)
		if blk == nil || blk.owner != r.script || blk.Env == r.root {
			return val
		}
		if clone, ok := r.seenBlocks[blk]; ok {
			return clone
		}
		clone := *blk
		clone.Env = r.rebindCapturedEnv(blk.Env)
		cloneVal := wrapBlock(&clone)
		if r.seenBlocks == nil {
			r.seenBlocks = make(map[*Block]Value)
		}
		r.seenBlocks[blk] = cloneVal
		return cloneVal
	case KindArray:
		items := val.Array()
		id := sliceIdentity{
			Ptr: reflect.ValueOf(items).Pointer(),
			Len: len(items),
			Cap: cap(items),
		}
		if clone, seen := r.seenArrays[id]; seen {
			return clone
		}
		clonedItems := make([]Value, len(items))
		clonedArray := NewArray(clonedItems)
		if r.seenArrays == nil {
			r.seenArrays = make(map[sliceIdentity]Value)
		}
		r.seenArrays[id] = clonedArray
		for i := range items {
			clonedItems[i] = r.rebindValue(items[i])
		}
		return clonedArray
	case KindHash:
		id := hashIdentity(val)
		if id != 0 {
			if clone, seen := r.seenHashes[id]; seen {
				return clone
			}
		}
		typedEntries := hashHasTypedEntries(val)
		// Only the legacy string-key map participates in shared-entry dedup. A
		// typed hash rebinds through HashEntries() below, so avoid materializing
		// its lossy string-key map here at all.
		var entries map[string]Value
		var entriesPtr uintptr
		var sharedEntries map[string]Value
		var sharedSeen bool
		if !typedEntries {
			entries = val.Hash()
			entriesPtr = reflect.ValueOf(entries).Pointer()
			// A distinct wrapper that shares this entry map already cloned it;
			// reuse that cloned map so both rebound wrappers mutate one map in
			// place and the host's intentional aliasing survives rebinding. The
			// shared map is already fully populated, so skip the fill loop -- only
			// a fresh wrapper (with this wrapper's own rebound defaults) is built
			// around it.
			sharedEntries, sharedSeen = r.seenHashEntries[entriesPtr]
		}
		clonedEntries := sharedEntries
		if !sharedSeen {
			clonedEntries = make(map[string]Value, val.HashLen())
		}
		defaultValue := hashDefaultValue(val)
		defaultProc := hashDefaultProc(val)
		hasDefault := !defaultValue.IsNil() || !defaultProc.IsNil()
		var cloned Value
		if hasDefault {
			cloned = NewHashWithDefault(clonedEntries, NewNil(), NewNil())
		} else {
			cloned = NewHash(clonedEntries)
		}
		// Register the wrapper before rebinding defaults or entries so a hash that
		// contains itself -- whether through an entry or through a default that
		// reaches the hash (e.g. Hash.new { |_, _| h }) -- rebinds against this
		// clone rather than recursing forever or rebinding a second wrapper.
		if id != 0 {
			if r.seenHashes == nil {
				r.seenHashes = make(map[uintptr]Value)
			}
			r.seenHashes[id] = cloned
		}
		if !typedEntries && !sharedSeen && entriesPtr != 0 {
			if r.seenHashEntries == nil {
				r.seenHashEntries = make(map[uintptr]map[string]Value)
			}
			r.seenHashEntries[entriesPtr] = clonedEntries
		}
		if hasDefault {
			clonedDefaultValue := NewNil()
			clonedDefaultProc := NewNil()
			if !defaultValue.IsNil() {
				clonedDefaultValue = r.rebindValue(defaultValue)
			}
			if !defaultProc.IsNil() {
				clonedDefaultProc = r.rebindValue(defaultProc)
			}
			cloned.SetHashDefaults(clonedDefaultValue, clonedDefaultProc)
		}
		if !sharedSeen {
			if typedEntries {
				for _, entry := range val.HashEntries() {
					setClonedHashEntry(cloned, r.rebindValue(entry.Key), r.rebindValue(entry.Value))
				}
			} else {
				for key, item := range entries {
					clonedEntries[key] = r.rebindValue(item)
				}
			}
		}
		return cloned
	case KindObject:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if cloneMap, seen := r.seenMaps[ptr]; seen {
			return NewObject(cloneMap)
		}
		clonedEntries := make(map[string]Value, len(entries))
		if r.seenMaps == nil {
			r.seenMaps = make(map[uintptr]map[string]Value)
		}
		r.seenMaps[ptr] = clonedEntries
		for key, item := range entries {
			clonedEntries[key] = r.rebindValue(item)
		}
		return NewObject(clonedEntries)
	default:
		return val
	}
}

// rebindCapturedEnv re-roots the captured environment of an escaped closure onto
// the current call. A closure that escaped a prior Script.Call captures a chain
// of local frames (e.g. the parameters of the function that produced it) that
// bottoms out in the originating call's ambient root (globals, capabilities,
// per-call function clones). Only that ambient root is stale; the local frames
// hold values the closure legitimately closed over and must be preserved. Each
// local frame is cloned so the live call cannot mutate the escaped closure's
// captured state, its bound values are rebound (they may reference per-call
// functions, classes, or further escaped closures), and the deepest local
// frame's parent is re-rooted onto the current call root. If the closure captured
// the ambient root directly (no local frames), the current root replaces it.
func (r *callFunctionRebinder) rebindCapturedEnv(env *Env) *Env {
	// Re-root at the originating call's ambient root (and discard the builtin
	// proto beneath it): the live call root carries the current globals,
	// capabilities, per-call function clones, and chains to the live proto.
	if env == nil || env.callRoot {
		return r.root
	}
	if clone, ok := r.seenEnvs[env]; ok {
		return clone
	}
	clone := newEnvWithCapacity(nil, env.dynamicLen())
	clone.assignBoundary = env.assignBoundary
	if r.seenEnvs == nil {
		r.seenEnvs = make(map[*Env]*Env)
	}
	r.seenEnvs[env] = clone
	clone.parent = r.rebindCapturedEnv(env.parent)
	env.rangeDynamicBindings(func(name string, val Value) {
		clone.Define(name, r.rebindValue(val))
	})
	env.rangeStaticBindings(func(name string, val Value) {
		clone.DefineStatic(name, r.rebindValue(val))
	})
	// A call frame captured by an escaped closure carries the block its method
	// received; preserve and rebind it so a re-entering closure's yield or
	// block_given? still resolves to that block re-rooted onto the live call.
	if env.hasCallBlock {
		clone.setCallBlock(r.rebindValue(env.callBlock))
	}
	return clone
}

func (r *callFunctionRebinder) rebindValues(values []Value) []Value {
	if len(values) == 0 {
		return values
	}
	out := make([]Value, len(values))
	for i, val := range values {
		out[i] = r.rebindValue(val)
	}
	return out
}

func (r *callFunctionRebinder) rebindKeywords(kwargs map[string]Value) map[string]Value {
	if len(kwargs) == 0 {
		return kwargs
	}
	out := make(map[string]Value, len(kwargs))
	for name, val := range kwargs {
		out[name] = r.rebindValue(val)
	}
	return out
}

func bindCapabilitiesForCall(exec *Execution, root *Env, rebinder *callFunctionRebinder, capabilities []CapabilityAdapter) error {
	if len(capabilities) == 0 {
		return nil
	}
	if exec.capabilityContracts == nil {
		exec.capabilityContracts = make(map[*Builtin]CapabilityMethodContract)
	}
	if exec.capabilityContractScopes == nil {
		exec.capabilityContractScopes = make(map[*Builtin]*capabilityContractScope)
	}
	if exec.capabilityContractsByName == nil {
		exec.capabilityContractsByName = make(map[string]CapabilityMethodContract)
	}

	binding := CapabilityBinding{Context: exec.ctx, Engine: exec.engine}
	ambientEnvs := ambientEnvSet(root)
	for _, adapter := range capabilities {
		if adapter == nil {
			continue
		}
		scope := &capabilityContractScope{
			contracts:     map[string]CapabilityMethodContract{},
			knownBuiltins: make(map[*Builtin]struct{}),
		}
		if provider, ok := adapter.(CapabilityContractProvider); ok {
			for methodName, contract := range provider.CapabilityContracts() {
				name := strings.TrimSpace(methodName)
				if name == "" {
					return fmt.Errorf("capability contract method name must be non-empty")
				}
				if _, exists := exec.capabilityContractsByName[name]; exists {
					return fmt.Errorf("duplicate capability contract for %s", name)
				}
				exec.capabilityContractsByName[name] = contract
				scope.contracts[name] = contract
			}
		}
		globals, err := adapter.Bind(binding)
		if err != nil {
			if ctxErr := exec.checkContext(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("bind capability: %w", err)
		}
		if err := exec.checkContext(); err != nil {
			return err
		}
		for name, val := range globals {
			if err := exec.checkContext(); err != nil {
				return err
			}
			rebound := rebinder.rebindValue(val)
			root.Define(name, rebound)
			if len(scope.contracts) > 0 {
				scope.roots = append(scope.roots, rebound)
			}
			// Mark every builtin this adapter exposes as a per-call capability
			// grant. The marker lets the inbound rebinder revoke a captured grant
			// when a closure (for example a Hash.new default proc that copied a
			// capability into a local) escapes and re-enters a later call that did
			// not grant the same capability.
			markCapabilityBuiltins(rebound)
			// Skip the ambient global chain (root + ancestors) when walking a
			// capability-supplied closure's captured environment, matching the
			// pre/post-call scanners above. Otherwise a contract method whose
			// name happens to match a pre-existing global builtin would bind to
			// that global through a closure rooted in the ambient env.
			scanner := newCapabilityContractScanner()
			scanner.ambientEnvs = ambientEnvs
			scanner.bindContracts(rebound, scope, exec.capabilityContracts, exec.capabilityContractScopes)
		}
	}

	return nil
}

func initializeClassBodiesForCall(exec *Execution, env *Env, callClasses map[string]*ClassDef, order []string, skip map[string]struct{}) error {
	for _, name := range order {
		classDef, ok := callClasses[name]
		if !ok {
			continue
		}
		if _, deferred := skip[name]; deferred {
			continue
		}
		if len(classDef.Body) == 0 {
			continue
		}
		classVal, ok := env.Get(name)
		if !ok {
			return exec.errorAt(classDef.Body[0].Pos(), "class %s is not bound", name)
		}
		if err := exec.initializeClassBody(classVal, classDef, env); err != nil {
			return exec.wrapError(err, classDef.Body[0].Pos())
		}
		if err := exec.checkContext(); err != nil {
			return err
		}
	}

	return nil
}

func (exec *Execution) initializeClassBody(classVal Value, classDef *ClassDef, parent *Env) error {
	if classDef == nil || len(classDef.Body) == 0 || classDef.bodyRan {
		return nil
	}
	env := newEnv(parent)
	env.Define("self", classVal)
	exec.pushReceiver(classVal)
	defer exec.popReceiver()
	_, _, err := exec.evalStatements(classDef.Body, env)
	if err != nil {
		return err
	}
	classDef.bodyRan = true
	return nil
}

func prepareCallEnvForFunction(exec *Execution, root *Env, rebinder *callFunctionRebinder, fn *ScriptFunction, args []Value, keywords map[string]Value) (*Env, error) {
	if err := exec.checkContext(); err != nil {
		return nil, err
	}

	callEnv := newEnvWithCapacity(root, len(fn.Params))
	// The host entry call never supplies a block, but the frame is still a call
	// frame: mark it with a nil block so block_given? reports false, yield
	// raises, and a &block parameter binds nil, keeping the invariant that every
	// call frame carries its own block slot.
	callEnv.setCallBlock(NewNil())
	callArgs := rebinder.rebindValues(args)
	callKeywords := rebinder.rebindKeywords(keywords)
	if err := exec.bindFunctionArgs(fn, callEnv, callArgs, callKeywords, fn.Pos); err != nil {
		if isHostControlSignal(err) {
			return nil, err
		}
		return nil, fmt.Errorf("bind function args: %w", err)
	}
	exec.pushEnv(callEnv)
	if err := exec.checkMemory(); err != nil {
		exec.popEnv()
		return nil, fmt.Errorf("check memory after binding call env: %w", err)
	}
	exec.popEnv()

	return callEnv, nil
}

func newExecutionForCall(script *Script, ctx context.Context, root *Env, opts CallOptions) *Execution {
	childCallOptions := CallOptions{
		Globals:      opts.Globals,
		Capabilities: opts.Capabilities,
		AllowRequire: opts.AllowRequire,
	}
	exec := &Execution{
		engine:        script.engine,
		script:        script,
		ctx:           ctx,
		quota:         script.engine.config.StepQuota,
		memoryQuota:   script.engine.config.MemoryQuotaBytes,
		recursionCap:  script.engine.config.RecursionLimit,
		root:          root,
		strictEffects: script.engine.config.StrictEffects,
		allowRequire:  opts.AllowRequire,
		callOptions:   childCallOptions,
	}
	// The module stacks stay nil: most calls never require a module,
	// and append allocates them on first use.
	exec.callStack = exec.callStackArr[:0]
	exec.receiverStack = exec.receiverStackArr[:0]
	exec.envStack = exec.envStackArr[:0]
	exec.validatedCapabilityArgs = exec.validatedCapabilityArgsArr[:0]
	return exec
}

func (exec *Execution) evalCallTarget(call *CallExpr, env *Env) (Value, Value, error) {
	if member, ok := call.Callee.(*MemberExpr); ok {
		receiver, err := exec.evalExpressionWithAuto(member.Object, env, memberReceiverAutoInvokes(member.Property))
		if err != nil {
			return NewNil(), NewNil(), err
		}
		if err := exec.checkMemoryWith(receiver); err != nil {
			return NewNil(), NewNil(), err
		}
		if directCallee, handled, err := exec.evalDirectPublicMemberMethodCall(receiver, member.Property, member.Pos()); handled || err != nil {
			if err != nil {
				return NewNil(), NewNil(), err
			}
			return directCallee, receiver, nil
		}
		callee, err := exec.getPublicMember(receiver, member.Property, member.Pos())
		if err != nil {
			return NewNil(), NewNil(), err
		}
		return callee, receiver, nil
	}

	if ident, ok := call.Callee.(*Identifier); ok {
		return exec.evalIdentifierCallTarget(ident, env)
	}

	callee, err := exec.evalExpressionWithAuto(call.Callee, env, false)
	if err != nil {
		return NewNil(), NewNil(), err
	}
	return callee, NewNil(), nil
}

// evalIdentifierCallTarget resolves a bare identifier used as a call target. A
// local variable binds with a nil receiver (it is a free-standing callable),
// while an identifier that falls through to an implicit-self member binds self
// as the receiver so builtins resolved off self (such as the universal
// introspection predicates) receive the correct receiver.
func (exec *Execution) evalIdentifierCallTarget(ident *Identifier, env *Env) (Value, Value, error) {
	// Mirror the per-expression step charged by evalExpressionWithAuto, which
	// this branch replaces for identifier callees, so step accounting (and the
	// statement position a step-quota limit reports) is unchanged.
	if err := exec.step(); err != nil {
		return NewNil(), NewNil(), err
	}
	if val, ok := env.Get(ident.Name); ok {
		env.clearArrayAppendBuffer(ident.Name)
		return val, NewNil(), nil
	}
	if self, hasSelf := env.Get("self"); hasSelf && (self.Kind() == KindInstance || self.Kind() == KindClass) {
		member, err := exec.getMember(self, ident.Name, ident.Pos())
		if err != nil {
			return NewNil(), NewNil(), err
		}
		return member, self, nil
	}
	return NewNil(), NewNil(), exec.errorAt(ident.Pos(), "undefined variable %s%s", ident.Name, didYouMean(ident.Name, env.visibleNames()))
}

func (exec *Execution) evalDirectPublicMemberMethodCall(receiver Value, property string, pos Position) (Value, bool, error) {
	switch receiver.Kind() {
	case KindClass:
		if property == "new" {
			return NewNil(), false, nil
		}
		classDef := valueClass(receiver)
		fn, ok := classDef.ClassMethods[property]
		if !ok {
			return NewNil(), false, nil
		}
		if fn.Private {
			return NewNil(), true, exec.errorAt(pos, "private method %s", property)
		}
		return NewFunction(fn), true, nil
	case KindInstance:
		instance := valueInstance(receiver)
		fn, ok := instance.Class.Methods[property]
		if !ok {
			return NewNil(), false, nil
		}
		if fn.Private {
			return NewNil(), true, exec.errorAt(pos, "private method %s", property)
		}
		return NewFunction(fn), true, nil
	default:
		return NewNil(), false, nil
	}
}

func (exec *Execution) evalCallArgs(call *CallExpr, env *Env) ([]Value, error) {
	return exec.evalCallArgsForCallee(call, env, NewNil())
}

func (exec *Execution) evalCallArgsForCallee(call *CallExpr, env *Env, callee Value) ([]Value, error) {
	params, hasParams := callableParamTypes(callee)
	args := make([]Value, len(call.Args))
	for i, arg := range call.Args {
		expectsCallable := false
		if hasParams {
			if param, ok := positionalCallableParam(params, i); ok && paramExpectsCallableArgument(param) {
				expectsCallable = true
			}
		}
		val, err := exec.evalCallArgument(arg, env, expectsCallable)
		if err != nil {
			return nil, err
		}
		if err := exec.checkMemoryWith(val); err != nil {
			return nil, err
		}
		args[i] = val
	}
	return args, nil
}

func (exec *Execution) evalCallKwArgs(call *CallExpr, env *Env) (map[string]Value, error) {
	return exec.evalCallKwArgsForCallee(call, env, NewNil())
}

func (exec *Execution) evalCallKwArgsForCallee(call *CallExpr, env *Env, callee Value) (map[string]Value, error) {
	if len(call.KwArgs) == 0 {
		return nil, nil
	}
	params, hasParams := callableParamTypes(callee)
	kwargs := make(map[string]Value, len(call.KwArgs))
	for _, kw := range call.KwArgs {
		expectsCallable := false
		if hasParams {
			if param, ok := keywordCallableParam(params, kw.Name); ok && paramExpectsCallableArgument(param) {
				expectsCallable = true
			}
		}
		val, err := exec.evalCallArgument(kw.Value, env, expectsCallable)
		if err != nil {
			return nil, err
		}
		if err := exec.checkMemoryWith(val); err != nil {
			return nil, err
		}
		kwargs[kw.Name] = val
	}
	return kwargs, nil
}

func (exec *Execution) evalCallArgument(arg Expression, env *Env, expectsCallable bool) (Value, error) {
	if !expectsCallable {
		return exec.evalExpressionWithAuto(arg, env, true)
	}
	if val, ok, err := exec.evalBareCallableArgument(arg, env); ok || err != nil {
		return val, err
	}
	return exec.evalExpressionWithAuto(arg, env, false)
}

func (exec *Execution) evalBareCallableArgument(arg Expression, env *Env) (Value, bool, error) {
	call, ok := arg.(*CallExpr)
	if !ok || call.Parenthesized || len(call.Args) > 0 || len(call.KwArgs) > 0 || call.Block != nil {
		return NewNil(), false, nil
	}
	if _, ok := call.Callee.(*Identifier); !ok {
		return NewNil(), false, nil
	}
	callee, _, err := exec.evalCallTarget(call, env)
	if err != nil {
		return NewNil(), true, err
	}
	return callee, true, nil
}

func callableParamTypes(callee Value) ([]Param, bool) {
	switch callee.Kind() {
	case KindFunction:
		fn := valueFunction(callee)
		if fn == nil {
			return nil, false
		}
		return fn.Params, true
	case KindBlock:
		blk := valueBlock(callee)
		if blk == nil {
			return nil, false
		}
		return blk.Params, true
	case KindBuiltin:
		builtin := valueBuiltin(callee)
		if builtin == nil {
			return nil, false
		}
		if builtin.OptionsHashTarget != nil {
			return builtin.OptionsHashTarget.Params, true
		}
		if len(builtin.CapturedValues) == 1 && builtin.CapturedValues[0].Kind() == KindBlock {
			blk := valueBlock(builtin.CapturedValues[0])
			if blk != nil {
				return blk.Params, true
			}
		}
	}
	return nil, false
}

func positionalCallableParam(params []Param, argIndex int) (Param, bool) {
	positional := 0
	for _, param := range params {
		switch param.Kind {
		case ParamNormal:
			if positional == argIndex {
				return param, true
			}
			positional++
		case ParamRest:
			if argIndex >= positional {
				return param, true
			}
		}
	}
	return Param{}, false
}

func keywordCallableParam(params []Param, name string) (Param, bool) {
	for _, param := range params {
		switch param.Kind {
		case ParamKeyword, ParamNormal:
			if param.Name == name {
				return param, true
			}
		case ParamKeywordRest:
			return param, true
		}
	}
	return Param{}, false
}

func paramExpectsCallableArgument(param Param) bool {
	switch param.Kind {
	case ParamRest:
		return restParamExpectsCallableElement(param.Type)
	case ParamKeywordRest:
		return keywordRestParamExpectsCallableValue(param.Type)
	default:
		return typeExprIncludesCallable(param.Type)
	}
}

func restParamExpectsCallableElement(ty *TypeExpr) bool {
	if ty == nil {
		return false
	}
	if ty.Kind == TypeArray && len(ty.TypeArgs) > 0 {
		return typeExprIncludesCallable(ty.TypeArgs[0])
	}
	return typeExprIncludesCallable(ty)
}

func keywordRestParamExpectsCallableValue(ty *TypeExpr) bool {
	if ty == nil {
		return false
	}
	if ty.Kind == TypeHash && len(ty.TypeArgs) > 1 {
		return typeExprIncludesCallable(ty.TypeArgs[1])
	}
	return typeExprIncludesCallable(ty)
}

func typeExprIncludesCallable(ty *TypeExpr) bool {
	if ty == nil {
		return false
	}
	switch ty.Kind {
	case TypeFunction:
		return true
	case TypeUnion:
		for _, option := range ty.Union {
			if typeExprIncludesCallable(option) {
				return true
			}
		}
	}
	return false
}

// calleeResolution records how a call's callee was resolved, which decides
// whether a parenthesized call may collapse its keyword arguments into a
// positional options hash. The distinction matters only for member calls: a
// function value surfaced as a genuine method must stay strict, while a plain
// function value merely stored in a member collapses like any direct call.
type calleeResolution int

const (
	// calleeDirect marks a callee resolved from a non-member expression, such
	// as a local function or a function-valued variable.
	calleeDirect calleeResolution = iota
	// calleeMemberMethod marks a callee surfaced through the direct
	// member-method path: a genuine instance, class, or constructor method.
	calleeMemberMethod
	// calleeMemberValue marks a callee fetched as a stored member value, such
	// as a module function exposed on a namespace object.
	calleeMemberValue
)

// resolveKeywordOptionsHash collapses a call's keyword arguments into a trailing
// positional options hash when the callee has no matching keyword parameter and
// exposes a positional parameter to receive it. This mirrors Ruby's options-hash
// binding. Parenless calls collapse for any options-hash target. Parenthesized
// calls collapse for plain function calls (a function value, its `call` alias, or
// a function value held in a member) and constructors. Parenthesized ordinary
// methods stay strict. resolution reports how the callee was resolved, which the
// member paths use to tell genuine methods apart from stored function values that
// happen to surface as bare function values too.
func resolveKeywordOptionsHash(call *CallExpr, callee Value, resolution calleeResolution, args []Value, kwargs map[string]Value) ([]Value, map[string]Value) {
	if !call.KeywordOptionsHash || len(kwargs) == 0 {
		return args, kwargs
	}
	if !calleeCollapsesOptionsHash(call, callee, resolution) {
		return args, kwargs
	}
	fn := optionsHashTarget(callee)
	if fn == nil || !functionCanReceiveOptionsHash(fn, len(args), kwargs) {
		return args, kwargs
	}
	hash := make(map[string]Value, len(kwargs))
	for name, val := range kwargs {
		hash[name] = val
	}
	return append(args, NewHash(hash)), nil
}

// calleeCollapsesOptionsHash reports whether the resolved callee permits keyword
// arguments to collapse into a positional options hash for the given call form.
// The parenless form collapses for any options-hash target. Parenthesized calls
// keep ordinary method binding strict: a call to a plain function value collapses
// like a plain function call, whether that value was resolved directly or fetched
// from a member, constructors collapse through their initialize options target,
// and a member call collapses through a function value's direct-call alias as
// well. A callee surfaced through the direct member-method path stays strict,
// since that path surfaces methods as bare function values too.
func calleeCollapsesOptionsHash(call *CallExpr, callee Value, resolution calleeResolution) bool {
	if !call.Parenthesized {
		return true
	}
	builtin := valueBuiltin(callee)
	if builtin != nil && builtinCollapsesConstructorOptionsHash(builtin) {
		return true
	}
	switch resolution {
	case calleeMemberMethod:
		return false
	case calleeMemberValue:
		if callee.Kind() == KindFunction {
			return true
		}
		return builtin != nil && builtin.DirectCallAlias
	default:
		return callee.Kind() == KindFunction
	}
}

func builtinCollapsesConstructorOptionsHash(builtin *Builtin) bool {
	return builtin.OptionsHashTarget != nil && strings.HasSuffix(builtin.Name, ".new")
}

func optionsHashTarget(callee Value) *ScriptFunction {
	switch callee.Kind() {
	case KindFunction:
		return valueFunction(callee)
	case KindBuiltin:
		builtin := valueBuiltin(callee)
		if builtin == nil {
			return nil
		}
		return builtin.OptionsHashTarget
	default:
		return nil
	}
}

func functionCanReceiveOptionsHash(fn *ScriptFunction, positionalCount int, kwargs map[string]Value) bool {
	for _, param := range fn.Params {
		if param.Kind == ParamKeyword || param.Kind == ParamKeywordRest {
			return false
		}
	}
	for _, param := range fn.Params {
		switch param.Kind {
		case ParamNormal:
			if positionalCount > 0 {
				positionalCount--
				continue
			}
			_, keywordTargetsThisParam := kwargs[param.Name]
			return !keywordTargetsThisParam
		case ParamRest:
			return true
		}
	}
	return false
}

func (exec *Execution) evalCallBlock(call *CallExpr, env *Env) (Value, error) {
	if call.Block == nil {
		return NewNil(), nil
	}
	block, err := exec.evalBlockLiteral(call.Block, env)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(block); err != nil {
		return NewNil(), err
	}
	return block, nil
}

func (exec *Execution) checkCallMemoryRoots(receiver Value, args []Value, kwargs map[string]Value, block Value) error {
	return exec.checkCallMemoryRootsWithCallee(NewNil(), receiver, args, kwargs, block)
}

// checkCallMemoryRootsWithCallee charges the live call roots — the callee,
// receiver, arguments, keyword arguments, and block — against the memory quota
// before the call runs.
//
// The callee is included because a bound predicate builtin captures its receiver
// (see universalMember): the captured payload is reachable only through the
// callee value, not through the call's own receiver. A stored probe such as
// `probe = huge.eql?` is charged because the variable keeps the builtin in the
// environment, but an immediately invoked temporary callee such as
// `make_probe()(huge_arg)` lives only on the Go call stack, so without charging
// it here the captured receiver plus the outer arguments could exceed the quota
// unseen. Passing the callee through the same estimator deduplicates it against
// the environment, so a callee that is also reachable from a variable is counted
// once and the common static callee — a function, or a builtin with no captures —
// adds nothing.
func (exec *Execution) checkCallMemoryRootsWithCallee(callee, receiver Value, args []Value, kwargs map[string]Value, block Value) error {
	if !calleeCapturesRoots(callee) {
		if receiver.Kind() == KindNil && len(kwargs) == 0 && block.IsNil() {
			if len(args) == 0 {
				return nil
			}
			return exec.checkMemoryWith(args...)
		}
		return exec.checkMemoryWithCallRoots(NewNil(), receiver, args, kwargs, block)
	}
	return exec.checkMemoryWithCallRoots(callee, receiver, args, kwargs, block)
}

// calleeCapturesRoots reports whether a callee value carries captured runtime
// values that the call roots must charge — that is, a bound builtin (such as a
// stored or temporary eql?/equal? predicate) whose Fn closes over a receiver.
// Static callees (functions, or builtins without captures) carry no extra
// payload, so the common call path skips charging them.
func calleeCapturesRoots(callee Value) bool {
	if callee.Kind() != KindBuiltin {
		return false
	}
	builtin := valueBuiltin(callee)
	return builtin != nil && len(builtin.CapturedValues) > 0
}

func (exec *Execution) evalCallExpr(call *CallExpr, env *Env) (Value, error) {
	if member, ok := call.Callee.(*MemberExpr); ok {
		return exec.evalMemberCallExpr(call, member, env)
	}

	if ident, ok := call.Callee.(*Identifier); ok && ident.Name == blockGivenName {
		return exec.evalBlockGivenCall(call, env)
	}

	callee, receiver, err := exec.evalCallTarget(call, env)
	if err != nil {
		return NewNil(), err
	}
	args, err := exec.evalCallArgsForCallee(call, env, callee)
	if err != nil {
		return NewNil(), err
	}
	kwargs, err := exec.evalCallKwArgsForCallee(call, env, callee)
	if err != nil {
		return NewNil(), err
	}
	args, kwargs = resolveKeywordOptionsHash(call, callee, calleeDirect, args, kwargs)
	block, err := exec.evalCallBlock(call, env)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkContext(); err != nil {
		return NewNil(), err
	}
	if err := exec.checkCallMemoryRootsWithCallee(callee, receiver, args, kwargs, block); err != nil {
		return NewNil(), err
	}

	result, callErr := exec.invokeCallable(callee, receiver, args, kwargs, block, call.Pos())
	if callErr != nil {
		return NewNil(), callErr
	}
	if err := exec.checkMemoryWith(result); err != nil {
		return NewNil(), err
	}
	return result, nil
}

// evalBlockGivenCall handles the parenthesized block_given?() form. Like Ruby's
// Kernel#block_given?, it accepts no arguments and reports whether the enclosing
// call was supplied a block.
func (exec *Execution) evalBlockGivenCall(call *CallExpr, env *Env) (Value, error) {
	if len(call.Args) != 0 || len(call.KwArgs) != 0 {
		return NewNil(), exec.errorAt(call.Pos(), "%s takes no arguments", blockGivenName)
	}
	if call.Block != nil {
		return NewNil(), exec.errorAt(call.Pos(), "%s does not accept a block", blockGivenName)
	}
	return NewBool(blockGivenInCurrentCall(env)), nil
}

func (exec *Execution) evalMemberCallExpr(call *CallExpr, member *MemberExpr, env *Env) (Value, error) {
	receiver, err := exec.evalExpression(member.Object, env)
	if err != nil {
		return NewNil(), err
	}
	if call.Safe && receiver.Kind() == KindNil {
		return NewNil(), nil
	}
	if err := exec.checkMemoryWith(receiver); err != nil {
		return NewNil(), err
	}

	if canCallBuiltinMemberDirect(receiver, member.Property) {
		return exec.evalDirectBuiltinMemberCallExpr(call, receiver, member.Property, env)
	}

	var callee Value
	resolution := calleeMemberValue
	if directCallee, handled, err := exec.evalDirectPublicMemberMethodCall(receiver, member.Property, member.Pos()); handled || err != nil {
		if err != nil {
			return NewNil(), err
		}
		callee = directCallee
		resolution = calleeMemberMethod
	} else {
		var err error
		callee, err = exec.getPublicMember(receiver, member.Property, member.Pos())
		if err != nil {
			return NewNil(), err
		}
	}

	args, err := exec.evalCallArgsForCallee(call, env, callee)
	if err != nil {
		return NewNil(), err
	}
	kwargs, err := exec.evalCallKwArgsForCallee(call, env, callee)
	if err != nil {
		return NewNil(), err
	}
	args, kwargs = resolveKeywordOptionsHash(call, callee, resolution, args, kwargs)
	block, err := exec.evalCallBlock(call, env)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkContext(); err != nil {
		return NewNil(), err
	}
	if err := exec.checkCallMemoryRootsWithCallee(callee, receiver, args, kwargs, block); err != nil {
		return NewNil(), err
	}

	result, callErr := exec.invokeCallable(callee, receiver, args, kwargs, block, call.Pos())
	if callErr != nil {
		return NewNil(), callErr
	}
	if err := exec.checkMemoryWith(result); err != nil {
		return NewNil(), err
	}
	return result, nil
}

func (exec *Execution) evalDirectBuiltinMemberCallExpr(call *CallExpr, receiver Value, property string, env *Env) (Value, error) {
	args, err := exec.evalCallArgs(call, env)
	if err != nil {
		return NewNil(), err
	}
	kwargs, err := exec.evalCallKwArgs(call, env)
	if err != nil {
		return NewNil(), err
	}
	block, err := exec.evalCallBlock(call, env)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkContext(); err != nil {
		return NewNil(), err
	}
	if err := exec.checkCallMemoryRoots(receiver, args, kwargs, block); err != nil {
		return NewNil(), err
	}

	result, err := callBuiltinMemberDirect(exec, receiver, property, args, kwargs, block)
	if err != nil {
		if errors.Is(err, errLoopBreak) {
			return NewNil(), exec.localJumpErrorAt(call.Pos(), "break cannot cross call boundary")
		}
		if errors.Is(err, errLoopNext) {
			return NewNil(), exec.localJumpErrorAt(call.Pos(), "next cannot cross call boundary")
		}
		if ctxErr := exec.checkContext(); ctxErr != nil {
			return NewNil(), ctxErr
		}
		return NewNil(), exec.wrapError(err, call.Pos())
	}
	if err := exec.checkContext(); err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(result); err != nil {
		return NewNil(), err
	}
	return result, nil
}

func canCallBuiltinMemberDirect(receiver Value, property string) bool {
	switch receiver.Kind() {
	case KindDuration:
		return canCallDurationMemberDirect(property)
	case KindTime:
		return canCallTimeMemberDirect(property)
	default:
		return false
	}
}

func callBuiltinMemberDirect(exec *Execution, receiver Value, property string, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	switch receiver.Kind() {
	case KindDuration:
		return callDurationMemberDirect(receiver.Duration(), property, args, kwargs, block)
	case KindTime:
		return callTimeMemberDirect(exec, receiver.Time(), property, args, kwargs, block)
	default:
		return NewNil(), fmt.Errorf("unsupported member access on %s", receiver.Kind())
	}
}

func bindGlobalsForCall(exec *Execution, root *Env, rebinder *callFunctionRebinder, globals map[string]Value) error {
	if err := exec.checkContext(); err != nil {
		return err
	}

	if exec.strictEffects {
		if err := validateStrictGlobals(globals); err != nil {
			return err
		}
	}

	for name, val := range globals {
		root.Define(name, rebinder.rebindValue(val))
	}

	return nil
}

func bindLazyTaskGlobalsForCall(exec *Execution, root *Env, globals *taskLazyGlobals, rebinder *callFunctionRebinder) error {
	if err := exec.checkContext(); err != nil {
		return err
	}

	if globals == nil || len(globals.values) == 0 {
		return nil
	}
	if exec.strictEffects {
		if err := globals.ensureStrictValidated(); err != nil {
			return err
		}
	}
	globals.root = root
	globals.rebinder = rebinder
	for name, val := range globals.values {
		if val.Kind() == KindEnum {
			root.Define(name, rebinder.rebindValue(val))
			continue
		}
		root.defineLazy(name, taskLazyGlobalBinding{globals: globals, name: name})
	}
	return nil
}

func executeFunctionForCall(exec *Execution, fn *ScriptFunction, callEnv *Env) (Value, error) {
	if err := exec.pushFrame(fn.Name, fn.Pos, fn.owner, fn.owner); err != nil {
		return NewNil(), err
	}
	val, returned, err := exec.evalStatements(fn.Body, callEnv)
	if err != nil {
		err = exec.wrapError(err, fn.Pos)
	}
	exec.popFrame()
	if err != nil {
		return NewNil(), err
	}
	val = callEnv.detachArrayAppendResult(val)
	if err := exec.checkContext(); err != nil {
		return NewNil(), err
	}
	if fn.ReturnTy != nil {
		normalized, err := normalizeValueForType(val, fn.ReturnTy, typeContext{
			owner:    fn.owner,
			env:      fn.Env,
			fallback: exec.root,
			exec:     exec,
		})
		if err != nil {
			if isHostControlSignal(err) {
				return NewNil(), err
			}
			if isNormalizationLimitError(err) {
				return NewNil(), exec.wrapError(err, fn.Pos)
			}
			return NewNil(), exec.errorAt(fn.Pos, "%s", formatReturnTypeMismatch(fn.Name, err))
		}
		val = normalized
	}
	if err := exec.checkMemoryWith(val); err != nil {
		return NewNil(), exec.wrapError(err, fn.Pos)
	}
	if returned {
		return val, nil
	}
	return val, nil
}

// validateCallShape checks that args and kwargs can satisfy fn's parameters
// before any default is evaluated. It reproduces the positional and keyword
// consumption of the binding loop so it reports the same missing-argument,
// leftover-positional, and unexpected-keyword errors, but it touches no default
// expression. Surfacing these mismatches first keeps a defaulted parameter's
// side effects, errors, or step-quota cost from masking a call that can never
// bind, such as f(1) against def f(a: expensive()) or an omitted required
// keyword that follows a defaulted one.
func (exec *Execution) validateCallShape(fn *ScriptFunction, args []Value, kwargs map[string]Value, pos Position) error {
	var usedKw map[string]bool
	if len(kwargs) > 0 {
		usedKw = make(map[string]bool, len(kwargs))
	}
	argIdx := 0

	for _, param := range fn.Params {
		switch param.Kind {
		case ParamKeyword:
			if _, ok := kwargs[param.Name]; ok {
				if usedKw != nil {
					usedKw[param.Name] = true
				}
			} else if param.DefaultVal == nil {
				return exec.argumentErrorAt(pos, "missing keyword argument %s", param.Name)
			}
		case ParamRest:
			argIdx = len(args)
		case ParamKeywordRest:
			for name := range kwargs {
				if usedKw != nil {
					usedKw[name] = true
				}
			}
		case ParamBlock:
			// A block parameter binds from the call environment, never from the
			// positional or keyword arguments, so it imposes no shape constraint.
		case ParamNormal:
			if argIdx < len(args) {
				argIdx++
			} else if _, ok := kwargs[param.Name]; ok {
				if usedKw != nil {
					usedKw[param.Name] = true
				}
			} else if param.DefaultVal == nil {
				return exec.argumentErrorAt(pos, "missing argument %s", param.Name)
			}
		default:
			return exec.errorAt(pos, "unknown parameter kind for %s", param.Name)
		}
	}

	if argIdx < len(args) {
		return exec.argumentErrorAt(pos, "unexpected positional arguments")
	}
	if usedKw != nil {
		for name := range kwargs {
			if !usedKw[name] {
				return exec.argumentErrorAt(pos, "unexpected keyword argument %s", name)
			}
		}
	}
	return nil
}

func (exec *Execution) bindFunctionArgs(fn *ScriptFunction, env *Env, args []Value, kwargs map[string]Value, pos Position) error {
	// Validate the whole call shape before binding so that no parameter default
	// is evaluated when the call can never bind successfully. A default may have
	// side effects, raise an error, or consume the step quota, and evaluating it
	// would mask the real arity or keyword mismatch. validateCallShape mirrors
	// the binding loop's positional/keyword bookkeeping without evaluating any
	// default expression.
	if err := exec.validateCallShape(fn, args, kwargs, pos); err != nil {
		return err
	}

	var usedKw map[string]bool
	if len(kwargs) > 0 {
		usedKw = make(map[string]bool, len(kwargs))
	}
	argIdx := 0

	for _, param := range fn.Params {
		var val Value
		switch param.Kind {
		case ParamKeyword:
			if kw, ok := kwargs[param.Name]; ok {
				val = kw
				if usedKw != nil {
					usedKw[param.Name] = true
				}
			} else if param.DefaultVal != nil {
				defaultVal, err := exec.evalExpressionWithAuto(param.DefaultVal, env, true)
				if err != nil {
					return err
				}
				val = defaultVal
			} else {
				return exec.argumentErrorAt(pos, "missing keyword argument %s", param.Name)
			}
		case ParamRest:
			rest := append([]Value(nil), args[argIdx:]...)
			val = NewArray(rest)
			argIdx = len(args)
		case ParamKeywordRest:
			rest := make(map[string]Value)
			for name, kw := range kwargs {
				if usedKw != nil && usedKw[name] {
					continue
				}
				rest[name] = kw
				if usedKw != nil {
					usedKw[name] = true
				}
			}
			val = NewHash(rest)
		case ParamBlock:
			if block, ok := env.lookupCallBlock(); ok {
				val = block
			} else {
				val = NewNil()
			}
		case ParamNormal:
			if argIdx < len(args) {
				val = args[argIdx]
				argIdx++
			} else if kw, ok := kwargs[param.Name]; ok {
				val = kw
				if usedKw != nil {
					usedKw[param.Name] = true
				}
			} else if param.DefaultVal != nil {
				defaultVal, err := exec.evalExpressionWithAuto(param.DefaultVal, env, true)
				if err != nil {
					return err
				}
				val = defaultVal
			} else {
				return exec.argumentErrorAt(pos, "missing argument %s", param.Name)
			}
		default:
			return exec.errorAt(pos, "unknown parameter kind for %s", param.Name)
		}

		if param.Type != nil {
			normalized, err := normalizeValueForType(val, param.Type, typeContext{
				owner:    fn.owner,
				env:      fn.Env,
				fallback: exec.root,
				exec:     exec,
			})
			if err != nil {
				if isHostControlSignal(err) {
					return err
				}
				if isNormalizationLimitError(err) {
					return exec.wrapError(err, pos)
				}
				return exec.errorAt(pos, "%s", formatArgumentTypeMismatch(param.Name, err))
			}
			val = normalized
		}
		env.Define(param.Name, val)
		if param.IsIvar {
			if selfVal, ok := env.Get("self"); ok && selfVal.Kind() == KindInstance {
				inst := valueInstance(selfVal)
				if inst != nil {
					inst.Ivars[param.Name] = val
				}
			}
		}
	}

	if argIdx < len(args) {
		return exec.errorAt(pos, "unexpected positional arguments")
	}
	if usedKw != nil {
		for name := range kwargs {
			if !usedKw[name] {
				return exec.errorAt(pos, "unexpected keyword argument %s", name)
			}
		}
	}
	return nil
}
