package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moduleFile describes one .vibe file written into a temporary module tree.
type moduleFile struct {
	path    string
	content string
}

// tempModuleTree creates a temporary directory and populates it with the
// given module files. Any intermediate directories in each path are
// created automatically. The directory is cleaned up by t.
func tempModuleTree(t testing.TB, files ...moduleFile) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range files {
		full := filepath.Join(root, filepath.FromSlash(f.path))
		if dir := filepath.Dir(full); dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", dir, err)
			}
		}
		if err := os.WriteFile(full, []byte(f.content), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return root
}

// mustNewEngineWithModuleRoot constructs an engine whose only module
// search path is root.
func mustNewEngineWithModuleRoot(t testing.TB, root string) *Engine {
	t.Helper()
	return MustNewEngine(Config{ModulePaths: []string{root}})
}

func compileScriptWithConfig(t testing.TB, cfg Config, source string) *Script {
	t.Helper()
	engine := MustNewEngine(cfg)
	script, err := engine.Compile(source)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	return script
}

func compileScriptDefault(t testing.TB, source string) *Script {
	t.Helper()
	return compileScriptWithConfig(t, Config{}, source)
}

func compileScriptFromFileWithConfig(t testing.TB, cfg Config, path string) *Script {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return compileScriptWithConfig(t, cfg, string(source))
}

func compileScriptFromFileDefault(t testing.TB, path string) *Script {
	t.Helper()
	return compileScriptFromFileWithConfig(t, Config{}, path)
}

func compileScriptFromFileWithEngine(t testing.TB, engine *Engine, path string) *Script {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return compileScriptWithEngine(t, engine, string(source))
}

func compileScriptWithEngine(t testing.TB, engine *Engine, source string) *Script {
	t.Helper()
	script, err := engine.Compile(source)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	return script
}

func compileScriptErrorWithConfig(t testing.TB, cfg Config, source string) error {
	t.Helper()
	engine := MustNewEngine(cfg)
	_, err := engine.Compile(source)
	if err == nil {
		t.Fatalf("expected compile to fail")
	}
	return err
}

func compileScriptErrorDefault(t testing.TB, source string) error {
	t.Helper()
	return compileScriptErrorWithConfig(t, Config{}, source)
}

func requireCompileErrorContainsWithConfig(t testing.TB, cfg Config, source, want string) {
	t.Helper()
	err := compileScriptErrorWithConfig(t, cfg, source)
	requireErrorContains(t, err, want)
}

func requireCompileErrorContainsDefault(t testing.TB, source, want string) {
	t.Helper()
	requireCompileErrorContainsWithConfig(t, Config{}, source, want)
}

func callScript(t testing.TB, ctx context.Context, script *Script, fn string, args []Value, opts CallOptions) Value {
	t.Helper()
	result, err := script.Call(ctx, fn, args, opts)
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	return result
}

func callScriptErr(t testing.TB, ctx context.Context, script *Script, fn string, args []Value, opts CallOptions) error {
	t.Helper()
	_, err := script.Call(ctx, fn, args, opts)
	if err == nil {
		t.Fatalf("expected call to fail")
	}
	return err
}

func requireCallErrorContains(t testing.TB, script *Script, fn string, args []Value, opts CallOptions, want string) {
	t.Helper()
	err := callScriptErr(t, context.Background(), script, fn, args, opts)
	requireErrorContains(t, err, want)
}

func requireCallErrorIs(t testing.TB, script *Script, fn string, args []Value, opts CallOptions, target error) {
	t.Helper()
	err := callScriptErr(t, context.Background(), script, fn, args, opts)
	requireErrorIs(t, err, target)
}

func requireErrorContains(t testing.TB, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", want)
	}
	if got := err.Error(); !strings.Contains(got, want) {
		t.Fatalf("unexpected error: %s", got)
	}
}

func requireErrorIs(t testing.TB, err, target error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error matching %v, got nil", target)
	}
	if !errors.Is(err, target) {
		t.Fatalf("expected error matching %v, got %v", target, err)
	}
}

func callOptionsWithCapabilities(capabilities ...CapabilityAdapter) CallOptions {
	return CallOptions{Capabilities: capabilities}
}
