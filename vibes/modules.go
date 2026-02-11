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
	path   string
	script *Script
}

func normalizeModuleName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("require: module name must be non-empty")
	}
	if filepath.Ext(trimmed) == "" {
		trimmed += ".vibe"
	}

	clean := filepath.Clean(trimmed)
	if clean == "." {
		return "", fmt.Errorf("require: module name %q resolves to current directory", name)
	}
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("require: module name %q must be relative", name)
	}

	sep := string(filepath.Separator)
	for _, part := range strings.Split(clean, sep) {
		if part == ".." {
			return "", fmt.Errorf("require: module name %q escapes search paths", name)
		}
	}

	return clean, nil
}

func (e *Engine) loadModule(name string) (moduleEntry, error) {
	normalized, err := normalizeModuleName(name)
	if err != nil {
		return moduleEntry{}, err
	}

	e.modMu.RLock()
	entry, ok := e.modules[normalized]
	e.modMu.RUnlock()
	if ok {
		return entry, nil
	}

	if len(e.modPaths) == 0 {
		return moduleEntry{}, fmt.Errorf("require: module paths not configured")
	}

	var content []byte
	var fullPath string
	found := false

	for _, dir := range e.modPaths {
		candidate := filepath.Join(dir, normalized)
		data, readErr := os.ReadFile(candidate)
		if readErr != nil {
			if errors.Is(readErr, fs.ErrNotExist) {
				continue
			}
			return moduleEntry{}, fmt.Errorf("require: reading %s: %w", candidate, readErr)
		}
		content = data
		fullPath = candidate
		found = true
		break
	}

	if !found {
		return moduleEntry{}, fmt.Errorf("require: module %q not found", name)
	}

	script, err := e.Compile(string(content))
	if err != nil {
		return moduleEntry{}, fmt.Errorf("require: compiling %s failed: %w", fullPath, err)
	}

	entry = moduleEntry{key: normalized, path: fullPath, script: script}

	e.modMu.Lock()
	if len(e.modules) >= e.config.MaxCachedModules {
		e.modMu.Unlock()
		return moduleEntry{}, fmt.Errorf("require: module cache limit reached (%d modules)", e.config.MaxCachedModules)
	}
	e.modules[normalized] = entry
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

	entry, err := exec.engine.loadModule(modNameVal.String())
	if err != nil {
		return NewNil(), err
	}

	if cached, ok := exec.modules[entry.key]; ok {
		return cached, nil
	}

	if exec.moduleLoading[entry.key] {
		return NewNil(), fmt.Errorf("require: circular dependency detected for module %q", entry.key)
	}
	exec.moduleLoading[entry.key] = true
	defer delete(exec.moduleLoading, entry.key)

	moduleEnv := newEnv(exec.root)
	exports := make(map[string]Value, len(entry.script.functions))
	for name, fn := range entry.script.functions {
		clone := cloneFunctionForEnv(fn, moduleEnv)
		fnVal := NewFunction(clone)
		moduleEnv.Define(name, fnVal)
		exports[name] = fnVal
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
