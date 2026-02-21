package vibes

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

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
