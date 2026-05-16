package vibes

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

func normalizeModulePolicyPattern(pattern string) string {
	return normalizeModulePolicyValue(pattern)
}

func normalizeModulePolicyModuleName(relative string) string {
	return normalizeModulePolicyValue(relative)
}

// normalizeModulePolicyValue canonicalizes a pattern or module name for
// policy comparison. After path normalization it iteratively strips
// ".vibe" suffixes from the *basename* only, never from intermediate
// directory components, until either no ".vibe" suffix remains or
// stripping the next one would empty the basename (so ".vibe" and
// "helper/.vibe" are preserved rather than collapsing to "").
//
// The function is idempotent: normalize(normalize(x)) == normalize(x)
// for every input. Equivalent spellings of the same logical module —
// "helper", "helper.vibe", "./helper.vibe", "helper.vibe.vibe" — all
// reduce to "helper". Directory names keep their dots:
// "helper.vibe/foo.vibe" reduces to "helper.vibe/foo".
func normalizeModulePolicyValue(value string) string {
	current := normalizeModulePolicyPath(value)
	for {
		if current == "" {
			return ""
		}
		dir, base := path.Split(current)
		if !strings.HasSuffix(base, ".vibe") {
			return current
		}
		trimmed := strings.TrimSuffix(base, ".vibe")
		if trimmed == "" {
			return current
		}
		next := normalizeModulePolicyPath(dir + trimmed)
		if next == "" {
			return current
		}
		current = next
	}
}

func normalizeModulePolicyPath(value string) string {
	normalized := strings.TrimSpace(value)
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	normalized = filepath.ToSlash(normalized)
	normalized = strings.TrimPrefix(normalized, "./")
	normalized = path.Clean(normalized)
	if normalized == "." {
		return ""
	}
	parts := strings.Split(normalized, "/")
	for i, part := range parts {
		parts[i] = strings.TrimSpace(part)
	}
	normalized = path.Clean(strings.Join(parts, "/"))
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
		if len(e.config.ModuleAllowList) > 0 || len(e.config.ModuleDenyList) > 0 {
			return fmt.Errorf("require: module name %q is invalid", relative)
		}
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
