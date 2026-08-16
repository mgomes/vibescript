package runtime

import (
	"context"
	"fmt"
	"slices"

	"github.com/mgomes/vibescript/internal/ast"
)

func (s *Script) Call(ctx context.Context, name string, args []Value, opts CallOptions) (Value, error) {
	// Composite globals bind lazily, and the lazy binding stores the per-call
	// rebinder, which would force it onto the heap for every call if the
	// store were reachable from this function. Route those calls through the
	// variant that already pays that cost, keeping the common no-globals /
	// scalar-globals path allocation-free at this layer.
	if globalsBindLazily(opts.Globals) {
		return s.callWithLazyGlobals(ctx, name, args, opts)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return NewNil(), err
	}

	_, ok := s.functions[name]
	if !ok {
		candidates := functionSuggestionCandidates(s.functions)
		return NewNil(), fmt.Errorf("function %s not found%s", name, didYouMean(name, candidates))
	}

	rootCapacity := len(s.classes) + len(opts.Globals) + len(opts.Capabilities)*2
	root := newEnvWithCapacity(nil, rootCapacity)
	s.engine.attachBuiltins(root, len(s.functions)+len(s.enums))

	bindFunctionsForCall(s.functions, root)
	fn, ok := materializeCallFunction(root, name)
	if !ok {
		return NewNil(), fmt.Errorf("function %s not found", name)
	}

	callClasses := cloneClassesForCall(s.classes, root)
	for n, classDef := range callClasses {
		root.Define(n, NewClass(classDef))
	}
	callEnums := cloneEnumsForCall(s.enums)
	for n, enumDef := range callEnums {
		root.DefineStatic(n, NewEnum(enumDef))
	}
	rebinder := newCallFunctionRebinder(s, root, callClasses, callEnums)
	rebinder.inboundDataFast = scanInboundCallValues(args, opts.Keywords)

	exec := newExecutionForCall(s, ctx, root, opts)
	defer exec.releaseBaseWalkCache()
	defer exec.releaseMemoryChain()

	// Taken before anything binds, and in particular before capabilities do.
	// A binder is allowed to block -- the sleeping binder in this repository's
	// own tests does -- and until the baseline exists this level contributes
	// nothing to the chain, so its fresh root and cloned definitions were
	// invisible to an ancestor for as long as a binder chose to wait. Nothing
	// bound between here and the old position changes what this measures: the
	// baseline is the graph tail, which is the modules and task globals the call
	// arrived with, and binding writes to the root env rather than to those.
	exec.captureMemoryInheritedBaseline()

	// Publish this level's own setup before anything else runs.
	//
	// The property is about *when this level becomes visible*, not about which
	// callers can block. A child builds its root env and clones the call's
	// classes and enums before its execution exists, so there is no node to
	// publish to while that happens; the first publication after the node exists
	// is the earliest an ancestor can see any of it. Until then a nonblocking
	// parent allocates against a total that omits this whole level.
	//
	// It was previously gated on there being capabilities to bind, reasoning
	// that a binder is the only thing on this path that can block. That was
	// right about blocking and wrong about the property: an ordinary
	// capability-free job has the same window, because the window is opened by
	// allocating before registration rather than by waiting. Blocking only makes
	// it longer.
	//
	// A residual remains and is bounded rather than closed. Everything built
	// before the execution exists -- the root env, the cloned classes and enums
	// -- cannot be published, because there is no node yet to publish to, so a
	// nonblocking parent can be admitted against a total omitting it. That
	// quantity is fixed by the script's own text: measured flat at 19,904 bytes
	// across task payloads from 1 KiB to 4 MiB, a four-thousand-fold range, and
	// growing only with definitions, at roughly 745 bytes per class -- 1,040
	// bytes for a script with none and 298,048 for one with four hundred.
	//
	// So the exposure is a property of the program, not of anything the running
	// script controls, which is what makes it a residual worth documenting
	// rather than a hole worth a reservation taken before an Execution exists.
	// The invariant holding that bound static is that nothing on this path
	// retains argument data: scanInboundCallValues walks the arguments but is a
	// predicate returning a bool, and the deep copy it enables happens later,
	// after registration. A change that made this path retain anything derived
	// from the arguments would make the bound scale with runtime data, and the
	// measurement above is the one to repeat.
	if err := exec.checkMemory(); err != nil {
		return NewNil(), exec.wrapError(err, fn.Pos)
	}

	if err := bindCapabilitiesForCall(exec, root, rebinder, opts.Capabilities); err != nil {
		return NewNil(), err
	}

	if err := bindGlobalsForCall(exec, root, rebinder, opts.Globals); err != nil {
		return NewNil(), err
	}

	if err := exec.checkContext(); err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemory(); err != nil {
		return NewNil(), exec.wrapError(err, fn.Pos)
	}

	if err := initializeClassBodiesForCall(exec, root, callClasses, s.classOrder, deferredClassBodiesForFunction(fn, s.deferredClassBodies)); err != nil {
		return NewNil(), err
	}
	if err := exec.checkContext(); err != nil {
		return NewNil(), err
	}

	// The invocation token opens before argument binding so a block built by a
	// default-argument expression homes to this entry invocation.
	token := exec.pushReturnToken()
	callEnv, err := prepareCallEnvForFunction(exec, root, rebinder, fn, args, opts.Keywords)
	if err != nil {
		exec.popReturnToken()
		if sig := matchNonLocalReturn(err, token); sig != nil {
			// A default-argument block returned during binding: that is the
			// entry function's return value, validated like any other.
			val, finishErr := finishFunctionForCall(exec, fn, sig.value)
			if finishErr != nil {
				return NewNil(), finishErr
			}
			if valueNeedsHostClone(val) {
				return cloneValueForHost(val), nil
			}
			return val, nil
		}
		return NewNil(), exec.wrapError(err, fn.Pos)
	}

	val, err := executeFunctionForCall(exec, fn, callEnv, token)
	exec.popReturnToken()
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkContext(); err != nil {
		return NewNil(), err
	}
	if valueNeedsHostClone(val) {
		return cloneValueForHost(val), nil
	}
	return val, nil
}

// callWithLazyGlobals keeps lazily bound composite host globals off the public
// Call hot path, so the per-call rebinder stays stack-allocated for calls that
// bind no deferred globals.
func (s *Script) callWithLazyGlobals(ctx context.Context, name string, args []Value, opts CallOptions) (Value, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return NewNil(), err
	}

	_, ok := s.functions[name]
	if !ok {
		candidates := functionSuggestionCandidates(s.functions)
		return NewNil(), fmt.Errorf("function %s not found%s", name, didYouMean(name, candidates))
	}

	rootCapacity := len(s.classes) + len(opts.Globals) + len(opts.Capabilities)*2
	root := newEnvWithCapacity(nil, rootCapacity)
	s.engine.attachBuiltins(root, len(s.functions)+len(s.enums))

	bindFunctionsForCall(s.functions, root)
	fn, ok := materializeCallFunction(root, name)
	if !ok {
		return NewNil(), fmt.Errorf("function %s not found", name)
	}

	callClasses := cloneClassesForCall(s.classes, root)
	for n, classDef := range callClasses {
		root.Define(n, NewClass(classDef))
	}
	callEnums := cloneEnumsForCall(s.enums)
	for n, enumDef := range callEnums {
		root.DefineStatic(n, NewEnum(enumDef))
	}
	rebinder := newCallFunctionRebinder(s, root, callClasses, callEnums)
	rebinder.inboundDataFast = scanInboundCallValues(args, opts.Keywords)

	exec := newExecutionForCall(s, ctx, root, opts)
	defer exec.releaseBaseWalkCache()
	defer exec.releaseMemoryChain()

	// Taken before anything binds, and in particular before capabilities do.
	// A binder is allowed to block -- the sleeping binder in this repository's
	// own tests does -- and until the baseline exists this level contributes
	// nothing to the chain, so its fresh root and cloned definitions were
	// invisible to an ancestor for as long as a binder chose to wait. Nothing
	// bound between here and the old position changes what this measures: the
	// baseline is the graph tail, which is the modules and task globals the call
	// arrived with, and binding writes to the root env rather than to those.
	exec.captureMemoryInheritedBaseline()

	// Publish this level's own setup before anything else runs.
	//
	// The property is about *when this level becomes visible*, not about which
	// callers can block. A child builds its root env and clones the call's
	// classes and enums before its execution exists, so there is no node to
	// publish to while that happens; the first publication after the node exists
	// is the earliest an ancestor can see any of it. Until then a nonblocking
	// parent allocates against a total that omits this whole level.
	//
	// It was previously gated on there being capabilities to bind, reasoning
	// that a binder is the only thing on this path that can block. That was
	// right about blocking and wrong about the property: an ordinary
	// capability-free job has the same window, because the window is opened by
	// allocating before registration rather than by waiting. Blocking only makes
	// it longer.
	//
	// A residual remains and is bounded rather than closed. Everything built
	// before the execution exists -- the root env, the cloned classes and enums
	// -- cannot be published, because there is no node yet to publish to, so a
	// nonblocking parent can be admitted against a total omitting it. That
	// quantity is fixed by the script's own text: measured flat at 19,904 bytes
	// across task payloads from 1 KiB to 4 MiB, a four-thousand-fold range, and
	// growing only with definitions, at roughly 745 bytes per class -- 1,040
	// bytes for a script with none and 298,048 for one with four hundred.
	//
	// So the exposure is a property of the program, not of anything the running
	// script controls, which is what makes it a residual worth documenting
	// rather than a hole worth a reservation taken before an Execution exists.
	// The invariant holding that bound static is that nothing on this path
	// retains argument data: scanInboundCallValues walks the arguments but is a
	// predicate returning a bool, and the deep copy it enables happens later,
	// after registration. A change that made this path retain anything derived
	// from the arguments would make the bound scale with runtime data, and the
	// measurement above is the one to repeat.
	if err := exec.checkMemory(); err != nil {
		return NewNil(), exec.wrapError(err, fn.Pos)
	}

	if err := bindCapabilitiesForCall(exec, root, rebinder, opts.Capabilities); err != nil {
		return NewNil(), err
	}

	if err := bindGlobalsForCallLazy(exec, root, rebinder, opts.Globals); err != nil {
		return NewNil(), err
	}

	if err := exec.checkContext(); err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemory(); err != nil {
		return NewNil(), exec.wrapError(err, fn.Pos)
	}

	if err := initializeClassBodiesForCall(exec, root, callClasses, s.classOrder, deferredClassBodiesForFunction(fn, s.deferredClassBodies)); err != nil {
		return NewNil(), err
	}
	if err := exec.checkContext(); err != nil {
		return NewNil(), err
	}

	// The invocation token opens before argument binding so a block built by a
	// default-argument expression homes to this entry invocation.
	token := exec.pushReturnToken()
	callEnv, err := prepareCallEnvForFunction(exec, root, rebinder, fn, args, opts.Keywords)
	if err != nil {
		exec.popReturnToken()
		if sig := matchNonLocalReturn(err, token); sig != nil {
			// A default-argument block returned during binding: that is the
			// entry function's return value, validated like any other.
			val, finishErr := finishFunctionForCall(exec, fn, sig.value)
			if finishErr != nil {
				return NewNil(), finishErr
			}
			if valueNeedsHostClone(val) {
				return cloneValueForHost(val), nil
			}
			return val, nil
		}
		return NewNil(), exec.wrapError(err, fn.Pos)
	}

	val, err := executeFunctionForCall(exec, fn, callEnv, token)
	exec.popReturnToken()
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkContext(); err != nil {
		return NewNil(), err
	}
	if valueNeedsHostClone(val) {
		return cloneValueForHost(val), nil
	}
	return val, nil
}

func deferredClassBodiesForFunction(fn *ScriptFunction, deferred map[string]struct{}) map[string]struct{} {
	if len(deferred) == 0 || fn == nil {
		return nil
	}
	for _, stmt := range fn.Body {
		classStmt, ok := stmt.(*ClassStmt)
		if !ok {
			continue
		}
		if _, ok := deferred[classStmt.Name]; ok {
			return deferred
		}
	}
	return nil
}

// Function looks up a compiled function by name.
func (s *Script) Function(name string) (*ScriptFunction, bool) {
	fn, ok := s.functions[name]
	if !ok {
		return nil, false
	}
	return cloneFunctionForSnapshot(fn, nil), true
}

// Functions returns compiled functions in deterministic name order.
func (s *Script) Functions() []*ScriptFunction {
	names := make([]string, 0, len(s.functions))
	for name := range s.functions {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]*ScriptFunction, 0, len(names))
	for _, name := range names {
		out = append(out, cloneFunctionForSnapshot(s.functions[name], nil))
	}
	return out
}

// Classes returns compiled classes in deterministic name order.
func (s *Script) Classes() []*ClassDef {
	names := make([]string, 0, len(s.classes))
	for name := range s.classes {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]*ClassDef, 0, len(names))
	// One memo for the whole snapshot, not one per class. A module's methods are
	// copied into every including class by shallow copy, and re-resolving the
	// contract against the including class lands on the module's own node again,
	// so all of them share one contract. A per-class memo would copy a wide
	// module property once per include and put the O(classes * type size) blowup
	// back, just spelled with `include` instead of with methods (#16).
	propertyTypes := ast.NewTypeExprMemo()
	for _, name := range names {
		out = append(out, cloneClassForSnapshot(s.classes[name], propertyTypes))
	}
	return out
}

// Enums returns compiled enums in deterministic name order.
func (s *Script) Enums() []*EnumDef {
	names := make([]string, 0, len(s.enums))
	for name := range s.enums {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]*EnumDef, 0, len(names))
	for _, name := range names {
		out = append(out, cloneEnumForSnapshot(s.enums[name]))
	}
	return out
}

func (s *Script) bindFunctionOwnership() {
	for _, fn := range s.functions {
		fn.owner = s
	}
	for _, classDef := range s.classes {
		classDef.owner = s
		for _, fn := range classDef.Methods {
			fn.owner = s
		}
		for _, fn := range classDef.ClassMethods {
			fn.owner = s
		}
	}
	for _, enumDef := range s.enums {
		enumDef.owner = s
	}
}

func cloneFunctionsForCall(functions map[string]*ScriptFunction, env *Env) map[string]*ScriptFunction {
	cloned := make(map[string]*ScriptFunction, len(functions))
	for name, fn := range functions {
		cloned[name] = cloneFunctionForEnv(fn, env)
	}
	return cloned
}

type callFunctionBinding struct {
	fn  *ScriptFunction
	env *Env
}

func (binding callFunctionBinding) materialize() Value {
	return NewFunction(cloneFunctionForEnv(binding.fn, binding.env))
}

func bindFunctionsForCall(functions map[string]*ScriptFunction, root *Env) {
	if len(functions) == 1 {
		for name, fn := range functions {
			root.DefineStatic(name, NewFunction(cloneFunctionForEnv(fn, root)))
		}
		return
	}
	for name, fn := range functions {
		// Static: function clones are immutable per call, so they are accounted
		// once instead of on every quota check. Reassigning the name from script
		// code demotes the binding to dynamic.
		root.DefineStatic(name, newLazyValue(callFunctionBinding{fn: fn, env: root}))
	}
}

func materializeCallFunction(root *Env, name string) (*ScriptFunction, bool) {
	val, ok := root.Get(name)
	if !ok {
		return nil, false
	}
	fn := valueFunction(val)
	return fn, fn != nil
}

func cloneClassesForCall(classes map[string]*ClassDef, env *Env) map[string]*ClassDef {
	if len(classes) == 0 {
		return nil
	}
	cloned := make(map[string]*ClassDef, len(classes))
	for name, classDef := range classes {
		classClone := &ClassDef{
			Name:            classDef.Name,
			IsModule:        classDef.IsModule,
			Methods:         make(map[string]*ScriptFunction, len(classDef.Methods)),
			ClassMethods:    make(map[string]*ScriptFunction, len(classDef.ClassMethods)),
			ClassVars:       make(map[string]Value),
			NestedModules:   classDef.NestedModules,
			IncludedModules: classDef.IncludedModules,
			Body:            classDef.Body,
			owner:           classDef.owner,
		}
		for methodName, method := range classDef.Methods {
			classClone.Methods[methodName] = cloneFunctionForEnv(method, env)
		}
		for methodName, method := range classDef.ClassMethods {
			classClone.ClassMethods[methodName] = cloneFunctionForEnv(method, env)
		}
		cloned[name] = classClone
	}
	// Link nested module declarations into their parent's constants so
	// Outer::Inner resolves through the scoped-constant path. Linking runs
	// after the clone loop so parent and nested definitions reference this
	// call's clones regardless of map iteration order.
	for _, classClone := range cloned {
		for _, short := range classClone.NestedModules {
			if nested, ok := cloned[classClone.Name+"::"+short]; ok {
				classClone.ClassVars[short] = NewClass(nested)
			}
		}
	}
	return cloned
}

func cloneEnumsForCall(enums map[string]*EnumDef) map[string]*EnumDef {
	if len(enums) == 0 {
		return nil
	}
	cloned := make(map[string]*EnumDef, len(enums))
	for name, enumDef := range enums {
		cloned[name] = cloneEnumDef(enumDef, enumDef.owner)
	}
	return cloned
}

// cloneFunctionForSnapshot detaches a function for a caller that asked the
// script to describe itself. propertyTypes carries the property contracts the
// surrounding snapshot has already copied so a class's methods share one copy
// per property rather than one per parameter; it may be nil for a lone
// function, which has nothing to share with.
func cloneFunctionForSnapshot(fn *ScriptFunction, propertyTypes ast.TypeExprMemo) *ScriptFunction {
	if fn == nil {
		return nil
	}
	clone := *fn
	clone.Params = ast.CloneParamsWithTypeMemo(fn.Params, propertyTypes)
	clone.ReturnTy = ast.CloneTypeExprWithMemo(fn.ReturnTy, propertyTypes)
	clone.Body = cloneStatements(fn.Body)
	clone.Env = nil
	return &clone
}

// cloneClassForSnapshot detaches a class for a caller that asked the script to
// describe itself. propertyTypes spans the whole snapshot so contracts shared
// between classes — which is what a mixed-in property is — stay one copy.
func cloneClassForSnapshot(classDef *ClassDef, propertyTypes ast.TypeExprMemo) *ClassDef {
	if classDef == nil {
		return nil
	}
	classClone := &ClassDef{
		Name:            classDef.Name,
		IsModule:        classDef.IsModule,
		Methods:         make(map[string]*ScriptFunction, len(classDef.Methods)),
		ClassMethods:    make(map[string]*ScriptFunction, len(classDef.ClassMethods)),
		ClassVars:       cloneBuiltinMap(classDef.ClassVars),
		NestedModules:   classDef.NestedModules,
		IncludedModules: classDef.IncludedModules,
		Body:            cloneStatements(classDef.Body),
	}
	for methodName, method := range classDef.Methods {
		classClone.Methods[methodName] = cloneFunctionForSnapshot(method, propertyTypes)
	}
	for methodName, method := range classDef.ClassMethods {
		classClone.ClassMethods[methodName] = cloneFunctionForSnapshot(method, propertyTypes)
	}
	return classClone
}

func cloneEnumForSnapshot(enumDef *EnumDef) *EnumDef {
	return cloneEnumDef(enumDef, nil)
}

func cloneEnumDef(enumDef *EnumDef, owner *Script) *EnumDef {
	if enumDef == nil {
		return nil
	}
	clone := &EnumDef{
		Name:         enumDef.Name,
		Members:      make(map[string]*EnumValueDef, len(enumDef.Members)),
		MembersByKey: make(map[string]*EnumValueDef, len(enumDef.MembersByKey)),
		Order:        append([]string(nil), enumDef.Order...),
		owner:        owner,
	}
	for memberName, member := range enumDef.Members {
		if member == nil {
			continue
		}
		memberClone := &EnumValueDef{
			Enum:   clone,
			Name:   member.Name,
			Symbol: member.Symbol,
			Index:  member.Index,
		}
		clone.Members[memberName] = memberClone
		clone.MembersByKey[member.Symbol] = memberClone
	}
	return clone
}

func functionSuggestionCandidates(functions map[string]*ScriptFunction) []string {
	candidates := make([]string, 0, min(len(functions), suggestMaxCandidates))
	for name := range functions {
		if len(candidates) == suggestMaxCandidates {
			break
		}
		candidates = append(candidates, name)
	}
	return candidates
}
