package vibes

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
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

func normalizeModulePolicyPattern(pattern string) string {
	normalized := strings.TrimSpace(pattern)
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	normalized = strings.TrimPrefix(normalized, "./")
	normalized = strings.TrimSuffix(normalized, ".vibe")
	normalized = path.Clean(normalized)
	if normalized == "." {
		return ""
	}
	return normalized
}

func normalizeModulePolicyModuleName(relative string) string {
	normalized := filepath.ToSlash(filepath.Clean(relative))
	normalized = strings.TrimPrefix(normalized, "./")
	normalized = strings.TrimSuffix(normalized, ".vibe")
	if normalized == "." {
		return ""
	}
	return normalized
}

func validateModulePolicyPatterns(patterns []string, label string) error {
	for _, raw := range patterns {
		pattern := normalizeModulePolicyPattern(raw)
		if pattern == "" {
			return fmt.Errorf("vibes: module %s-list pattern cannot be empty", label)
		}
		if _, err := path.Match(pattern, "probe"); err != nil {
			return fmt.Errorf("vibes: invalid module %s-list pattern %q: %w", label, raw, err)
		}
	}
	return nil
}

func modulePolicyMatch(pattern string, module string) bool {
	if pattern == "*" {
		return module != ""
	}
	matched, err := path.Match(pattern, module)
	if err != nil {
		return false
	}
	return matched
}

func (e *Engine) enforceModulePolicy(relative string) error {
	module := normalizeModulePolicyModuleName(relative)
	if module == "" {
		return nil
	}

	for _, raw := range e.config.ModuleDenyList {
		pattern := normalizeModulePolicyPattern(raw)
		if pattern == "" {
			continue
		}
		if modulePolicyMatch(pattern, module) {
			return fmt.Errorf("require: module %q denied by policy", module)
		}
	}

	if len(e.config.ModuleAllowList) == 0 {
		return nil
	}
	for _, raw := range e.config.ModuleAllowList {
		pattern := normalizeModulePolicyPattern(raw)
		if pattern == "" {
			continue
		}
		if modulePolicyMatch(pattern, module) {
			return nil
		}
	}
	return fmt.Errorf("require: module %q not allowed by policy", module)
}

func parseModuleRequest(name string) (moduleRequest, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return moduleRequest{}, fmt.Errorf("require: module name must be non-empty")
	}
	normalizedName := strings.ReplaceAll(trimmed, "\\", string(filepath.Separator))
	normalizedName = strings.ReplaceAll(normalizedName, "/", string(filepath.Separator))

	request := moduleRequest{
		raw:              name,
		explicitRelative: isExplicitRelativeModulePath(trimmed),
	}
	if filepath.Ext(normalizedName) == "" {
		normalizedName += ".vibe"
	}

	request.normalized = filepath.Clean(normalizedName)
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
	normalized := strings.ReplaceAll(filepath.Clean(cleanPath), "\\", "/")
	return slices.Contains(strings.Split(normalized, "/"), "..")
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

func moduleDisplayName(key string) string {
	display := filepath.ToSlash(moduleKeyDisplay(key))
	return strings.TrimSuffix(display, ".vibe")
}

func moduleRelativePath(root, fullPath string) (string, error) {
	rel, err := moduleRelativePathLexical(root, fullPath)
	if err != nil {
		return "", err
	}
	cleanRoot := filepath.Clean(root)
	cleanPath := filepath.Clean(fullPath)

	resolvedRoot, err := resolvedExistingPath(cleanRoot)
	if err != nil {
		return "", err
	}
	resolvedPath, err := resolvedPathWithMissing(cleanPath)
	if err != nil {
		return "", err
	}
	resolvedRel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil {
		return "", err
	}
	resolvedRel = filepath.Clean(resolvedRel)
	sep := string(filepath.Separator)
	if resolvedRel == ".." || strings.HasPrefix(resolvedRel, ".."+sep) || filepath.IsAbs(resolvedRel) {
		return "", fmt.Errorf("require: module path %q escapes module root %q", cleanPath, cleanRoot)
	}
	return rel, nil
}

func moduleRelativePathLexical(root, fullPath string) (string, error) {
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

func resolvedExistingPath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolvedPath), nil
}

func resolvedPathWithMissing(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	cleanPath := filepath.Clean(absPath)

	existing := cleanPath
	suffix := make([]string, 0, 4)

	for {
		_, statErr := os.Lstat(existing)
		if statErr == nil {
			break
		}
		if !errors.Is(statErr, fs.ErrNotExist) {
			return "", statErr
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", statErr
		}
		suffix = append(suffix, filepath.Base(existing))
		existing = parent
	}

	resolvedExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}

	resolved := filepath.Clean(resolvedExisting)
	for i := len(suffix) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, suffix[i])
	}
	return resolved, nil
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
