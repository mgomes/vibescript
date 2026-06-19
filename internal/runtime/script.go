package runtime

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
)

func (s *Script) Call(ctx context.Context, name string, args []Value, opts CallOptions) (Value, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	_, ok := s.functions[name]
	if !ok {
		candidates := slices.Collect(maps.Keys(s.functions))
		return NewNil(), fmt.Errorf("function %s not found%s", name, didYouMean(name, candidates))
	}

	rootCapacity := len(s.classes) + len(opts.Globals) + len(opts.Capabilities)*2
	root := newEnvWithCapacity(nil, rootCapacity)
	s.engine.attachBuiltins(root, len(s.functions)+len(s.enums))

	callFunctions := cloneFunctionsForCall(s.functions, root)
	fn, ok := callFunctions[name]
	if !ok {
		return NewNil(), fmt.Errorf("function %s not found", name)
	}
	for n, fnDecl := range callFunctions {
		// Static: function clones are immutable per call, so they are
		// accounted once instead of on every quota check. Reassigning
		// the name from script code demotes the binding to dynamic.
		root.DefineStatic(n, NewFunction(fnDecl))
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

	exec := newExecutionForCall(s, ctx, root, opts)

	if err := bindCapabilitiesForCall(exec, root, rebinder, opts.Capabilities); err != nil {
		return NewNil(), err
	}

	if err := bindGlobalsForCall(exec, root, rebinder, opts.Globals); err != nil {
		return NewNil(), err
	}

	if err := exec.checkMemory(); err != nil {
		return NewNil(), exec.wrapError(err, fn.Pos)
	}

	if err := initializeClassBodiesForCall(exec, root, callClasses, s.classOrder, deferredClassBodiesForFunction(fn, s.deferredClassBodies)); err != nil {
		return NewNil(), err
	}

	callEnv, err := prepareCallEnvForFunction(exec, root, rebinder, fn, args, opts.Keywords)
	if err != nil {
		return NewNil(), exec.wrapError(err, fn.Pos)
	}

	val, err := executeFunctionForCall(exec, fn, callEnv)
	if err != nil {
		return NewNil(), err
	}
	if valueNeedsHostClone(val) {
		return cloneValueForHost(val), nil
	}
	return val, nil
}

// callWithLazyTaskGlobals keeps task-only lazy global binding off the public Call hot path.
func (s *Script) callWithLazyTaskGlobals(ctx context.Context, name string, args []Value, opts CallOptions, lazyTaskGlobals *taskLazyGlobals) (Value, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if lazyTaskGlobals != nil {
		ctx = contextWithTaskLazyGlobals(ctx, lazyTaskGlobals)
	}

	_, ok := s.functions[name]
	if !ok {
		candidates := slices.Collect(maps.Keys(s.functions))
		return NewNil(), fmt.Errorf("function %s not found%s", name, didYouMean(name, candidates))
	}

	rootCapacity := len(s.classes) + len(opts.Globals) + len(opts.Capabilities)*2
	if lazyTaskGlobals != nil {
		rootCapacity += lazyTaskGlobals.len()
	}
	root := newEnvWithCapacity(nil, rootCapacity)
	s.engine.attachBuiltins(root, len(s.functions)+len(s.enums))

	callFunctions := cloneFunctionsForCall(s.functions, root)
	fn, ok := callFunctions[name]
	if !ok {
		return NewNil(), fmt.Errorf("function %s not found", name)
	}
	for n, fnDecl := range callFunctions {
		root.DefineStatic(n, NewFunction(fnDecl))
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

	exec := newExecutionForCall(s, ctx, root, opts)

	if err := bindCapabilitiesForCall(exec, root, rebinder, opts.Capabilities); err != nil {
		return NewNil(), err
	}

	if err := bindGlobalsForCall(exec, root, rebinder, opts.Globals); err != nil {
		return NewNil(), err
	}
	if lazyTaskGlobals != nil {
		if err := bindLazyTaskGlobalsForCall(exec, root, lazyTaskGlobals, rebinder); err != nil {
			return NewNil(), err
		}
	}

	if err := exec.checkMemory(); err != nil {
		return NewNil(), exec.wrapError(err, fn.Pos)
	}

	if err := initializeClassBodiesForCall(exec, root, callClasses, s.classOrder, deferredClassBodiesForFunction(fn, s.deferredClassBodies)); err != nil {
		return NewNil(), err
	}

	callEnv, err := prepareCallEnvForFunction(exec, root, rebinder, fn, args, opts.Keywords)
	if err != nil {
		return NewNil(), exec.wrapError(err, fn.Pos)
	}

	val, err := executeFunctionForCall(exec, fn, callEnv)
	if err != nil {
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
	return cloneFunctionForSnapshot(fn), true
}

// Functions returns compiled functions in deterministic name order.
func (s *Script) Functions() []*ScriptFunction {
	names := make([]string, 0, len(s.functions))
	for name := range s.functions {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*ScriptFunction, 0, len(names))
	for _, name := range names {
		out = append(out, cloneFunctionForSnapshot(s.functions[name]))
	}
	return out
}

// Classes returns compiled classes in deterministic name order.
func (s *Script) Classes() []*ClassDef {
	names := make([]string, 0, len(s.classes))
	for name := range s.classes {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*ClassDef, 0, len(names))
	for _, name := range names {
		out = append(out, cloneClassForSnapshot(s.classes[name]))
	}
	return out
}

// Enums returns compiled enums in deterministic name order.
func (s *Script) Enums() []*EnumDef {
	names := make([]string, 0, len(s.enums))
	for name := range s.enums {
		names = append(names, name)
	}
	sort.Strings(names)
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

func cloneClassesForCall(classes map[string]*ClassDef, env *Env) map[string]*ClassDef {
	if len(classes) == 0 {
		return nil
	}
	cloned := make(map[string]*ClassDef, len(classes))
	for name, classDef := range classes {
		classClone := &ClassDef{
			Name:         classDef.Name,
			Methods:      make(map[string]*ScriptFunction, len(classDef.Methods)),
			ClassMethods: make(map[string]*ScriptFunction, len(classDef.ClassMethods)),
			ClassVars:    make(map[string]Value),
			Body:         classDef.Body,
			owner:        classDef.owner,
		}
		for methodName, method := range classDef.Methods {
			classClone.Methods[methodName] = cloneFunctionForEnv(method, env)
		}
		for methodName, method := range classDef.ClassMethods {
			classClone.ClassMethods[methodName] = cloneFunctionForEnv(method, env)
		}
		cloned[name] = classClone
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

func cloneFunctionForSnapshot(fn *ScriptFunction) *ScriptFunction {
	if fn == nil {
		return nil
	}
	clone := *fn
	clone.Params = cloneParams(fn.Params)
	clone.ReturnTy = cloneTypeExpr(fn.ReturnTy)
	clone.Body = cloneStatements(fn.Body)
	clone.Env = nil
	return &clone
}

func cloneClassForSnapshot(classDef *ClassDef) *ClassDef {
	if classDef == nil {
		return nil
	}
	classClone := &ClassDef{
		Name:         classDef.Name,
		Methods:      make(map[string]*ScriptFunction, len(classDef.Methods)),
		ClassMethods: make(map[string]*ScriptFunction, len(classDef.ClassMethods)),
		ClassVars:    cloneBuiltinMap(classDef.ClassVars),
		Body:         cloneStatements(classDef.Body),
	}
	for methodName, method := range classDef.Methods {
		classClone.Methods[methodName] = cloneFunctionForSnapshot(method)
	}
	for methodName, method := range classDef.ClassMethods {
		classClone.ClassMethods[methodName] = cloneFunctionForSnapshot(method)
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
