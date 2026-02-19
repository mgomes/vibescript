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

func TestRequireProvidesExports(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run(value)
  helpers = require("helper")
  helpers.triple(value) + double(value)
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", []Value{NewInt(3)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 15 {
		t.Fatalf("expected 15, got %#v", result)
	}
}

func TestRequireSupportsModuleAlias(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run(value)
  require("helper", as: "helpers")
  require("helper", as: "helpers")
  helpers.triple(value) + helpers.double(value)
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", []Value{NewInt(3)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 15 {
		t.Fatalf("expected 15, got %#v", result)
	}
}

func TestRequireAliasValidation(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

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
			script, err := engine.Compile(tc.source)
			if err != nil {
				t.Fatalf("compile failed: %v", err)
			}
			if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
				t.Fatalf("expected alias validation error")
			} else if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestRequireAliasRejectsConflictingGlobal(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def helpers(value)
  value
end

def run()
  require("helper", as: "helpers")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatalf("expected alias conflict error")
	} else if !strings.Contains(err.Error(), `require: alias "helpers" already defined`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRequireAliasConflictDoesNotLeakExportsWhenRescued(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def helpers(value)
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
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", []Value{NewInt(3)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindString || result.String() != "missing" {
		t.Fatalf("expected leaked export lookup to fail, got %#v", result)
	}
}

func TestRequirePreservesModuleLocalResolution(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def rate()
  100
end

def run(amount)
  fees = require("collision")
  fees.apply_fee(amount)
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", []Value{NewInt(10)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 11 {
		t.Fatalf("expected module-local rate to be used, got %#v", result)
	}
}

func TestRequireNamespaceConflictKeepsExistingGlobalBinding(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def double(value)
  value + 1
end

def run(value)
  mod = require("helper")
  {
    global: double(value),
    module: mod.double(value)
  }
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

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
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run(value)
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
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

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
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run()
  require("missing")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatalf("expected missing module error")
	} else if !strings.Contains(err.Error(), `module "missing" not found`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRequireCachesModules(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run()
  require("helper")
  require("helper")
  require("helper")
  double(10)
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

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
	script, err := engine.Compile(`def run()
  mod = require("dynamic")
  mod.value()
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

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
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

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

	script, err := engine.Compile(source)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatalf("expected error for absolute path")
	} else if !strings.Contains(err.Error(), "must be relative") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRequireRejectsPathTraversal(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run()
  require("nested/../../etc/passwd")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatalf("expected error for path traversal")
	} else if !strings.Contains(err.Error(), "escapes search paths") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRequireRejectsBackslashPathTraversal(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run()
  require("nested\\..\\..\\etc\\passwd")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatalf("expected error for backslash path traversal")
	} else if !strings.Contains(err.Error(), "escapes search paths") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRequireNormalizesPathSeparators(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run(value)
  unix_style = require("shared/math")
  windows_style = require("shared\\math")
  unix_style.double(value) + windows_style.double(value)
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

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
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run()
  require("./helper")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatalf("expected relative caller error")
	} else if !strings.Contains(err.Error(), "requires a module caller") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRequireRelativePathDoesNotLeakFromModuleIntoHostFunction(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def host_relative()
  require("./helper")
end

def run()
  mod = require("module_calls_host")
  mod.invoke_host_relative()
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatalf("expected relative caller error from host function")
	} else if !strings.Contains(err.Error(), "requires a module caller") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRequireSupportsRelativePathsWithinModuleRoot(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run(value)
  mod = require("relative/root")
  mod.run(value)
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", []Value{NewInt(5)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 16 {
		t.Fatalf("expected 16, got %#v", result)
	}
}

func TestRequireRelativePathRejectsEscapingModuleRoot(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run()
  mod = require("relative/escape")
  mod.run()
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatalf("expected module root escape error")
	} else if !strings.Contains(err.Error(), "escapes module root") {
		t.Fatalf("unexpected error: %v", err)
	}
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
	script, err := engine.Compile(`def run()
  mod = require("entry")
  mod.run()
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatalf("expected symlink escape error")
	} else if !strings.Contains(err.Error(), "escapes module root") {
		t.Fatalf("unexpected error: %v", err)
	}
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
	script, err := engine.Compile(`def run()
  require("link/secret")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatalf("expected symlink escape error")
	} else if !strings.Contains(err.Error(), "escapes module root") {
		t.Fatalf("unexpected error: %v", err)
	}
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
	script, err := engine.Compile(`def run()
  require("link/secret")
  entry = require("entry")
  entry.run()
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatalf("expected module root escape error")
	} else if !strings.Contains(err.Error(), "escapes module root") {
		t.Fatalf("unexpected error: %v", err)
	}
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
	script, err := engine.Compile(`def run()
  mod = require("entry")
  mod.run()
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

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
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def host_each()
  yield()
end

def run()
  mod = require("block_host_yield")
  mod.run()
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 33 {
		t.Fatalf("expected 33, got %#v", result)
	}
}

func TestRequireExportsOnlyPublicFunctions(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run(value)
  mod = require("private_exports")
  if mod["_internal"] != nil
    0
  else
    visible(value) + mod.call_internal(value)
  end
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", []Value{NewInt(2)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 105 {
		t.Fatalf("expected 105, got %#v", result)
	}
}

func TestRequireSupportsExplicitExportControls(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run(value)
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
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", []Value{NewInt(3)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindHash {
		t.Fatalf("expected hash result, got %#v", result)
	}
	out := result.Hash()
	if !out["has_exposed"].Bool() || !out["has_explicit_hidden"].Bool() {
		t.Fatalf("expected explicit exports to be present, got %#v", out)
	}
	if out["has_helper"].Bool() || out["has_internal"].Bool() {
		t.Fatalf("expected non-exported helpers to be hidden, got %#v", out)
	}
	if out["exposed"].Kind() != KindInt || out["exposed"].Int() != 10 {
		t.Fatalf("expected exposed(3)=10, got %#v", out["exposed"])
	}
	if out["explicit_hidden"].Kind() != KindInt || out["explicit_hidden"].Int() != 103 {
		t.Fatalf("expected _explicit_hidden(3)=103, got %#v", out["explicit_hidden"])
	}
}

func TestRequireNonExportedFunctionsAreNotInjectedAsGlobalsWhenUsingExplicitExports(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run(value)
  require("explicit_exports")
  helper(value)
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := script.Call(context.Background(), "run", []Value{NewInt(2)}, CallOptions{}); err == nil {
		t.Fatalf("expected undefined helper error")
	} else if !strings.Contains(err.Error(), "undefined variable helper") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExportKeywordValidation(t *testing.T) {
	engine := MustNewEngine(Config{})

	_, err := engine.Compile(`export helper`)
	if err == nil || !strings.Contains(err.Error(), "expected 'def'") {
		t.Fatalf("expected export def parse error, got %v", err)
	}

	_, err = engine.Compile(`class Example
  export def value()
    1
  end
end`)
	if err == nil || !strings.Contains(err.Error(), "export is only supported for top-level functions") {
		t.Fatalf("expected top-level export parse error, got %v", err)
	}

	_, err = engine.Compile(`def outer()
  if true
    export def nested()
      1
    end
  end
end`)
	if err == nil || !strings.Contains(err.Error(), "export is only supported for top-level functions") {
		t.Fatalf("expected nested export parse error, got %v", err)
	}
}

func TestRequirePrivateFunctionsAreNotInjectedAsGlobals(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run(value)
  require("private_exports")
  _internal(value)
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := script.Call(context.Background(), "run", []Value{NewInt(2)}, CallOptions{}); err == nil {
		t.Fatalf("expected undefined private function error")
	} else if !strings.Contains(err.Error(), "undefined variable _internal") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRequireModuleCacheAvoidsDuplicateLoads(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run()
  require("circular_a")
  require("circular_b")
  "ok"
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.String() != "ok" {
		t.Fatalf("expected ok, got %#v", result)
	}
}

func TestRequireRuntimeModuleRecursionHitsRecursionLimit(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run()
  mod = require("circular_runtime_a")
  mod.enter()
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatalf("expected recursion limit error")
	} else if !strings.Contains(err.Error(), "recursion depth exceeded") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRequireAllowsCachedModuleReuseAcrossModuleCalls(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run()
  mod = require("require_cached_a")
  mod.start()
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 21 {
		t.Fatalf("expected 21, got %#v", result)
	}
}

func TestRequireConcurrentLoading(t *testing.T) {
	engine := MustNewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run()
  require("helper")
  double(5)
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

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

	script, err := engine.Compile(`def run()
  require("helper")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{})
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

	script, err := engine.Compile(`def run(v)
  helpers = require("helper")
  helpers.triple(v)
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

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

	script, err := engine.Compile(`def run_allowed(value)
  mod = require("shared/math")
  mod.double(value)
end

def run_denied(value)
  mod = require("helper")
  mod.double(value)
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

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

func TestRequireModuleDenyListOverridesAllowList(t *testing.T) {
	engine := MustNewEngine(Config{
		ModulePaths:     []string{filepath.Join("testdata", "modules")},
		ModuleAllowList: []string{"*"},
		ModuleDenyList:  []string{"helper"},
	})

	script, err := engine.Compile(`def run()
  require("helper")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatalf("expected deny-list error")
	} else if !strings.Contains(err.Error(), `require: module "helper" denied by policy`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestModulePolicyPatternValidation(t *testing.T) {
	_, err := NewEngine(Config{
		ModulePaths:     []string{filepath.Join("testdata", "modules")},
		ModuleAllowList: []string{"[invalid"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid module allow-list pattern") {
		t.Fatalf("expected invalid allow-list pattern error, got %v", err)
	}
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
