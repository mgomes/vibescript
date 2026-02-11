package vibes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequireProvidesExports(t *testing.T) {
	engine := NewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

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
	engine := NewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

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
	engine := NewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

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
	engine := NewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

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
	engine := NewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

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
	engine := NewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

	script, err := engine.Compile(`def run()
  require("../../etc/passwd")
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

func TestRequireModuleCachePreventsRecursion(t *testing.T) {
	engine := NewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

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

func TestRequireConcurrentLoading(t *testing.T) {
	engine := NewEngine(Config{ModulePaths: []string{filepath.Join("testdata", "modules")}})

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
	engine := NewEngine(Config{
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
	engine := NewEngine(Config{
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
