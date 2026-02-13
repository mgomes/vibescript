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

	for i := 0; i < goroutines; i++ {
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

	for i := 0; i < goroutines; i++ {
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
