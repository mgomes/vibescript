package vibes_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/vibes"
	"github.com/mgomes/vibescript/vibes/value"
)

func TestNewEngineRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tempFile := filepath.Join(tempDir, "not_a_dir.vibe")
	if err := os.WriteFile(tempFile, []byte("def f()\nend\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		cfg     vibes.Config
		wantErr string
	}{
		{
			name:    "negative_max_source_bytes",
			cfg:     vibes.Config{MaxSourceBytes: -1},
			wantErr: "vibes: max source bytes cannot be negative",
		},
		{
			name:    "default_task_concurrency_exceeds_max",
			cfg:     vibes.Config{DefaultTaskConcurrency: 8, MaxTaskConcurrency: 2},
			wantErr: "vibes: default task concurrency cannot exceed max task concurrency",
		},
		{
			name:    "empty_module_path",
			cfg:     vibes.Config{ModulePaths: []string{"  "}},
			wantErr: "vibes: module path cannot be empty",
		},
		{
			name:    "module_path_missing",
			cfg:     vibes.Config{ModulePaths: []string{filepath.Join(tempDir, "missing")}},
			wantErr: "vibes: invalid module path",
		},
		{
			name:    "module_path_is_file",
			cfg:     vibes.Config{ModulePaths: []string{tempFile}},
			wantErr: "is not a directory",
		},
		{
			name:    "invalid_allow_pattern",
			cfg:     vibes.Config{ModuleAllowList: []string{"["}},
			wantErr: `vibes: invalid module allow-list pattern "[": syntax error in pattern`,
		},
		{
			name:    "empty_allow_pattern",
			cfg:     vibes.Config{ModuleAllowList: []string{""}},
			wantErr: "vibes: module allow-list pattern cannot be empty",
		},
		{
			name:    "invalid_deny_pattern",
			cfg:     vibes.Config{ModuleDenyList: []string{"["}},
			wantErr: `vibes: invalid module deny-list pattern "[": syntax error in pattern`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine, err := vibes.NewEngine(tc.cfg)
			if err == nil {
				t.Fatalf("NewEngine(%+v): expected error containing %q, got nil", tc.cfg, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("NewEngine error = %q, want containing %q", err, tc.wantErr)
			}
			if engine != nil {
				t.Fatalf("NewEngine returned non-nil engine alongside error %q", err)
			}
		})
	}
}

func TestMustNewEnginePanicsOnInvalidConfig(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("MustNewEngine with invalid config: expected panic, got none")
		}
		err, ok := r.(error)
		if !ok {
			t.Fatalf("panic value = %T, want error", r)
		}
		want := "vibes: max source bytes cannot be negative"
		if err.Error() != want {
			t.Fatalf("panic error = %q, want %q", err, want)
		}
	}()
	vibes.MustNewEngine(vibes.Config{MaxSourceBytes: -1})
}

func TestEngineConfigSummaryDefaults(t *testing.T) {
	t.Parallel()

	engine := vibes.MustNewEngine(vibes.Config{})
	want := "steps=50000 memory=65536B recursion=64 strict_effects=false tasks=4/64"
	if got := engine.ConfigSummary(); got != want {
		t.Fatalf("ConfigSummary() = %q, want %q", got, want)
	}
}

func TestEngineCompileErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     vibes.Config
		source  string
		wantErr string
	}{
		{
			name:    "parse_error",
			source:  "def f(",
			wantErr: "parse error at 1:6: expected parameter name",
		},
		{
			name:    "source_size_limit",
			cfg:     vibes.Config{MaxSourceBytes: 16},
			source:  "def long_name()\nend\n",
			wantErr: "source exceeds maximum size (20 > 16 bytes)",
		},
		{
			name:    "duplicate_function",
			source:  "def f()\nend\ndef f()\nend",
			wantErr: "duplicate function f",
		},
		{
			name:    "unsupported_top_level_statement",
			source:  "x = 1",
			wantErr: "unsupported top-level statement",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine := vibes.MustNewEngine(tc.cfg)
			script, err := engine.Compile(tc.source)
			if err == nil {
				t.Fatalf("Compile(%q): expected error containing %q, got nil", tc.source, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Compile error = %q, want containing %q", err, tc.wantErr)
			}
			if script != nil {
				t.Fatalf("Compile returned non-nil script alongside error %q", err)
			}
		})
	}
}

func TestEngineCompileExposesFunctions(t *testing.T) {
	t.Parallel()

	engine := vibes.MustNewEngine(vibes.Config{})
	script, err := engine.Compile("def beta()\nend\ndef alpha()\nend")
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := script.Function("alpha"); !ok {
		t.Error(`Function("alpha") not found`)
	}
	if fn, ok := script.Function("missing"); ok || fn != nil {
		t.Errorf(`Function("missing") = %v, %t; want nil, false`, fn, ok)
	}

	fns := script.Functions()
	if len(fns) != 2 || fns[0].Name != "alpha" || fns[1].Name != "beta" {
		names := make([]string, len(fns))
		for i, fn := range fns {
			names[i] = fn.Name
		}
		t.Errorf("Functions() order = %v, want [alpha beta]", names)
	}
}

func TestScriptCall(t *testing.T) {
	t.Parallel()

	engine := vibes.MustNewEngine(vibes.Config{})
	script, err := engine.Compile("def add(a, b)\n  a + b\nend")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("happy_path", func(t *testing.T) {
		t.Parallel()
		result, err := script.Call(
			context.Background(),
			"add",
			[]value.Value{value.NewInt(2), value.NewInt(40)},
			vibes.CallOptions{},
		)
		if err != nil {
			t.Fatalf("Call(add) error: %v", err)
		}
		if result.Kind() != value.KindInt || result.Int() != 42 {
			t.Fatalf("Call(add) = %s (%s), want 42 (int)", result, result.Kind())
		}
	})

	t.Run("nil_context", func(t *testing.T) {
		t.Parallel()
		//nolint:staticcheck // pins the documented fallback to context.Background on nil
		result, err := script.Call(nil, "add", []value.Value{value.NewInt(1), value.NewInt(1)}, vibes.CallOptions{})
		if err != nil {
			t.Fatalf("Call with nil context error: %v", err)
		}
		if result.Int() != 2 {
			t.Fatalf("Call with nil context = %s, want 2", result)
		}
	})

	t.Run("unknown_function", func(t *testing.T) {
		t.Parallel()
		result, err := script.Call(context.Background(), "missing", nil, vibes.CallOptions{})
		if err == nil {
			t.Fatal("Call(missing): expected error, got nil")
		}
		if got, want := err.Error(), "function missing not found"; got != want {
			t.Fatalf("Call(missing) error = %q, want %q", got, want)
		}
		if !result.IsNil() {
			t.Fatalf("Call(missing) result = %s, want nil value", result)
		}
	})
}

func TestScriptCallRuntimeErrorSurface(t *testing.T) {
	t.Parallel()

	engine := vibes.MustNewEngine(vibes.Config{})
	script, err := engine.Compile("def boom()\n  1 / 0\nend")
	if err != nil {
		t.Fatal(err)
	}

	_, err = script.Call(context.Background(), "boom", nil, vibes.CallOptions{})
	if err == nil {
		t.Fatal("Call(boom): expected runtime error, got nil")
	}

	var runtimeErr *vibes.RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("Call(boom) error type = %T, want *vibes.RuntimeError", err)
	}
	if runtimeErr.Type != "ZeroDivisionError" {
		t.Errorf("RuntimeError.Type = %q, want %q", runtimeErr.Type, "ZeroDivisionError")
	}
	if runtimeErr.Message != "division by zero" {
		t.Errorf("RuntimeError.Message = %q, want %q", runtimeErr.Message, "division by zero")
	}
	if len(runtimeErr.Frames) == 0 {
		t.Fatal("RuntimeError.Frames is empty")
	}
	frame := runtimeErr.Frames[0]
	if frame.Function != "boom" {
		t.Errorf("Frames[0].Function = %q, want %q", frame.Function, "boom")
	}
	wantPos := vibes.Position{Line: 2, Column: 5}
	if frame.Pos != wantPos {
		t.Errorf("Frames[0].Pos = %+v, want %+v", frame.Pos, wantPos)
	}
	if rendered := err.Error(); !strings.Contains(rendered, "at boom (2:5)") {
		t.Errorf("rendered error %q missing stack frame line %q", rendered, "at boom (2:5)")
	}
	if runtimeErr.Unwrap() != nil {
		t.Errorf("RuntimeError.Unwrap() = %v, want nil", runtimeErr.Unwrap())
	}
}

func TestScriptCallLimitErrorType(t *testing.T) {
	t.Parallel()

	engine := vibes.MustNewEngine(vibes.Config{StepQuota: 8})
	script, err := engine.Compile("def spin()\n  while true\n  end\nend")
	if err != nil {
		t.Fatal(err)
	}

	_, err = script.Call(context.Background(), "spin", nil, vibes.CallOptions{})
	if err == nil {
		t.Fatal("Call(spin): expected limit error, got nil")
	}

	var runtimeErr *vibes.RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("Call(spin) error type = %T, want *vibes.RuntimeError", err)
	}
	if runtimeErr.Type != "LimitError" {
		t.Errorf("RuntimeError.Type = %q, want %q", runtimeErr.Type, "LimitError")
	}
}

func TestEngineExecute(t *testing.T) {
	t.Parallel()

	engine := vibes.MustNewEngine(vibes.Config{})

	t.Run("valid_source_nil_context", func(t *testing.T) {
		t.Parallel()
		//nolint:staticcheck // pins the documented fallback to context.Background on nil
		if err := engine.Execute(nil, "def noop()\nend"); err != nil {
			t.Fatalf("Execute(valid) error: %v", err)
		}
	})

	t.Run("parse_error", func(t *testing.T) {
		t.Parallel()
		err := engine.Execute(context.Background(), "def f(")
		if err == nil || !strings.Contains(err.Error(), "parse error") {
			t.Fatalf("Execute(invalid) error = %v, want parse error", err)
		}
	})

	t.Run("canceled_context", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := engine.Execute(ctx, "def noop()\nend"); !errors.Is(err, context.Canceled) {
			t.Fatalf("Execute(canceled ctx) error = %v, want context.Canceled", err)
		}
	})
}

func TestEngineBuiltinsReturnsIsolatedSnapshot(t *testing.T) {
	t.Parallel()

	engine := vibes.MustNewEngine(vibes.Config{})
	first := engine.Builtins()
	for _, name := range []string{"assert", "money", "now", "JSON", "Time", "Duration"} {
		if _, ok := first[name]; !ok {
			t.Errorf("Builtins() missing %q", name)
		}
	}

	delete(first, "assert")
	second := engine.Builtins()
	if _, ok := second["assert"]; !ok {
		t.Error("mutating the Builtins() snapshot leaked into the engine")
	}
}

func TestNewBuiltinPayloads(t *testing.T) {
	t.Parallel()

	fn := func(_ *vibes.Execution, _ value.Value, _ []value.Value, _ map[string]value.Value, _ value.Value) (value.Value, error) {
		return value.NewNil(), nil
	}

	tests := []struct {
		name           string
		val            value.Value
		wantName       string
		wantAutoInvoke bool
	}{
		{name: "builtin", val: vibes.NewBuiltin("greet", fn), wantName: "greet", wantAutoInvoke: false},
		{name: "auto_builtin", val: vibes.NewAutoBuiltin("tenant", fn), wantName: "tenant", wantAutoInvoke: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.val.Kind() != value.KindBuiltin {
				t.Fatalf("Kind() = %s, want builtin", tc.val.Kind())
			}
			builtin, ok := tc.val.Data().(*vibes.Builtin)
			if !ok {
				t.Fatalf("Data() type = %T, want *vibes.Builtin", tc.val.Data())
			}
			if builtin.Name != tc.wantName {
				t.Errorf("Builtin.Name = %q, want %q", builtin.Name, tc.wantName)
			}
			if builtin.AutoInvoke != tc.wantAutoInvoke {
				t.Errorf("Builtin.AutoInvoke = %t, want %t", builtin.AutoInvoke, tc.wantAutoInvoke)
			}
			if tc.val.Builtin() == nil {
				t.Error("Value.Builtin() = nil, want payload")
			}
		})
	}
}
