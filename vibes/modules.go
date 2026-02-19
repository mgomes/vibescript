package vibes

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
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

func isPublicModuleExport(name string) bool {
	return name != "" && !strings.HasPrefix(name, "_")
}

func moduleHasExplicitExports(functions map[string]*ScriptFunction) bool {
	for _, fn := range functions {
		if fn != nil && fn.Exported {
			return true
		}
	}
	return false
}

func shouldExportModuleFunction(name string, fn *ScriptFunction, hasExplicitExports bool) bool {
	if hasExplicitExports {
		return fn != nil && fn.Exported
	}
	return isPublicModuleExport(name)
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
	hasExplicitExports := moduleHasExplicitExports(entry.script.functions)
	for name, fn := range entry.script.functions {
		clone := cloneFunctionForEnv(fn, moduleEnv)
		fnVal := NewFunction(clone)
		moduleEnv.Define(name, fnVal)
		if shouldExportModuleFunction(name, fn, hasExplicitExports) {
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
