package vibes

import (
	"fmt"
)

func builtinRequire(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if exec.strictEffects && !exec.allowRequire {
		return NewNil(), fmt.Errorf("strict effects: require is disabled without CallOptions.AllowRequire")
	}
	if len(args) != 1 {
		return NewNil(), fmt.Errorf("require expects a single module name argument")
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("require does not accept blocks")
	}
	if exec.root == nil {
		return NewNil(), fmt.Errorf("require unavailable in this context")
	}
	alias, err := parseRequireAlias(kwargs)
	if err != nil {
		return NewNil(), err
	}

	modNameVal := args[0]
	switch modNameVal.Kind() {
	case KindString, KindSymbol:
		// supported
	default:
		return NewNil(), fmt.Errorf("require expects a string or symbol module name")
	}

	entry, err := exec.engine.loadModule(modNameVal.String(), exec.currentModuleContext())
	if err != nil {
		return NewNil(), err
	}

	if cycle, ok := moduleCycleFromLoadStack(exec.moduleLoadStack, entry.key); ok {
		return NewNil(), fmt.Errorf("require: circular dependency detected: %s", formatModuleCycle(cycle))
	}

	if cached, ok := exec.modules[entry.key]; ok {
		if err := bindRequireAlias(exec.root, alias, cached); err != nil {
			return NewNil(), err
		}
		return cached, nil
	}

	if cycle, ok := moduleCycleFromExecution(exec.moduleStack, entry.key); ok {
		return NewNil(), fmt.Errorf("require: circular dependency detected: %s", formatModuleCycle(cycle))
	}

	if exec.moduleLoading[entry.key] {
		cycle := append(append([]string(nil), exec.moduleLoadStack...), entry.key)
		return NewNil(), fmt.Errorf("require: circular dependency detected: %s", formatModuleCycle(cycle))
	}
	exec.moduleLoading[entry.key] = true
	exec.moduleLoadStack = append(exec.moduleLoadStack, entry.key)
	defer func() {
		delete(exec.moduleLoading, entry.key)
		if len(exec.moduleLoadStack) > 0 {
			exec.moduleLoadStack = exec.moduleLoadStack[:len(exec.moduleLoadStack)-1]
		}
	}()

	moduleEnv := newEnv(exec.root)
	exports := make(map[string]Value, len(entry.script.functions))
	for name, fn := range entry.script.functions {
		clone := cloneFunctionForEnv(fn, moduleEnv)
		fnVal := NewFunction(clone)
		moduleEnv.Define(name, fnVal)
		if shouldExportModuleFunction(fn) {
			exports[name] = fnVal
		}
	}

	exportsVal := NewObject(exports)
	if err := validateRequireAliasBinding(exec.root, alias, exportsVal); err != nil {
		return NewNil(), err
	}
	bindModuleExportsWithoutOverwrite(exec.root, exports)
	exec.modules[entry.key] = exportsVal
	if alias != "" {
		exec.root.Define(alias, exportsVal)
	}
	return exportsVal, nil
}
