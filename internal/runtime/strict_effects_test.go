package runtime

import (
	"context"
	"slices"
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

// TestBoundaryClonesPreserveTypedHashOrder pins that clones keep Ruby-style
// insertion order. The typed entry map ranges arbitrarily, so a clone
// rebuilt from that order would record it and iterate differently from the
// hash it was copied from.
func TestBoundaryClonesPreserveTypedHashOrder(t *testing.T) {
	t.Parallel()

	build := func() Value {
		h := NewHashWithCapacity(8)
		for _, name := range []string{"zeta", "alpha", "mike", "bravo", "yankee", "delta", "kilo", "echo"} {
			if err := hashSet(h, NewSymbol(name), NewInt(1)); err != nil {
				t.Fatalf("HashSet %s: %v", name, err)
			}
		}
		return h
	}
	order := func(v Value) []string {
		entries := v.HashEntries()
		out := make([]string, 0, len(entries))
		for _, entry := range entries {
			out = append(out, entry.Key.String())
		}
		return out
	}

	source := build()
	want := order(source)

	cloned, err := cloneCapabilityDataOnlyValue("payload", source)
	if err != nil {
		t.Fatalf("cloning failed: %v", err)
	}
	if got := order(cloned); !slices.Equal(got, want) {
		t.Fatalf("capability clone iterates %v, want the source order %v", got, want)
	}

	// The host clone only engages for a graph that needs cloning, so give the
	// hash a value that forces it.
	hostSource := build()
	if err := hashSet(hostSource, NewSymbol("fn"), NewBuiltin("probe", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
		return NewNil(), nil
	})); err != nil {
		t.Fatalf("HashSet fn: %v", err)
	}
	hostWant := order(hostSource)
	if got := order(cloneValueForHost(hostSource)); !slices.Equal(got, hostWant) {
		t.Fatalf("host clone iterates %v, want the source order %v", got, hostWant)
	}
}
