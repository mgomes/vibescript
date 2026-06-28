package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestTaskResultCloneDoesNotMaskCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	script := compileScriptDefault(t, `def return_callable(_item)
  cancel_now()
  leak
end

def run()
  Tasks.map([1], max: 1, with: :return_callable)
end`)
	cancelBuiltin := NewBuiltin("cancel_now", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		cancel()
		return NewNil(), nil
	})
	leakBuiltin := NewBuiltin("leak", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewNil(), nil
	})
	_, err := script.Call(ctx, "run", nil, CallOptions{
		Globals: map[string]Value{
			"cancel_now": cancelBuiltin,
			"leak":       leakBuiltin,
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Script.Call(canceled during task result clone) error = %v, want context.Canceled", err)
	}
}

func TestScriptCallChecksCanceledContextBeforeBindingGlobals(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StrictEffects: true}, `def run()
  unsafe.save("player-1")
end`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := script.Call(ctx, "run", nil, CallOptions{
		Globals: map[string]Value{
			"unsafe": NewObject(map[string]Value{
				"save": NewBuiltin("unsafe.save", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
					return NewNil(), nil
				}),
			}),
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Script.Call(canceled before global binding) error = %v, want context.Canceled", err)
	}
}

func TestScriptCallChecksCanceledContextBeforeSetupMemoryQuota(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 1}, `def run()
  1
end`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := script.Call(ctx, "run", nil, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Script.Call(canceled before setup memory quota) error = %v, want context.Canceled", err)
	}
}

func TestScriptCallChecksContextBeforeHostClone(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	engine := MustNewEngine(Config{MemoryQuotaBytes: 4 << 20})
	engine.builtins["cancel_now"] = NewBuiltin("cancel_now", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		cancel()
		return NewNil(), nil
	})
	script := compileScriptWithEngine(t, engine, `def run()
  cancel_now()
  payload
end`)

	payload := NewBuiltin("payload", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewString("host"), nil
	})
	_, err := script.Call(ctx, "run", nil, CallOptions{
		Globals: map[string]Value{"payload": payload},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Script.Call(canceled before host clone) error = %v, want context.Canceled", err)
	}
}

func TestScriptCallChecksContextBeforeSuccessfulReturn(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	engine := MustNewEngine(Config{})
	engine.builtins["cancel_now"] = NewBuiltin("cancel_now", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		cancel()
		return NewNil(), nil
	})
	script := compileScriptWithEngine(t, engine, `def run()
  cancel_now()
  "done"
end`)

	_, err := script.Call(ctx, "run", nil, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Script.Call(canceled before successful return) error = %v, want context.Canceled", err)
	}
}

func TestCallStopsBeforeCalleeWhenArgumentCancelsContext(t *testing.T) {
	t.Parallel()

	var cancel context.CancelFunc
	probeCalled := false
	engine := MustNewEngine(Config{})
	engine.builtins["cancel_now"] = NewBuiltin("cancel_now", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		cancel()
		return NewInt(1), nil
	})
	engine.builtins["probe"] = NewBuiltin("probe", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		probeCalled = true
		return NewString("ok"), nil
	})
	script := compileScriptWithEngine(t, engine, `class Probe
  def call(value)
    probe(value)
  end
end

def run_builtin()
  probe(cancel_now())
end

def run_member()
  Probe.new.call(cancel_now())
end`)

	tests := []struct {
		name string
		fn   string
	}{
		{name: "builtin", fn: "run_builtin"},
		{name: "member", fn: "run_member"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancelFunc := context.WithCancel(context.Background())
			cancel = cancelFunc
			probeCalled = false
			_, err := script.Call(ctx, tc.fn, nil, CallOptions{})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Script.Call(%s) error = %v, want context.Canceled", tc.fn, err)
			}
			if probeCalled {
				t.Fatalf("probe builtin was called after %s argument canceled context", tc.fn)
			}
		})
	}
}

func TestBlockIteratorsStopWhenBlockCancelsContext(t *testing.T) {
	t.Parallel()

	var cancel context.CancelFunc
	calls := 0
	engine := MustNewEngine(Config{})
	engine.builtins["cancel_after_first"] = NewBuiltin("cancel_after_first", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
		calls++
		cancel()
		return args[0], nil
	})
	script := compileScriptWithEngine(t, engine, `def run_map()
  [1, 2, 3].map do |n|
    cancel_after_first(n)
  end
end

def run_select()
  [1, 2, 3].select do |n|
    cancel_after_first(n)
  end
end

def run_times()
  3.times do |n|
    cancel_after_first(n)
  end
end`)

	tests := []struct {
		name string
		fn   string
	}{
		{name: "array_map", fn: "run_map"},
		{name: "array_select", fn: "run_select"},
		{name: "int_times", fn: "run_times"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancelFunc := context.WithCancel(context.Background())
			cancel = cancelFunc
			calls = 0
			_, err := script.Call(ctx, tc.fn, nil, CallOptions{})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Script.Call(%s) error = %v, want context.Canceled", tc.fn, err)
			}
			if calls != 1 {
				t.Fatalf("%s cancel_after_first call count = %d, want 1", tc.fn, calls)
			}
		})
	}
}

func TestClassBodyCancellationStopsBeforeRunFunction(t *testing.T) {
	t.Parallel()

	var cancel context.CancelFunc
	probeCalled := false
	engine := MustNewEngine(Config{})
	engine.builtins["cancel_now"] = NewBuiltin("cancel_now", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		cancel()
		return NewNil(), nil
	})
	engine.builtins["probe"] = NewBuiltin("probe", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		probeCalled = true
		return NewString("ok"), nil
	})
	script := compileScriptWithEngine(t, engine, `class Setup
  cancel_now()
end

def run()
  probe()
end`)

	ctx, cancelFunc := context.WithCancel(context.Background())
	cancel = cancelFunc
	_, err := script.Call(ctx, "run", nil, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Script.Call(canceled inside class body) error = %v, want context.Canceled", err)
	}
	if probeCalled {
		t.Fatalf("probe builtin was called after class body canceled context")
	}
}

func TestModuleInitializerCancellationStopsRequireCaller(t *testing.T) {
	t.Parallel()

	var cancel context.CancelFunc
	probeCalled := false
	moduleRoot := tempModuleTree(t, moduleFile{
		path: "canceling.vibe",
		content: `cancel_now()

def value()
  1
end
`,
	})

	engine := MustNewEngine(Config{ModulePaths: []string{moduleRoot}})
	engine.builtins["cancel_now"] = NewBuiltin("cancel_now", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		cancel()
		return NewNil(), nil
	})
	engine.builtins["probe"] = NewBuiltin("probe", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		probeCalled = true
		return NewString("ok"), nil
	})
	script := compileScriptWithEngine(t, engine, `def run()
  require("canceling")
  probe()
end`)

	ctx, cancelFunc := context.WithCancel(context.Background())
	cancel = cancelFunc
	_, err := script.Call(ctx, "run", nil, CallOptions{AllowRequire: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Script.Call(canceled inside module initializer) error = %v, want context.Canceled", err)
	}
	if probeCalled {
		t.Fatalf("probe builtin was called after module initializer canceled context")
	}
}
