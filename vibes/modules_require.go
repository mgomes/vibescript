package vibes

import (
	"fmt"
	"reflect"
	"strings"
)

func moduleCycleFromLoadStack(stack []string, next string) ([]string, bool) {
	for idx, key := range stack {
		if key == next {
			cycle := append(append([]string(nil), stack[idx:]...), next)
			return cycle, true
		}
	}
	return nil, false
}

func moduleExecutionChain(stack []moduleContext) []string {
	chain := make([]string, 0, len(stack))
	for _, ctx := range stack {
		if ctx.key == "" {
			continue
		}
		if len(chain) > 0 && chain[len(chain)-1] == ctx.key {
			continue
		}
		chain = append(chain, ctx.key)
	}
	return chain
}

func moduleCycleFromExecution(stack []moduleContext, next string) ([]string, bool) {
	chain := moduleExecutionChain(stack)
	if len(chain) < 2 {
		return nil, false
	}
	for idx, key := range chain[:len(chain)-1] {
		if key == next {
			cycle := append(append([]string(nil), chain[idx:]...), next)
			return cycle, true
		}
	}
	return nil, false
}

func formatModuleCycle(cycle []string) string {
	if len(cycle) == 0 {
		return ""
	}
	normalized := make([]string, 0, len(cycle))
	for _, key := range cycle {
		if len(normalized) > 0 && normalized[len(normalized)-1] == key {
			continue
		}
		normalized = append(normalized, key)
	}
	parts := make([]string, len(normalized))
	for idx, key := range normalized {
		parts[idx] = moduleDisplayName(key)
	}
	return strings.Join(parts, " -> ")
}

func shouldExportModuleFunction(fn *ScriptFunction) bool {
	return fn != nil && !fn.Private
}

func parseRequireAlias(kwargs map[string]Value) (string, error) {
	if len(kwargs) == 0 {
		return "", nil
	}
	if len(kwargs) != 1 {
		for key := range kwargs {
			if key != "as" {
				return "", fmt.Errorf("require: unknown keyword argument %s", key)
			}
		}
		return "", fmt.Errorf("require: unknown keyword arguments")
	}

	aliasVal, ok := kwargs["as"]
	if !ok {
		for key := range kwargs {
			return "", fmt.Errorf("require: unknown keyword argument %s", key)
		}
		return "", fmt.Errorf("require: unknown keyword arguments")
	}

	var aliasName string
	switch aliasVal.Kind() {
	case KindString, KindSymbol:
		aliasName = strings.TrimSpace(aliasVal.String())
	default:
		return "", fmt.Errorf("require: alias must be a string or symbol")
	}

	if !isValidModuleAlias(aliasName) {
		return "", fmt.Errorf("require: invalid alias %q", aliasName)
	}

	return aliasName, nil
}

func isValidModuleAlias(name string) bool {
	if name == "" {
		return false
	}
	runes := []rune(name)
	if len(runes) == 0 || !isIdentifierStart(runes[0]) {
		return false
	}
	for _, r := range runes[1:] {
		if !isIdentifierRune(r) {
			return false
		}
	}
	return lookupIdent(name) == tokenIdent
}

func bindRequireAlias(root *Env, alias string, module Value) error {
	if err := validateRequireAliasBinding(root, alias, module); err != nil {
		return err
	}
	if alias == "" {
		return nil
	}
	root.Define(alias, module)
	return nil
}

func validateRequireAliasBinding(root *Env, alias string, module Value) error {
	if alias == "" {
		return nil
	}
	if existing, ok := root.Get(alias); ok {
		if existing.Kind() == KindObject && module.Kind() == KindObject && reflect.ValueOf(existing.Hash()).Pointer() == reflect.ValueOf(module.Hash()).Pointer() {
			return nil
		}
		return fmt.Errorf("require: alias %q already defined", alias)
	}
	return nil
}

func bindModuleExportsWithoutOverwrite(root *Env, exports map[string]Value) {
	for name, fnVal := range exports {
		if _, exists := root.Get(name); exists {
			continue
		}
		root.Define(name, fnVal)
	}
}

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
