package vibes

import (
	"fmt"
	"path/filepath"
)

func (e *Engine) compileAndCacheModule(key, root, relative, fullPath string, content []byte) (moduleEntry, error) {
	if err := e.enforceModulePolicy(relative); err != nil {
		return moduleEntry{}, err
	}

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
