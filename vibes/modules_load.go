package vibes

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

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
