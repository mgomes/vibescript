package vibes

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type moduleEntry struct {
	key    string
	name   string
	path   string
	script *Script
}

type moduleRequest struct {
	raw              string
	normalized       string
	explicitRelative bool
}

const moduleKeySeparator = "::"

func parseModuleRequest(name string) (moduleRequest, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return moduleRequest{}, fmt.Errorf("require: module name must be non-empty")
	}
	request := moduleRequest{
		raw:              name,
		explicitRelative: isExplicitRelativeModulePath(trimmed),
	}
	if filepath.Ext(trimmed) == "" {
		trimmed += ".vibe"
	}

	request.normalized = filepath.Clean(trimmed)
	if request.normalized == "." {
		return moduleRequest{}, fmt.Errorf("require: module name %q resolves to current directory", name)
	}
	if filepath.IsAbs(request.normalized) {
		return moduleRequest{}, fmt.Errorf("require: module name %q must be relative", name)
	}
	if !request.explicitRelative && containsPathTraversal(request.normalized) {
		return moduleRequest{}, fmt.Errorf("require: module name %q escapes search paths", name)
	}

	return request, nil
}

func isExplicitRelativeModulePath(name string) bool {
	return strings.HasPrefix(name, "./") ||
		strings.HasPrefix(name, "../") ||
		strings.HasPrefix(name, ".\\") ||
		strings.HasPrefix(name, "..\\")
}

func containsPathTraversal(cleanPath string) bool {
	sep := string(filepath.Separator)
	for _, part := range strings.Split(cleanPath, sep) {
		if part == ".." {
			return true
		}
	}
	return false
}

func moduleCacheKey(root, relative string) string {
	return filepath.Clean(root) + moduleKeySeparator + filepath.Clean(relative)
}

func moduleKeyDisplay(key string) string {
	idx := strings.LastIndex(key, moduleKeySeparator)
	if idx < 0 {
		return key
	}
	display := key[idx+len(moduleKeySeparator):]
	if display == "" {
		return key
	}
	return display
}

func moduleRelativePath(root, fullPath string) (string, error) {
	cleanRoot := filepath.Clean(root)
	cleanPath := filepath.Clean(fullPath)

	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return "", err
	}
	rel = filepath.Clean(rel)
	sep := string(filepath.Separator)
	if rel == ".." || strings.HasPrefix(rel, ".."+sep) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("require: module path %q escapes module root %q", cleanPath, cleanRoot)
	}
	return rel, nil
}

func (e *Engine) getCachedModule(key string) (moduleEntry, bool) {
	e.modMu.RLock()
	entry, ok := e.modules[key]
	e.modMu.RUnlock()
	return entry, ok
}

func (e *Engine) loadModule(name string, caller *moduleContext) (moduleEntry, error) {
	request, err := parseModuleRequest(name)
	if err != nil {
		return moduleEntry{}, err
	}

	if request.explicitRelative {
		if caller == nil || caller.path == "" || caller.root == "" {
			return moduleEntry{}, fmt.Errorf("require: relative module %q requires a module caller", name)
		}
		return e.loadRelativeModule(request, *caller)
	}

	return e.loadSearchPathModule(request)
}

func (e *Engine) loadRelativeModule(request moduleRequest, caller moduleContext) (moduleEntry, error) {
	candidate := filepath.Clean(filepath.Join(filepath.Dir(caller.path), request.normalized))
	relative, err := moduleRelativePath(caller.root, candidate)
	if err != nil {
		return moduleEntry{}, fmt.Errorf("require: module name %q escapes module root", request.raw)
	}
	key := moduleCacheKey(caller.root, relative)

	if entry, ok := e.getCachedModule(key); ok {
		return entry, nil
	}

	data, readErr := os.ReadFile(candidate)
	if readErr != nil {
		if errors.Is(readErr, fs.ErrNotExist) {
			return moduleEntry{}, fmt.Errorf("require: module %q not found", request.raw)
		}
		return moduleEntry{}, fmt.Errorf("require: reading %s: %w", candidate, readErr)
	}

	return e.compileAndCacheModule(key, caller.root, relative, candidate, data)
}

func (e *Engine) loadSearchPathModule(request moduleRequest) (moduleEntry, error) {
	if len(e.modPaths) == 0 {
		return moduleEntry{}, fmt.Errorf("require: module paths not configured")
	}

	for _, root := range e.modPaths {
		key := moduleCacheKey(root, request.normalized)
		if entry, ok := e.getCachedModule(key); ok {
			return entry, nil
		}

		candidate := filepath.Join(root, request.normalized)
		data, readErr := os.ReadFile(candidate)
		if readErr != nil {
			if errors.Is(readErr, fs.ErrNotExist) {
				continue
			}
			return moduleEntry{}, fmt.Errorf("require: reading %s: %w", candidate, readErr)
		}

		return e.compileAndCacheModule(key, root, request.normalized, candidate, data)
	}

	return moduleEntry{}, fmt.Errorf("require: module %q not found", request.raw)
}

func (e *Engine) compileAndCacheModule(key, root, relative, fullPath string, content []byte) (moduleEntry, error) {
	script, err := e.Compile(string(content))
	if err != nil {
		return moduleEntry{}, fmt.Errorf("require: compiling %s failed: %w", fullPath, err)
	}

	entry := moduleEntry{
		key:    key,
		name:   filepath.Clean(relative),
		path:   filepath.Clean(fullPath),
		script: script,
	}
	script.moduleKey = key
	script.modulePath = entry.path
	script.moduleRoot = filepath.Clean(root)

	e.modMu.Lock()
	if cached, ok := e.modules[key]; ok {
		e.modMu.Unlock()
		return cached, nil
	}
	if len(e.modules) >= e.config.MaxCachedModules {
		e.modMu.Unlock()
		return moduleEntry{}, fmt.Errorf("require: module cache limit reached (%d modules)", e.config.MaxCachedModules)
	}
	e.modules[key] = entry
	e.modMu.Unlock()

	return entry, nil
}

// cloneFunctionForEnv creates a shallow copy of a ScriptFunction with a different environment.
// This is safe because ScriptFunction fields are immutable except for Env which we explicitly override.
func cloneFunctionForEnv(fn *ScriptFunction, env *Env) *ScriptFunction {
	clone := *fn
	clone.Env = env
	return &clone
}

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
	parts := make([]string, len(cycle))
	for idx, key := range cycle {
		parts[idx] = moduleKeyDisplay(key)
	}
	return strings.Join(parts, " -> ")
}

func isPublicModuleExport(name string) bool {
	return name != "" && !strings.HasPrefix(name, "_")
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

	if cycle, ok := moduleCycleFromExecution(exec.moduleStack, entry.key); ok {
		return NewNil(), fmt.Errorf("require: circular dependency detected: %s", formatModuleCycle(cycle))
	}

	if cached, ok := exec.modules[entry.key]; ok {
		return cached, nil
	}

	if exec.moduleLoading[entry.key] {
		return NewNil(), fmt.Errorf("require: circular dependency detected for module %q", moduleKeyDisplay(entry.key))
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
		if isPublicModuleExport(name) {
			exports[name] = fnVal
		}
	}

	for name, fnVal := range exports {
		if _, exists := exec.root.Get(name); exists {
			continue
		}
		exec.root.Define(name, fnVal)
	}

	exportsVal := NewObject(exports)
	exec.modules[entry.key] = exportsVal
	return exportsVal, nil
}
