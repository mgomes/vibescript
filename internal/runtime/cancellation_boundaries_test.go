package runtime

import (
	"context"
	"errors"
	"testing"
)

type cancelingBindCapability struct {
	cancel context.CancelFunc
}

func (c cancelingBindCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	c.cancel()
	return nil, errors.New("bind failed")
}

type recordingBindCapability struct {
	called *bool
}

func (c recordingBindCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	*c.called = true
	return map[string]Value{"cap": NewNil()}, nil
}

type argumentCancelProbeCapability struct {
	cancel    context.CancelFunc
	validated *int
	called    *int
}

func (c argumentCancelProbeCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"cancel": NewObject(map[string]Value{
			"now": NewBuiltin("cancel.now", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
				c.cancel()
				return NewInt(1), nil
			}),
		}),
		"probe": NewObject(map[string]Value{
			"call": NewBuiltin("probe.call", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
				(*c.called)++
				return NewString("ok"), nil
			}),
		}),
	}, nil
}

func (c argumentCancelProbeCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"probe.call": {
			ValidateArgs: func(_ []Value, _ map[string]Value, _ Value) error {
				(*c.validated)++
				return nil
			},
		},
	}
}

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

func TestScriptCallChecksCanceledContextBeforeCapabilityBindError(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  1
end`)
	ctx, cancel := context.WithCancel(context.Background())

	_, err := script.Call(ctx, "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{cancelingBindCapability{cancel: cancel}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Script.Call(canceled during capability bind) error = %v, want context.Canceled", err)
	}
}

func TestScriptCallChecksCanceledContextBeforeCapabilityBind(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  1
end`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false

	_, err := script.Call(ctx, "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{recordingBindCapability{called: &called}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Script.Call(canceled before capability bind) error = %v, want context.Canceled", err)
	}
	if called {
		t.Fatal("capability Bind was called after context cancellation")
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

func TestScriptCallChecksContextBeforeReturnValidation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	engine := MustNewEngine(Config{})
	engine.builtins["cancel_now"] = NewBuiltin("cancel_now", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		cancel()
		return NewNil(), nil
	})
	script := compileScriptWithEngine(t, engine, `def run() -> string
  cancel_now()
  123
end`)

	_, err := script.Call(ctx, "run", nil, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Script.Call(canceled before return validation) error = %v, want context.Canceled", err)
	}
}

func TestBuiltinErrorChecksContextBeforeWrapping(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	engine := MustNewEngine(Config{})
	engine.builtins["cancel_and_fail"] = NewBuiltin("cancel_and_fail", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		cancel()
		return NewNil(), errors.New("driver failed")
	})
	script := compileScriptWithEngine(t, engine, `def run()
  cancel_and_fail()
end`)

	_, err := script.Call(ctx, "run", nil, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Script.Call(canceled builtin error) error = %v, want context.Canceled", err)
	}
}

func TestBuiltinReturnChecksContextBeforeMemoryAccounting(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	engine := MustNewEngine(Config{MemoryQuotaBytes: 64 * 1024})
	engine.builtins["cancel_and_return_payload"] = NewBuiltin("cancel_and_return_payload", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		cancel()
		items := make([]Value, 4096)
		for i := range items {
			items[i] = NewString("payload")
		}
		return NewArray(items), nil
	})
	script := compileScriptWithEngine(t, engine, `def run()
  cancel_and_return_payload()
end`)

	_, err := script.Call(ctx, "run", nil, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Script.Call(canceled builtin before memory accounting) error = %v, want context.Canceled", err)
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

func TestCapabilityCallStopsBeforeContractValidationWhenArgumentCancelsContext(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  probe.call(cancel.now())
end`)
	ctx, cancel := context.WithCancel(context.Background())
	validated := 0
	called := 0

	_, err := script.Call(ctx, "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{argumentCancelProbeCapability{
			cancel:    cancel,
			validated: &validated,
			called:    &called,
		}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Script.Call(canceled during capability argument evaluation) error = %v, want context.Canceled", err)
	}
	if validated != 0 {
		t.Fatalf("capability contract validations = %d, want 0", validated)
	}
	if called != 0 {
		t.Fatalf("capability builtin calls = %d, want 0", called)
	}
}

func TestArrayUniqChecksCanceledContextBeforeBlocklessLoop(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	engine := MustNewEngine(Config{MemoryQuotaBytes: 32 << 20})
	engine.builtins["cancel_now"] = NewBuiltin("cancel_now", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		cancel()
		return NewNil(), nil
	})
	script := compileScriptWithEngine(t, engine, `def run(values)
  cancel_now()
  values.uniq
end`)

	values := make([]Value, 300)
	for i := range values {
		values[i] = NewHash(map[string]Value{
			"id":    NewInt(int64(i)),
			"group": NewArray([]Value{NewString("alpha"), NewInt(int64(i % 7))}),
		})
	}

	_, err := script.Call(ctx, "run", []Value{NewArray(values)}, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Array#uniq after cancellation error = %v, want context.Canceled", err)
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
