package vibes

import (
	"context"
	"strings"
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
	engine := NewEngine(Config{StrictEffects: true})
	script, err := engine.Compile(`def run()
  db.save("player-1")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	called := false
	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Globals: map[string]Value{
			"db": NewObject(map[string]Value{
				"save": NewBuiltin("db.save", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
					called = true
					return NewNil(), nil
				}),
			}),
		},
	})
	if err == nil {
		t.Fatalf("expected strict effects global validation error")
	}
	if got := err.Error(); !strings.Contains(got, "strict effects: global db must be data-only") {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatalf("callable global should not execute when strict validation fails")
	}
}

func TestStrictEffectsAllowsDataGlobals(t *testing.T) {
	engine := NewEngine(Config{StrictEffects: true})
	script, err := engine.Compile(`def run()
  tenant
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Globals: map[string]Value{
			"tenant": NewString("acme"),
		},
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindString || result.String() != "acme" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestStrictEffectsAllowsCapabilities(t *testing.T) {
	engine := NewEngine(Config{StrictEffects: true})
	script, err := engine.Compile(`def run()
  db.save("player-1")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	called := false
	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			strictEffectsCapability{called: &called},
		},
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindString || result.String() != "saved" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !called {
		t.Fatalf("capability method was not invoked")
	}
}
