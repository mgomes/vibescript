package vibes

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	relative, err := moduleRelativePathLexical(caller.root, candidate)
	if err != nil {
		return moduleEntry{}, fmt.Errorf("require: module name %q escapes module root", request.raw)
	}
	key := moduleCacheKey(caller.root, relative)

	if entry, ok := e.getCachedModule(key); ok {
		if _, err := moduleRelativePath(caller.root, candidate); err != nil {
			return moduleEntry{}, fmt.Errorf("require: module name %q escapes module root", request.raw)
		}
		return entry, nil
	}

	relative, err = moduleRelativePath(caller.root, candidate)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return moduleEntry{}, fmt.Errorf("require: module %q not found", request.raw)
		}
		return moduleEntry{}, fmt.Errorf("require: module name %q escapes module root", request.raw)
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
		candidate := filepath.Join(root, request.normalized)

		if _, err := moduleRelativePath(root, candidate); err != nil {
			return moduleEntry{}, fmt.Errorf("require: module name %q escapes module root", request.raw)
		}

		if entry, ok := e.getCachedModule(key); ok {
			return entry, nil
		}
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
