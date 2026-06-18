package runtime

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

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

func (exec *Execution) invokeCallable(callee, receiver Value, args []Value, kwargs map[string]Value, block Value, pos Position) (Value, error) {
	switch callee.Kind() {
	case KindFunction:
		result, err := exec.callFunction(valueFunction(callee), receiver, args, kwargs, block, pos)
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				return NewNil(), exec.errorAt(pos, "break cannot cross call boundary")
			}
			if errors.Is(err, errLoopNext) {
				return NewNil(), exec.errorAt(pos, "next cannot cross call boundary")
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
				return NewNil(), exec.errorAt(pos, "break cannot cross call boundary")
			}
			if errors.Is(err, errLoopNext) {
				return NewNil(), exec.errorAt(pos, "next cannot cross call boundary")
			}
			return NewNil(), exec.wrapError(err, pos)
		}
		if hasContract && contract.ValidateReturn != nil {
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
	callEnv := newEnvWithCapacity(fn.Env, len(fn.Params)+2)
	if receiver.Kind() != KindNil {
		callEnv.Define("self", receiver)
	}
	callEnv.Define("__block__", block)
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
	if fn.ReturnTy != nil {
		normalized, err := normalizeValueForType(val, fn.ReturnTy, typeContext{
			owner:    fn.owner,
			env:      fn.Env,
			fallback: exec.root,
		})
		if err != nil {
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
	seenMaps      map[uintptr]map[string]Value
}

func newCallFunctionRebinder(script *Script, root *Env, callClasses map[string]*ClassDef, callEnums map[string]*EnumDef) *callFunctionRebinder {
	return &callFunctionRebinder{
		script:        script,
		root:          root,
		callClasses:   callClasses,
		callEnums:     callEnums,
		seenFunctions: make(map[*ScriptFunction]*ScriptFunction),
		seenInstances: make(map[*Instance]Value),
		seenArrays:    make(map[sliceIdentity]Value),
		seenMaps:      make(map[uintptr]map[string]Value),
	}
}

func (r *callFunctionRebinder) rebindValue(val Value) Value {
	switch val.Kind() {
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
		r.seenFunctions[fn] = clone
		return NewFunction(clone)
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
		r.seenArrays[id] = clonedArray
		for i := range items {
			clonedItems[i] = r.rebindValue(items[i])
		}
		return clonedArray
	case KindHash:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if cloneMap, seen := r.seenMaps[ptr]; seen {
			return NewHash(cloneMap)
		}
		clonedEntries := make(map[string]Value, len(entries))
		r.seenMaps[ptr] = clonedEntries
		for key, item := range entries {
			clonedEntries[key] = r.rebindValue(item)
		}
		return NewHash(clonedEntries)
	case KindObject:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if cloneMap, seen := r.seenMaps[ptr]; seen {
			return NewObject(cloneMap)
		}
		clonedEntries := make(map[string]Value, len(entries))
		r.seenMaps[ptr] = clonedEntries
		for key, item := range entries {
			clonedEntries[key] = r.rebindValue(item)
		}
		return NewObject(clonedEntries)
	default:
		return val
	}
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
			return fmt.Errorf("bind capability: %w", err)
		}
		for name, val := range globals {
			rebound := rebinder.rebindValue(val)
			root.Define(name, rebound)
			if len(scope.contracts) > 0 {
				scope.roots = append(scope.roots, rebound)
			}
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

func initializeClassBodiesForCall(exec *Execution, root *Env, callClasses map[string]*ClassDef) error {
	for name, classDef := range callClasses {
		if len(classDef.Body) == 0 {
			continue
		}
		classVal, _ := root.Get(name)
		env := newEnv(root)
		env.Define("self", classVal)
		exec.pushReceiver(classVal)
		_, _, err := exec.evalStatements(classDef.Body, env)
		exec.popReceiver()
		if err != nil {
			return err
		}
	}

	return nil
}

func prepareCallEnvForFunction(exec *Execution, root *Env, rebinder *callFunctionRebinder, fn *ScriptFunction, args []Value, keywords map[string]Value) (*Env, error) {
	callEnv := newEnvWithCapacity(root, len(fn.Params))
	callArgs := rebinder.rebindValues(args)
	callKeywords := rebinder.rebindKeywords(keywords)
	if err := exec.bindFunctionArgs(fn, callEnv, callArgs, callKeywords, fn.Pos); err != nil {
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
		receiver, err := exec.evalExpression(member.Object, env)
		if err != nil {
			return NewNil(), NewNil(), err
		}
		if err := exec.checkMemoryWith(receiver); err != nil {
			return NewNil(), NewNil(), err
		}
		if directCallee, handled, err := exec.evalDirectMemberMethodCall(receiver, member.Property, member.Pos()); handled || err != nil {
			if err != nil {
				return NewNil(), NewNil(), err
			}
			return directCallee, receiver, nil
		}
		callee, err := exec.getMember(receiver, member.Property, member.Pos())
		if err != nil {
			return NewNil(), NewNil(), err
		}
		return callee, receiver, nil
	}

	callee, err := exec.evalExpressionWithAuto(call.Callee, env, false)
	if err != nil {
		return NewNil(), NewNil(), err
	}
	return callee, NewNil(), nil
}

func (exec *Execution) evalDirectMemberMethodCall(receiver Value, property string, pos Position) (Value, bool, error) {
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
		if fn.Private && !exec.isCurrentReceiver(receiver) {
			return NewNil(), true, exec.errorAt(pos, "private method %s", property)
		}
		return NewFunction(fn), true, nil
	case KindInstance:
		instance := valueInstance(receiver)
		fn, ok := instance.Class.Methods[property]
		if !ok {
			return NewNil(), false, nil
		}
		if fn.Private && !exec.isCurrentReceiver(receiver) {
			return NewNil(), true, exec.errorAt(pos, "private method %s", property)
		}
		return NewFunction(fn), true, nil
	default:
		return NewNil(), false, nil
	}
}

func (exec *Execution) evalCallArgs(call *CallExpr, env *Env) ([]Value, error) {
	args := make([]Value, len(call.Args))
	for i, arg := range call.Args {
		val, err := exec.evalExpressionWithAuto(arg, env, true)
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
	if len(call.KwArgs) == 0 {
		return nil, nil
	}
	kwargs := make(map[string]Value, len(call.KwArgs))
	for _, kw := range call.KwArgs {
		val, err := exec.evalExpressionWithAuto(kw.Value, env, true)
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
	if receiver.Kind() == KindNil && len(kwargs) == 0 && block.IsNil() {
		if len(args) == 0 {
			return nil
		}
		return exec.checkMemoryWith(args...)
	}
	return exec.checkMemoryWithCallRoots(receiver, args, kwargs, block)
}

func (exec *Execution) evalCallExpr(call *CallExpr, env *Env) (Value, error) {
	if member, ok := call.Callee.(*MemberExpr); ok {
		return exec.evalMemberCallExpr(call, member, env)
	}

	callee, receiver, err := exec.evalCallTarget(call, env)
	if err != nil {
		return NewNil(), err
	}
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
	if err := exec.checkCallMemoryRoots(receiver, args, kwargs, block); err != nil {
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

func (exec *Execution) evalMemberCallExpr(call *CallExpr, member *MemberExpr, env *Env) (Value, error) {
	receiver, err := exec.evalExpression(member.Object, env)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(receiver); err != nil {
		return NewNil(), err
	}

	if canCallBuiltinMemberDirect(receiver, member.Property) {
		return exec.evalDirectBuiltinMemberCallExpr(call, receiver, member.Property, env)
	}

	var callee Value
	if directCallee, handled, err := exec.evalDirectMemberMethodCall(receiver, member.Property, member.Pos()); handled || err != nil {
		if err != nil {
			return NewNil(), err
		}
		callee = directCallee
	} else {
		var err error
		callee, err = exec.getMember(receiver, member.Property, member.Pos())
		if err != nil {
			return NewNil(), err
		}
	}

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
	if err := exec.checkCallMemoryRoots(receiver, args, kwargs, block); err != nil {
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
	if err := exec.checkCallMemoryRoots(receiver, args, kwargs, block); err != nil {
		return NewNil(), err
	}

	result, err := callBuiltinMemberDirect(receiver, property, args, kwargs, block)
	if err != nil {
		if errors.Is(err, errLoopBreak) {
			return NewNil(), exec.errorAt(call.Pos(), "break cannot cross call boundary")
		}
		if errors.Is(err, errLoopNext) {
			return NewNil(), exec.errorAt(call.Pos(), "next cannot cross call boundary")
		}
		return NewNil(), exec.wrapError(err, call.Pos())
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

func callBuiltinMemberDirect(receiver Value, property string, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	switch receiver.Kind() {
	case KindDuration:
		return callDurationMemberDirect(receiver.Duration(), property, args, kwargs, block)
	case KindTime:
		return callTimeMemberDirect(receiver.Time(), property, args, kwargs, block)
	default:
		return NewNil(), fmt.Errorf("unsupported member access on %s", receiver.Kind())
	}
}

func bindGlobalsForCall(exec *Execution, root *Env, rebinder *callFunctionRebinder, globals map[string]Value) error {
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
	if fn.ReturnTy != nil {
		normalized, err := normalizeValueForType(val, fn.ReturnTy, typeContext{
			owner:    fn.owner,
			env:      fn.Env,
			fallback: exec.root,
		})
		if err != nil {
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

func (exec *Execution) bindFunctionArgs(fn *ScriptFunction, env *Env, args []Value, kwargs map[string]Value, pos Position) error {
	var usedKw map[string]bool
	if len(kwargs) > 0 {
		usedKw = make(map[string]bool, len(kwargs))
	}
	argIdx := 0

	for _, param := range fn.Params {
		var val Value
		switch param.Kind {
		case ParamKeyword:
			kw, ok := kwargs[param.Name]
			if !ok {
				return exec.errorAt(pos, "missing keyword argument %s", param.Name)
			}
			val = kw
			if usedKw != nil {
				usedKw[param.Name] = true
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
			block, ok := env.Get("__block__")
			if ok {
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
				return exec.errorAt(pos, "missing argument %s", param.Name)
			}
		default:
			return exec.errorAt(pos, "unknown parameter kind for %s", param.Name)
		}

		if param.Type != nil {
			normalized, err := normalizeValueForType(val, param.Type, typeContext{
				owner:    fn.owner,
				env:      fn.Env,
				fallback: exec.root,
			})
			if err != nil {
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
