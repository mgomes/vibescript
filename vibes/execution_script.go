package vibes

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func (e *Engine) Compile(source string) (*Script, error) {
	p := newParser(source)
	program, parseErrors := p.ParseProgram()
	if len(parseErrors) > 0 {
		return nil, combineErrors(parseErrors)
	}

	functions := make(map[string]*ScriptFunction)
	classes := make(map[string]*ClassDef)

	for _, stmt := range program.Statements {
		switch s := stmt.(type) {
		case *FunctionStmt:
			if _, exists := functions[s.Name]; exists {
				return nil, fmt.Errorf("duplicate function %s", s.Name)
			}
			functions[s.Name] = &ScriptFunction{Name: s.Name, Params: s.Params, ReturnTy: s.ReturnTy, Body: s.Body, Pos: s.Pos(), Exported: s.Exported, Private: s.Private}
		case *ClassStmt:
			if _, exists := classes[s.Name]; exists {
				return nil, fmt.Errorf("duplicate class %s", s.Name)
			}
			classDef := &ClassDef{
				Name:         s.Name,
				Methods:      make(map[string]*ScriptFunction),
				ClassMethods: make(map[string]*ScriptFunction),
				ClassVars:    make(map[string]Value),
				Body:         s.Body,
			}
			for _, prop := range s.Properties {
				for _, name := range prop.Names {
					if prop.Kind == "property" || prop.Kind == "getter" {
						getter := &ScriptFunction{
							Name: name,
							Body: []Statement{&ReturnStmt{Value: &IvarExpr{Name: name, position: prop.position}, position: prop.position}},
							Pos:  prop.position,
						}
						classDef.Methods[name] = getter
					}
					if prop.Kind == "property" || prop.Kind == "setter" {
						setter := &ScriptFunction{
							Name: name + "=",
							Params: []Param{{
								Name: "value",
							}},
							Body: []Statement{
								&AssignStmt{
									Target:   &IvarExpr{Name: name, position: prop.position},
									Value:    &Identifier{Name: "value", position: prop.position},
									position: prop.position,
								},
								&ReturnStmt{Value: &Identifier{Name: "value", position: prop.position}, position: prop.position},
							},
							Pos: prop.position,
						}
						classDef.Methods[name+"="] = setter
					}
				}
			}
			for _, fn := range s.Methods {
				classDef.Methods[fn.Name] = &ScriptFunction{Name: fn.Name, Params: fn.Params, ReturnTy: fn.ReturnTy, Body: fn.Body, Pos: fn.Pos(), Private: fn.Private}
			}
			for _, fn := range s.ClassMethods {
				classDef.ClassMethods[fn.Name] = &ScriptFunction{Name: fn.Name, Params: fn.Params, ReturnTy: fn.ReturnTy, Body: fn.Body, Pos: fn.Pos(), Private: fn.Private}
			}
			classes[s.Name] = classDef
		default:
			return nil, fmt.Errorf("unsupported top-level statement %T", stmt)
		}
	}

	script := &Script{engine: e, functions: functions, classes: classes, source: source}
	script.bindFunctionOwnership()
	return script, nil
}

func combineErrors(errs []error) error {
	if len(errs) == 1 {
		return errs[0]
	}
	msg := ""
	for _, err := range errs {
		if msg != "" {
			msg += "\n\n"
		}
		msg += err.Error()
	}
	return errors.New(msg)
}

func (s *Script) Call(ctx context.Context, name string, args []Value, opts CallOptions) (Value, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	_, ok := s.functions[name]
	if !ok {
		return NewNil(), fmt.Errorf("function %s not found", name)
	}

	root := newEnv(nil)
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

	exec := &Execution{
		engine:                    s.engine,
		script:                    s,
		ctx:                       ctx,
		quota:                     s.engine.config.StepQuota,
		memoryQuota:               s.engine.config.MemoryQuotaBytes,
		recursionCap:              s.engine.config.RecursionLimit,
		callStack:                 make([]callFrame, 0, 8),
		root:                      root,
		modules:                   make(map[string]Value),
		moduleLoading:             make(map[string]bool),
		moduleLoadStack:           make([]string, 0, 8),
		moduleStack:               make([]moduleContext, 0, 8),
		capabilityContracts:       make(map[*Builtin]CapabilityMethodContract),
		capabilityContractScopes:  make(map[*Builtin]*capabilityContractScope),
		capabilityContractsByName: make(map[string]CapabilityMethodContract),
		receiverStack:             make([]Value, 0, 8),
		envStack:                  make([]*Env, 0, 8),
		strictEffects:             s.engine.config.StrictEffects,
		allowRequire:              opts.AllowRequire,
	}

	if len(opts.Capabilities) > 0 {
		binding := CapabilityBinding{Context: exec.ctx, Engine: s.engine}
		for _, adapter := range opts.Capabilities {
			if adapter == nil {
				continue
			}
			scope := &capabilityContractScope{
				contracts: map[string]CapabilityMethodContract{},
			}
			if provider, ok := adapter.(CapabilityContractProvider); ok {
				for methodName, contract := range provider.CapabilityContracts() {
					name := strings.TrimSpace(methodName)
					if name == "" {
						return NewNil(), fmt.Errorf("capability contract method name must be non-empty")
					}
					if _, exists := exec.capabilityContractsByName[name]; exists {
						return NewNil(), fmt.Errorf("duplicate capability contract for %s", name)
					}
					exec.capabilityContractsByName[name] = contract
					scope.contracts[name] = contract
				}
			}
			globals, err := adapter.Bind(binding)
			if err != nil {
				return NewNil(), err
			}
			for name, val := range globals {
				rebound := rebinder.rebindValue(val)
				root.Define(name, rebound)
				if len(scope.contracts) > 0 {
					scope.roots = append(scope.roots, rebound)
				}
				bindCapabilityContracts(rebound, scope, exec.capabilityContracts, exec.capabilityContractScopes)
			}
		}
	}

	if exec.strictEffects {
		if err := validateStrictGlobals(opts.Globals); err != nil {
			return NewNil(), err
		}
	}

	for n, val := range opts.Globals {
		root.Define(n, rebinder.rebindValue(val))
	}

	if err := exec.checkMemory(); err != nil {
		return NewNil(), err
	}

	// initialize class bodies (class vars)
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
			return NewNil(), err
		}
	}

	callEnv := newEnv(root)
	callArgs := rebinder.rebindValues(args)
	callKeywords := rebinder.rebindKeywords(opts.Keywords)
	if err := exec.bindFunctionArgs(fn, callEnv, callArgs, callKeywords, fn.Pos); err != nil {
		return NewNil(), err
	}
	exec.pushEnv(callEnv)
	if err := exec.checkMemory(); err != nil {
		exec.popEnv()
		return NewNil(), err
	}
	exec.popEnv()

	if err := exec.pushFrame(fn.Name, fn.Pos); err != nil {
		return NewNil(), err
	}
	val, returned, err := exec.evalStatements(fn.Body, callEnv)
	exec.popFrame()
	if err != nil {
		return NewNil(), err
	}
	if fn.ReturnTy != nil {
		if err := checkValueType(val, fn.ReturnTy); err != nil {
			return NewNil(), exec.errorAt(fn.Pos, "%s", formatReturnTypeMismatch(fn.Name, err))
		}
	}
	if err := exec.checkMemoryWith(val); err != nil {
		return NewNil(), err
	}
	if returned {
		return val, nil
	}
	return val, nil
}
