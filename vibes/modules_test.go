package vibes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const moduleFixturesRoot = "testdata/modules"

func moduleTestEngine(t testing.TB) *Engine {
	t.Helper()
	return MustNewEngine(Config{ModulePaths: []string{filepath.FromSlash(moduleFixturesRoot)}})
}

func TestRequireProvidesExports(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run(value)
  helpers = require("helper")
  helpers.triple(value) + double(value)
end`)

	result, err := script.Call(context.Background(), "run", []Value{NewInt(3)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 15 {
		t.Fatalf("expected 15, got %#v", result)
	}
}

func TestRequireSupportsModuleAlias(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run(value)
  require("helper", as: "helpers")
  require("helper", as: "helpers")
  helpers.triple(value) + helpers.double(value)
end`)

	result, err := script.Call(context.Background(), "run", []Value{NewInt(3)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 15 {
		t.Fatalf("expected 15, got %#v", result)
	}
}

func TestRequireAliasValidation(t *testing.T) {
	engine := moduleTestEngine(t)

	cases := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name: "invalid identifier",
			source: `def run()
  require("helper", as: "123bad")
end`,
			wantErr: `require: invalid alias "123bad"`,
		},
		{
			name: "keyword alias",
			source: `def run()
  require("helper", as: "if")
end`,
			wantErr: `require: invalid alias "if"`,
		},
		{
			name: "invalid type",
			source: `def run()
  require("helper", as: 10)
end`,
			wantErr: "require: alias must be a string or symbol",
		},
		{
			name: "unknown keyword",
			source: `def run()
  require("helper", name: "helpers")
end`,
			wantErr: "require: unknown keyword argument name",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := compileScriptWithEngine(t, engine, tc.source)
			if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
				t.Fatalf("expected alias validation error")
			} else if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestRequireAliasRejectsConflictingGlobal(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def helpers(value)
  value
end

def run()
  require("helper", as: "helpers")
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, `require: alias "helpers" already defined`)
}

func TestRequireAliasConflictDoesNotLeakExportsWhenRescued(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def helpers(value)
  value
end

def run(value)
  begin
    require("helper", as: "helpers")
  rescue
    nil
  end

  begin
    double(value)
  rescue
    "missing"
  end
end`)

	result, err := script.Call(context.Background(), "run", []Value{NewInt(3)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindString || result.String() != "missing" {
		t.Fatalf("expected leaked export lookup to fail, got %#v", result)
	}
}

func TestRequirePreservesModuleLocalResolution(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def rate()
  100
end

def run(amount)
  fees = require("collision")
  fees.apply_fee(amount)
end`)

	result, err := script.Call(context.Background(), "run", []Value{NewInt(10)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 11 {
		t.Fatalf("expected module-local rate to be used, got %#v", result)
	}
}

func TestRequireNamespaceConflictKeepsExistingGlobalBinding(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def double(value)
  value + 1
end

def run(value)
  mod = require("helper")
  {
    global: double(value),
    module: mod.double(value)
  }
end`)

	result, err := script.Call(context.Background(), "run", []Value{NewInt(3)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindHash {
		t.Fatalf("expected hash result, got %#v", result)
	}
	out := result.Hash()
	if out["global"].Kind() != KindInt || out["global"].Int() != 4 {
		t.Fatalf("expected global binding to stay at 4, got %#v", out["global"])
	}
	if out["module"].Kind() != KindInt || out["module"].Int() != 6 {
		t.Fatalf("expected module object function to return 6, got %#v", out["module"])
	}
}

func TestRequireNamespaceConflictKeepsFirstModuleBinding(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run(value)
  first = require("helper")
  second = require("helper_alt")
  require("helper_alt", as: "alt")
  {
    global: double(value),
    first: first.double(value),
    second: second.double(value),
    alias: alt.double(value)
  }
end`)

	result, err := script.Call(context.Background(), "run", []Value{NewInt(3)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindHash {
		t.Fatalf("expected hash result, got %#v", result)
	}
	out := result.Hash()
	if out["global"].Kind() != KindInt || out["global"].Int() != 6 {
		t.Fatalf("expected first module binding to stay global at 6, got %#v", out["global"])
	}
	if out["first"].Kind() != KindInt || out["first"].Int() != 6 {
		t.Fatalf("expected first module object to return 6, got %#v", out["first"])
	}
	if out["second"].Kind() != KindInt || out["second"].Int() != 30 {
		t.Fatalf("expected second module object to return 30, got %#v", out["second"])
	}
	if out["alias"].Kind() != KindInt || out["alias"].Int() != 30 {
		t.Fatalf("expected alias module object to return 30, got %#v", out["alias"])
	}
}

func TestRequireMissingModule(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run()
  require("missing")
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, `module "missing" not found`)
}

func TestRequireCachesModules(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run()
  require("helper")
  require("helper")
  require("helper")
  double(10)
end`)

	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 20 {
		t.Fatalf("expected 20, got %#v", result)
	}

	if len(engine.modules) != 1 {
		t.Fatalf("expected 1 cached module, got %d", len(engine.modules))
	}
}

func TestClearModuleCacheForcesModuleReload(t *testing.T) {
	moduleRoot := t.TempDir()
	modulePath := filepath.Join(moduleRoot, "dynamic.vibe")
	writeModule := func(v int) {
		content := fmt.Sprintf(`def value()
  %d
end
`, v)
		if err := os.WriteFile(modulePath, []byte(content), 0o644); err != nil {
			t.Fatalf("write module: %v", err)
		}
	}

	writeModule(1)

	engine := MustNewEngine(Config{ModulePaths: []string{moduleRoot}})
	script := compileScriptWithEngine(t, engine, `def run()
  mod = require("dynamic")
  mod.value()
end`)

	first, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if first.Kind() != KindInt || first.Int() != 1 {
		t.Fatalf("expected first value 1, got %#v", first)
	}

	writeModule(2)

	stale, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("stale call failed: %v", err)
	}
	if stale.Kind() != KindInt || stale.Int() != 1 {
		t.Fatalf("expected cached value 1 before cache clear, got %#v", stale)
	}

	if cleared := engine.ClearModuleCache(); cleared != 1 {
		t.Fatalf("expected 1 cleared module, got %d", cleared)
	}

	refreshed, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("refreshed call failed: %v", err)
	}
	if refreshed.Kind() != KindInt || refreshed.Int() != 2 {
		t.Fatalf("expected refreshed value 2 after cache clear, got %#v", refreshed)
	}
}

func TestRequireRejectsAbsolutePaths(t *testing.T) {
	engine := moduleTestEngine(t)

	absPath := "/etc/passwd"
	if filepath.Separator == '\\' {
		drive := os.Getenv("SYSTEMDRIVE")
		if drive == "" {
			drive = "C:"
		}
		drive = drive + string(filepath.Separator)
		absPath = filepath.Join(drive, "Windows", "system32")
	}

	source := fmt.Sprintf(`def run()
  require(%q)
end`, absPath)

	script := compileScriptWithEngine(t, engine, source)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "must be relative")
}

func TestRequireRejectsPathTraversal(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run()
  require("nested/../../etc/passwd")
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "escapes search paths")
}

func TestRequireRejectsBackslashPathTraversal(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run()
  require("nested\\..\\..\\etc\\passwd")
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "escapes search paths")
}

func TestRequireNormalizesPathSeparators(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run(value)
  unix_style = require("shared/math")
  windows_style = require("shared\\math")
  unix_style.double(value) + windows_style.double(value)
end`)

	result, err := script.Call(context.Background(), "run", []Value{NewInt(3)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 12 {
		t.Fatalf("expected 12, got %#v", result)
	}
	if len(engine.modules) != 1 {
		t.Fatalf("expected normalized requires to share cache entry, got %d modules", len(engine.modules))
	}
}

func TestRequireRelativePathRequiresModuleCaller(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run()
  require("./helper")
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "requires a module caller")
}

func TestRequireRelativePathDoesNotLeakFromModuleIntoHostFunction(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def host_relative()
  require("./helper")
end

def run()
  mod = require("module_calls_host")
  mod.invoke_host_relative()
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "requires a module caller")
}

func TestRequireSupportsRelativePathsWithinModuleRoot(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run(value)
  mod = require("relative/root")
  mod.run(value)
end`)

	result, err := script.Call(context.Background(), "run", []Value{NewInt(5)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 16 {
		t.Fatalf("expected 16, got %#v", result)
	}
}

func TestRequireRelativePathRejectsEscapingModuleRoot(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run()
  mod = require("relative/escape")
  mod.run()
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "escapes module root")
}

func TestRequireRelativePathRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior is environment-specific on Windows")
	}

	moduleRoot := t.TempDir()
	outsideRoot := t.TempDir()

	secretModule := filepath.Join(outsideRoot, "secret.vibe")
	if err := os.WriteFile(secretModule, []byte(`def hidden()
  42
end
`), 0o644); err != nil {
		t.Fatalf("write secret module: %v", err)
	}

	symlinkPath := filepath.Join(moduleRoot, "link")
	if err := os.Symlink(outsideRoot, symlinkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	entryModule := filepath.Join(moduleRoot, "entry.vibe")
	if err := os.WriteFile(entryModule, []byte(`def run()
  require("./link/secret")
end
`), 0o644); err != nil {
		t.Fatalf("write entry module: %v", err)
	}

	engine := MustNewEngine(Config{ModulePaths: []string{moduleRoot}})
	script := compileScriptWithEngine(t, engine, `def run()
  mod = require("entry")
  mod.run()
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "escapes module root")
}

func TestRequireSearchPathRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior is environment-specific on Windows")
	}

	moduleRoot := t.TempDir()
	outsideRoot := t.TempDir()

	secretModule := filepath.Join(outsideRoot, "secret.vibe")
	if err := os.WriteFile(secretModule, []byte(`def hidden()
  42
end
`), 0o644); err != nil {
		t.Fatalf("write secret module: %v", err)
	}

	symlinkPath := filepath.Join(moduleRoot, "link")
	if err := os.Symlink(outsideRoot, symlinkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	engine := MustNewEngine(Config{ModulePaths: []string{moduleRoot}})
	script := compileScriptWithEngine(t, engine, `def run()
  require("link/secret")
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "escapes module root")
}

func TestRequireRelativePathRejectsOutOfRootCachedModule(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior is environment-specific on Windows")
	}

	moduleRoot := t.TempDir()
	outsideRoot := t.TempDir()

	if err := os.WriteFile(filepath.Join(outsideRoot, "secret.vibe"), []byte(`def value()
  9
end
`), 0o644); err != nil {
		t.Fatalf("write secret module: %v", err)
	}

	symlinkPath := filepath.Join(moduleRoot, "link")
	if err := os.Symlink(outsideRoot, symlinkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if err := os.WriteFile(filepath.Join(moduleRoot, "entry.vibe"), []byte(`def run()
  dep = require("./link/secret")
  dep.value()
end
`), 0o644); err != nil {
		t.Fatalf("write entry module: %v", err)
	}

	engine := MustNewEngine(Config{ModulePaths: []string{moduleRoot}})
	script := compileScriptWithEngine(t, engine, `def run()
  require("link/secret")
  entry = require("entry")
  entry.run()
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "escapes module root")
}

func TestRequireRelativePathUsesCacheBeforeFilesystemResolution(t *testing.T) {
	moduleRoot := t.TempDir()

	depPath := filepath.Join(moduleRoot, "dep.vibe")
	if err := os.WriteFile(depPath, []byte(`def value()
  7
end
`), 0o644); err != nil {
		t.Fatalf("write dep module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleRoot, "entry.vibe"), []byte(`def run()
  dep = require("./dep")
  dep.value()
end
`), 0o644); err != nil {
		t.Fatalf("write entry module: %v", err)
	}

	engine := MustNewEngine(Config{ModulePaths: []string{moduleRoot}})
	script := compileScriptWithEngine(t, engine, `def run()
  mod = require("entry")
  mod.run()
end`)

	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 7 {
		t.Fatalf("expected first call result 7, got %#v", result)
	}

	if err := os.Remove(depPath); err != nil {
		t.Fatalf("remove dep module: %v", err)
	}

	result, err = script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 7 {
		t.Fatalf("expected second call result 7, got %#v", result)
	}
}

func TestRequireRelativePathWorksInModuleDefinedBlockYieldedFromHost(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def host_each()
  yield()
end

def run()
  mod = require("block_host_yield")
  mod.run()
end`)

	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 33 {
		t.Fatalf("expected 33, got %#v", result)
	}
}

func TestRequireExportsOnlyNonPrivateFunctions(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run(value)
  mod = require("private_exports")
  if mod["_internal"] != nil
    0
  else
    visible(value) + mod.call_internal(value)
  end
end`)

	result, err := script.Call(context.Background(), "run", []Value{NewInt(2)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 105 {
		t.Fatalf("expected 105, got %#v", result)
	}
}

func TestRequireSupportsPrivateModuleExportOptOut(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run(value)
  mod = require("explicit_exports")
  {
    has_exposed: mod["exposed"] != nil,
    has_explicit_hidden: mod["_explicit_hidden"] != nil,
    has_helper: mod["helper"] != nil,
    has_internal: mod["_internal"] != nil,
    exposed: mod.exposed(value),
    explicit_hidden: mod._explicit_hidden(value)
  }
end`)

	result, err := script.Call(context.Background(), "run", []Value{NewInt(3)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindHash {
		t.Fatalf("expected hash result, got %#v", result)
	}
	out := result.Hash()
	if !out["has_exposed"].Bool() || !out["has_explicit_hidden"].Bool() {
		t.Fatalf("expected non-private exports to be present, got %#v", out)
	}
	if out["has_helper"].Bool() || out["has_internal"].Bool() {
		t.Fatalf("expected private helpers to be hidden, got %#v", out)
	}
	if out["exposed"].Kind() != KindInt || out["exposed"].Int() != 10 {
		t.Fatalf("expected exposed(3)=10, got %#v", out["exposed"])
	}
	if out["explicit_hidden"].Kind() != KindInt || out["explicit_hidden"].Int() != 103 {
		t.Fatalf("expected _explicit_hidden(3)=103, got %#v", out["explicit_hidden"])
	}
}

func TestRequirePrivateFunctionsAreNotInjectedAsGlobals(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run(value)
  require("explicit_exports")
  helper(value)
end`)

	requireCallErrorContains(t, script, "run", []Value{NewInt(2)}, CallOptions{}, "undefined variable helper")
}

func TestExportKeywordValidation(t *testing.T) {
	requireCompileErrorContainsDefault(t, `export helper`, "expected 'def'")

	requireCompileErrorContainsDefault(t, `class Example
  export def value()
    1
  end
end`, "export is only supported for top-level functions")

	requireCompileErrorContainsDefault(t, `def outer()
  if true
    export def nested()
      1
    end
  end
end`, "export is only supported for top-level functions")
}

func TestPrivateKeywordValidation(t *testing.T) {
	requireCompileErrorContainsDefault(t, `private helper`, "expected 'def'")

	requireCompileErrorContainsDefault(t, `def outer()
  if true
    private def nested()
      1
    end
  end
end`, "private is only supported for top-level functions and class methods")

	requireCompileErrorContainsDefault(t, `private def self.value()
  1
end`, "private cannot be used with class methods")
}

func TestRequirePrivateFunctionsRemainModuleScoped(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run(value)
  require("private_exports")
  _internal(value)
end`)

	requireCallErrorContains(t, script, "run", []Value{NewInt(2)}, CallOptions{}, "undefined variable _internal")
}

func TestRequireModuleCacheAvoidsDuplicateLoads(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run()
  require("circular_a")
  require("circular_b")
  "ok"
end`)

	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.String() != "ok" {
		t.Fatalf("expected ok, got %#v", result)
	}
}

func TestRequireRuntimeModuleRecursionHitsRecursionLimit(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run()
  mod = require("circular_runtime_a")
  mod.enter()
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "recursion depth exceeded")
}

func TestRequireAllowsCachedModuleReuseAcrossModuleCalls(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run()
  mod = require("require_cached_a")
  mod.start()
end`)

	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 21 {
		t.Fatalf("expected 21, got %#v", result)
	}
}

func TestRequireConcurrentLoading(t *testing.T) {
	engine := moduleTestEngine(t)

	script := compileScriptWithEngine(t, engine, `def run()
  require("helper")
  double(5)
end`)

	const goroutines = 10
	results := make(chan error, goroutines)

	for range goroutines {
		go func() {
			result, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				results <- err
				return
			}
			if result.Kind() != KindInt || result.Int() != 10 {
				results <- fmt.Errorf("expected 10, got %#v", result)
				return
			}
			results <- nil
		}()
	}

	for range goroutines {
		if err := <-results; err != nil {
			t.Fatalf("concurrent call failed: %v", err)
		}
	}

	if len(engine.modules) != 1 {
		t.Fatalf("expected 1 cached module after concurrent access, got %d", len(engine.modules))
	}
}

func TestRequireStrictEffectsRequiresAllowRequire(t *testing.T) {
	engine := MustNewEngine(Config{
		StrictEffects: true,
		ModulePaths:   []string{filepath.Join("testdata", "modules")},
	})

	script := compileScriptWithEngine(t, engine, `def run()
  require("helper")
end`)

	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected strict effects require error")
	}
	if got := err.Error(); !strings.Contains(got, "strict effects: require is disabled") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRequireStrictEffectsAllowsRequireWhenOptedIn(t *testing.T) {
	engine := MustNewEngine(Config{
		StrictEffects: true,
		ModulePaths:   []string{filepath.Join("testdata", "modules")},
	})

	script := compileScriptWithEngine(t, engine, `def run(v)
  helpers = require("helper")
  helpers.triple(v)
end`)

	result, err := script.Call(context.Background(), "run", []Value{NewInt(4)}, CallOptions{
		AllowRequire: true,
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 12 {
		t.Fatalf("expected 12, got %#v", result)
	}
}

func TestRequireModuleAllowList(t *testing.T) {
	engine := MustNewEngine(Config{
		ModulePaths:     []string{filepath.Join("testdata", "modules")},
		ModuleAllowList: []string{"shared/*"},
	})

	script := compileScriptWithEngine(t, engine, `def run_allowed(value)
  mod = require("shared/math")
  mod.double(value)
end

def run_denied(value)
  mod = require("helper")
  mod.double(value)
end`)

	allowed, err := script.Call(context.Background(), "run_allowed", []Value{NewInt(3)}, CallOptions{})
	if err != nil {
		t.Fatalf("allowed call failed: %v", err)
	}
	if allowed.Kind() != KindInt || allowed.Int() != 6 {
		t.Fatalf("expected allowed result 6, got %#v", allowed)
	}

	if _, err := script.Call(context.Background(), "run_denied", []Value{NewInt(3)}, CallOptions{}); err == nil {
		t.Fatalf("expected denied module error")
	} else if !strings.Contains(err.Error(), `require: module "helper" not allowed by policy`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRequireModuleAllowListStarMatchesNestedModules(t *testing.T) {
	engine := MustNewEngine(Config{
		ModulePaths:     []string{filepath.Join("testdata", "modules")},
		ModuleAllowList: []string{"*"},
	})

	script := compileScriptWithEngine(t, engine, `def run(value)
  mod = require("shared/math")
  mod.double(value)
end`)

	result, err := script.Call(context.Background(), "run", []Value{NewInt(4)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 8 {
		t.Fatalf("expected nested module call result 8, got %#v", result)
	}
}

func TestRequireModuleDenyListOverridesAllowList(t *testing.T) {
	engine := MustNewEngine(Config{
		ModulePaths:     []string{filepath.Join("testdata", "modules")},
		ModuleAllowList: []string{"*"},
		ModuleDenyList:  []string{"helper"},
	})

	script := compileScriptWithEngine(t, engine, `def run()
  require("helper")
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, `require: module "helper" denied by policy`)
}

func TestModulePolicyPatternValidation(t *testing.T) {
	_, err := NewEngine(Config{
		ModulePaths:     []string{filepath.Join("testdata", "modules")},
		ModuleAllowList: []string{"[invalid"},
	})
	requireErrorContains(t, err, "invalid module allow-list pattern")
}

func TestFormatModuleCycleUsesConciseChain(t *testing.T) {
	root := filepath.Join("tmp", "modules")
	a := moduleCacheKey(root, filepath.Join("nested", "a.vibe"))
	b := moduleCacheKey(root, filepath.Join("nested", "b.vibe"))

	got := formatModuleCycle([]string{a, b, b, a})
	want := filepath.ToSlash(filepath.Join("nested", "a")) + " -> " + filepath.ToSlash(filepath.Join("nested", "b")) + " -> " + filepath.ToSlash(filepath.Join("nested", "a"))
	if got != want {
		t.Fatalf("expected cycle %q, got %q", want, got)
	}
}

func TestModuleDisplayNameTrimsExtension(t *testing.T) {
	key := moduleCacheKey(filepath.Join("tmp", "modules"), filepath.Join("pkg", "helper.vibe"))
	got := moduleDisplayName(key)
	want := filepath.ToSlash(filepath.Join("pkg", "helper"))
	if got != want {
		t.Fatalf("expected display %q, got %q", want, got)
	}
}
