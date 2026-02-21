package vibes

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

func TestStrictEffectsAllowsCapabilities(t *testing.T) {
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
