package vibes

import (
	"context"
	"fmt"
)

func (s *Script) Call(ctx context.Context, name string, args []Value, opts CallOptions) (Value, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	_, ok := s.functions[name]
	if !ok {
		return NewNil(), fmt.Errorf("function %s not found", name)
	}

	rootCapacity := len(s.engine.builtins) + len(s.functions) + len(s.classes) + len(opts.Globals) + len(opts.Capabilities)*2
	root := newEnvWithCapacity(nil, rootCapacity)
	for n, builtin := range s.engine.builtins {
		root.Define(n, builtin)
	}

	callFunctions := cloneFunctionsForCall(s.functions, root)
	fn, ok := callFunctions[name]
	if !ok {
		return NewNil(), fmt.Errorf("function %s not found", name)
	}
	for n, fnDecl := range callFunctions {
		root.Define(n, NewFunction(fnDecl))
	}

	callClasses := cloneClassesForCall(s.classes, root)
	for n, classDef := range callClasses {
		root.Define(n, NewClass(classDef))
	}
	rebinder := newCallFunctionRebinder(s, root, callClasses)

	exec := newExecutionForCall(s, ctx, root, opts)

	if err := bindCapabilitiesForCall(exec, root, rebinder, opts.Capabilities); err != nil {
		return NewNil(), err
	}

	if err := bindGlobalsForCall(exec, root, rebinder, opts.Globals); err != nil {
		return NewNil(), err
	}

	if err := exec.checkMemory(); err != nil {
		return NewNil(), err
	}

	if err := initializeClassBodiesForCall(exec, root, callClasses); err != nil {
		return NewNil(), err
	}

	callEnv, err := prepareCallEnvForFunction(exec, root, rebinder, fn, args, opts.Keywords)
	if err != nil {
		return NewNil(), err
	}

	val, err := executeFunctionForCall(exec, fn, callEnv)
	if err != nil {
		return NewNil(), err
	}
	return val, nil
}
