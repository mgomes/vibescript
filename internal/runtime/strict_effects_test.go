package runtime

import (
	"context"
	"testing"
)

type strictEffectsCapability struct {
	called *bool
}

func (c strictEffectsCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"db": NewObject(map[string]Value{
			"save": NewBuiltin("db.save", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				*c.called = true
				return NewString("saved"), nil
			}),
		}),
	}, nil
}

func TestStrictEffectsRejectsCallableGlobals(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StrictEffects: true}, `def run()
  db.save("player-1")
end`)

	called := false
	err := callScriptErr(t, context.Background(), script, "run", nil, CallOptions{
		Globals: map[string]Value{
			"db": NewObject(map[string]Value{
				"save": NewBuiltin("db.save", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
					called = true
					return NewNil(), nil
				}),
			}),
		},
	})
	requireErrorContains(t, err, "strict effects: global db must be data-only")
	if called {
		t.Fatalf("callable global should not execute when strict validation fails")
	}
}

func TestStrictEffectsAllowsDataGlobals(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StrictEffects: true}, `def run()
  tenant
end`)

	result := callScript(t, context.Background(), script, "run", nil, CallOptions{
		Globals: map[string]Value{
			"tenant": NewString("acme"),
		},
	})
	if result.Kind() != KindString || result.String() != "acme" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestStrictEffectsRejectsCyclicGlobals(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StrictEffects: true}, `def run()
  payload
end`)

	entries := map[string]Value{}
	cyclic := NewHash(entries)
	entries["self"] = cyclic

	err := callScriptErr(t, context.Background(), script, "run", nil, CallOptions{
		Globals: map[string]Value{"payload": cyclic},
	})
	requireErrorContains(t, err, "strict effects: global payload must be data-only")
}

func TestStrictEffectsAllowsCapabilities(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StrictEffects: true}, `def run()
  db.save("player-1")
end`)

	called := false
	result := callScript(t, context.Background(), script, "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			strictEffectsCapability{called: &called},
		},
	})
	if result.Kind() != KindString || result.String() != "saved" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !called {
		t.Fatalf("capability method was not invoked")
	}
}

// TestStrictGlobalsScannerRejectsHashDefaults pins that the strict-globals
// callable scan reaches a hash's Ruby-style default metadata. A default proc is
// a script block, and a default value can nest callables; both must be rejected
// so a Hash.new { ... } global is not admitted as an empty, callable-free hash.
func TestStrictGlobalsScannerRejectsHashDefaults(t *testing.T) {
	t.Parallel()

	procDefault := NewHashWithDefault(map[string]Value{}, NewNil(), NewBlock(nil, nil, newEnv(nil)))
	callableValueDefault := NewHashWithDefault(
		map[string]Value{},
		NewArray([]Value{NewBuiltin("x", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
			return NewNil(), nil
		})}),
		NewNil(),
	)
	dataDefault := NewHashWithDefault(map[string]Value{"k": NewInt(1)}, NewInt(0), NewNil())

	tests := []struct {
		name    string
		global  Value
		wantErr bool
	}{
		{name: "default_proc", global: procDefault, wantErr: true},
		{name: "callable_default_value", global: callableValueDefault, wantErr: true},
		{name: "data_only_default_value", global: dataDefault, wantErr: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateStrictGlobals(map[string]Value{"g": tc.global})
			if tc.wantErr && err == nil {
				t.Fatalf("validateStrictGlobals(%s) = nil, want a data-only error", tc.name)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateStrictGlobals(%s) = %v, want nil", tc.name, err)
			}
		})
	}
}

// TestStrictEffectsRejectsHashDefaultProcGlobal proves the rejection holds
// end-to-end: a strict-effects script handed a global hash carrying a default
// proc fails validation before any code runs.
func TestStrictEffectsRejectsHashDefaultProcGlobal(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StrictEffects: true}, `def run()
  cache[:x]
end`)

	err := callScriptErr(t, context.Background(), script, "run", nil, CallOptions{
		Globals: map[string]Value{
			"cache": NewHashWithDefault(map[string]Value{}, NewNil(), NewBlock(nil, nil, newEnv(nil))),
		},
	})
	requireErrorContains(t, err, "strict effects: global cache must be data-only")
}

// TestCapabilityDataOnlyRejectsHashDefaultProc proves the capability-boundary
// callable scan also reaches a hash default proc.
func TestCapabilityDataOnlyRejectsHashDefaultProc(t *testing.T) {
	t.Parallel()

	procDefault := NewHashWithDefault(map[string]Value{}, NewNil(), NewBlock(nil, nil, newEnv(nil)))
	if err := validateCapabilityDataOnlyValue("payload", procDefault); err == nil {
		t.Fatal("validateCapabilityDataOnlyValue with a default proc = nil, want data-only error")
	}

	dataDefault := NewHashWithDefault(map[string]Value{"k": NewInt(1)}, NewInt(0), NewNil())
	if err := validateCapabilityDataOnlyValue("payload", dataDefault); err != nil {
		t.Fatalf("validateCapabilityDataOnlyValue with a data-only default = %v, want nil", err)
	}
}

// TestDataOnlyScanSharedEntryMapDistinctDefaults pins that two hash wrappers
// sharing one entry map are scanned independently. The callable-existence scans
// used to key their seen-set on the entry-map pointer alone, so visiting a plain
// wrapper first marked the shared map seen and a second wrapper carrying a
// callable default slipped past data-only/strict validation. The seen identity
// must cover the whole hash wrapper (entries plus defaults), so the second
// wrapper is still rejected.
func TestDataOnlyScanSharedEntryMapDistinctDefaults(t *testing.T) {
	t.Parallel()

	sharedEntries := map[string]Value{"k": NewInt(1)}
	plain := NewHashWithDefault(sharedEntries, NewInt(0), NewNil())
	withCallableDefault := NewHashWithDefault(
		sharedEntries,
		NewNil(),
		NewBlock(nil, nil, newEnv(nil)),
	)
	// Order matters: the plain wrapper is scanned first so it marks the shared
	// entry map seen before the callable-carrying wrapper is reached.
	container := NewArray([]Value{plain, withCallableDefault})

	t.Run("capability data-only boundary", func(t *testing.T) {
		t.Parallel()
		if err := validateCapabilityDataOnlyValue("payload", container); err == nil {
			t.Fatal("validateCapabilityDataOnlyValue admitted a callable default behind a shared entry map")
		}
	})

	t.Run("strict globals scan", func(t *testing.T) {
		t.Parallel()
		if err := validateStrictGlobals(map[string]Value{"g": container}); err == nil {
			t.Fatal("validateStrictGlobals admitted a callable default behind a shared entry map")
		}
	})
}
