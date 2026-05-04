package vibes

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNewEngineRejectsMissingModulePath(t *testing.T) {
	_, err := NewEngine(Config{ModulePaths: []string{"./definitely-missing-mod-path"}})
	if err == nil {
		t.Fatalf("expected NewEngine to reject missing module path")
	}
	requireErrorContains(t, err, "invalid module path")
}

func TestNewEngineRejectsFileModulePath(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "module.vibe")
	if writeErr := os.WriteFile(filePath, []byte("def run\n  1\nend"), 0o644); writeErr != nil {
		t.Fatalf("write temp file: %v", writeErr)
	}

	_, err := NewEngine(Config{ModulePaths: []string{filePath}})
	if err == nil {
		t.Fatalf("expected NewEngine to reject file module path")
	}
	requireErrorContains(t, err, "is not a directory")
}

func TestNewEngineAcceptsValidModulePaths(t *testing.T) {
	dir := t.TempDir()
	engine, err := NewEngine(Config{ModulePaths: []string{dir}})
	if err != nil {
		t.Fatalf("expected valid module path to succeed: %v", err)
	}
	if engine == nil {
		t.Fatalf("expected non-nil engine")
	}
}

func TestNewEngineNormalizesModulePathsAtCreation(t *testing.T) {
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	root := t.TempDir()
	mods := filepath.Join(root, "mods")
	if err := os.Mkdir(mods, 0o755); err != nil {
		t.Fatalf("mkdir mods: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir root: %v", err)
	}

	engine, err := NewEngine(Config{ModulePaths: []string{"mods"}})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	want, err := filepath.EvalSymlinks(mods)
	if err != nil {
		t.Fatalf("resolve module path: %v", err)
	}
	if got := engine.config.ModulePaths[0]; got != filepath.Clean(want) {
		t.Fatalf("engine.config.ModulePaths[0] = %q, want %q", got, want)
	}
}

func TestNewEngineCopiesConfigSlices(t *testing.T) {
	root := t.TempDir()
	modulePaths := []string{root}
	allowList := []string{"allowed"}
	denyList := []string{"blocked"}

	engine, err := NewEngine(Config{
		ModulePaths:     modulePaths,
		ModuleAllowList: allowList,
		ModuleDenyList:  denyList,
	})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}

	modulePaths[0] = filepath.Join(root, "other")
	allowList[0] = "other"
	denyList[0] = "allowed"

	wantModulePath, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve module path: %v", err)
	}
	if got := engine.config.ModulePaths[0]; got != filepath.Clean(wantModulePath) {
		t.Fatalf("engine.config.ModulePaths[0] = %q, want %q", got, wantModulePath)
	}
	if err := engine.enforceModulePolicy("allowed.vibe"); err != nil {
		t.Fatalf("engine policy changed after caller mutated config slices: %v", err)
	}
}

func TestBuiltinsReturnsIsolatedBuiltinValues(t *testing.T) {
	engine := MustNewEngine(Config{})

	builtins := engine.Builtins()
	assertBuiltin := builtins["assert"].Builtin()
	if assertBuiltin == nil {
		t.Fatalf("Builtins()[assert].Builtin() = nil, want builtin")
	}
	assertBuiltin.Name = "mutated"
	assertBuiltin.AutoInvoke = true
	assertBuiltin.Fn = func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewString("mutated"), nil
	}

	freshAssert := engine.Builtins()["assert"].Builtin()
	if freshAssert == nil {
		t.Fatalf("fresh Builtins()[assert].Builtin() = nil, want builtin")
	}
	if freshAssert.Name != "assert" {
		t.Fatalf("fresh assert builtin name = %q, want %q", freshAssert.Name, "assert")
	}
	if freshAssert.AutoInvoke {
		t.Fatalf("fresh assert builtin AutoInvoke = true, want false")
	}
	if _, err := freshAssert.Fn(nil, NewNil(), []Value{NewBool(false)}, nil, NewNil()); err == nil {
		t.Fatalf("fresh assert builtin returned nil error after caller mutated previous snapshot")
	}
}

func TestBuiltinsReturnsIsolatedObjectValues(t *testing.T) {
	mutatedBuiltin := func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewString("mutated"), nil
	}

	tests := []struct {
		name   string
		mutate func(t *testing.T, methods map[string]Value)
	}{
		{
			name: "method builtin pointer",
			mutate: func(t *testing.T, methods map[string]Value) {
				t.Helper()
				parseBuiltin := methods["parse"].Builtin()
				if parseBuiltin == nil {
					t.Fatalf("Builtins()[JSON].Hash()[parse].Builtin() = nil, want builtin")
				}
				parseBuiltin.Name = "mutated.JSON.parse"
				parseBuiltin.AutoInvoke = true
				parseBuiltin.Fn = mutatedBuiltin
			},
		},
		{
			name: "method table entry",
			mutate: func(t *testing.T, methods map[string]Value) {
				t.Helper()
				methods["parse"] = NewBuiltin("mutated.JSON.parse", mutatedBuiltin)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := MustNewEngine(Config{})

			jsonBuiltin := engine.Builtins()["JSON"]
			if jsonBuiltin.Kind() != KindObject {
				t.Fatalf("Builtins()[JSON].Kind() = %s, want object", jsonBuiltin.Kind())
			}
			tt.mutate(t, jsonBuiltin.Hash())

			freshParse := engine.Builtins()["JSON"].Hash()["parse"].Builtin()
			if freshParse == nil {
				t.Fatalf("fresh Builtins()[JSON].Hash()[parse].Builtin() = nil, want builtin")
			}
			if freshParse.Name != "JSON.parse" {
				t.Fatalf("fresh JSON.parse builtin name = %q, want %q", freshParse.Name, "JSON.parse")
			}
			if freshParse.AutoInvoke {
				t.Fatalf("fresh JSON.parse builtin AutoInvoke = true, want false")
			}

			script, err := engine.Compile(`def parse_name
  JSON.parse("{\"name\":\"alex\"}")[:name]
end`)
			if err != nil {
				t.Fatalf("Compile(parse_name) failed: %v", err)
			}
			result, err := script.Call(context.Background(), "parse_name", nil, CallOptions{})
			if err != nil {
				t.Fatalf("parse_name call failed after caller mutated Builtins()[JSON]: %v", err)
			}
			if !result.Equal(NewString("alex")) {
				t.Fatalf("parse_name after caller mutated Builtins()[JSON] = %#v, want alex", result)
			}
		})
	}
}

func TestCompileAndCallAdd(t *testing.T) {
	script := compileScriptDefault(t, `def add(a, b)
  a + b
end`)

	result, err := script.Call(context.Background(), "add", []Value{NewInt(2), NewInt(3)}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 5 {
		t.Fatalf("expected 5, got %#v", result)
	}
}

func TestMoneyBuiltin(t *testing.T) {
	script := compileScriptDefault(t, `def total
  money("10.00 USD") + money("5.00 USD")
end`)

	result, err := script.Call(context.Background(), "total", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindMoney {
		t.Fatalf("expected money value, got %v", result.Kind())
	}
	money := result.Money()
	if money.Cents() != 1500 || money.Currency() != "USD" {
		t.Fatalf("unexpected money result: %s", money.String())
	}
}

func TestGlobalsAccess(t *testing.T) {
	script := compileScriptDefault(t, `def user_id
  ctx.user.id
end`)

	ctxVal := NewObject(map[string]Value{
		"user": NewObject(map[string]Value{
			"id": NewString("coach-1"),
		}),
	})

	result, err := script.Call(context.Background(), "user_id", nil, CallOptions{Globals: map[string]Value{"ctx": ctxVal}})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindString || result.String() != "coach-1" {
		t.Fatalf("unexpected result: %v", result.String())
	}
}

func TestAssertFailure(t *testing.T) {
	script := compileScriptDefault(t, `def check
  assert false, "boom"
end`)

	if _, err := script.Call(context.Background(), "check", nil, CallOptions{}); err == nil {
		t.Fatalf("expected assertion error")
	}
}

func TestSymbolIndex(t *testing.T) {
	script := compileScriptDefault(t, `def amount(row)
  row[:amount]
end`)

	row := NewHash(map[string]Value{"amount": NewInt(42)})
	result, err := script.Call(context.Background(), "amount", []Value{row}, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 42 {
		t.Fatalf("unexpected result: %v", result)
	}
}

func TestDurationLiteral(t *testing.T) {
	script := compileScriptDefault(t, `def seconds
  (2.minutes).seconds
end`)

	result, err := script.Call(context.Background(), "seconds", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 120 {
		t.Fatalf("expected 120 seconds, got %v", result)
	}
}

func TestZeroArgCallWithoutParens(t *testing.T) {
	script := compileScriptDefault(t, `def helper
  7
end

def run
  helper * 6
end`)

	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 42 {
		t.Fatalf("expected 42, got %v", result)
	}
}

func TestNestedZeroArgCalls(t *testing.T) {
	script := compileScriptDefault(t, `def inner
  10
end

def middle
  inner + 5
end

def outer
  middle * 2
end`)

	result, err := script.Call(context.Background(), "outer", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 30 {
		t.Fatalf("expected 30, got %v", result)
	}
}

func TestMixedZeroArgAndRegularCalls(t *testing.T) {
	script := compileScriptDefault(t, `def zero_arg
  5
end

def with_args(x, y)
  x + y
end

def run
  zero_arg + with_args(10, 20)
end`)

	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 35 {
		t.Fatalf("expected 35, got %v", result)
	}
}

func TestMethodChainingWithZeroArgMethods(t *testing.T) {
	script := compileScriptDefault(t, `def run
  values = [1, 2, 3, 4, 5]
  values.sum
end`)

	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 15 {
		t.Fatalf("expected 15, got %v", result)
	}
}

func TestZeroArgMethodChaining(t *testing.T) {
	script := compileScriptDefault(t, `def run
  values = [1, 2, 2, 3, 3, 3]
  values.uniq.sum
end`)

	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 6 {
		t.Fatalf("expected 6 (1+2+3), got %v", result)
	}
}
