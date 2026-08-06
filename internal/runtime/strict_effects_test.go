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

// buildCollidingTypedHash returns a typed hash holding two entries whose
// distinct keys (`:x` and `"x"`) render to the same display key, with the
// callable stored under the symbol key. Value.Hash() collapses the pair into
// one map slot, so a boundary scan reading that view sees only the data entry
// while the callable stays reachable through typed lookup and iteration.
func buildCollidingTypedHash(t *testing.T, callable Value) Value {
	t.Helper()

	h := NewTypedHash(2)
	if err := hashSet(h, NewSymbol("x"), callable); err != nil {
		t.Fatalf("HashSet symbol key: %v", err)
	}
	if err := hashSet(h, NewString("x"), NewInt(0)); err != nil {
		t.Fatalf("HashSet string key: %v", err)
	}
	if h.HashLen() != 2 {
		t.Fatalf("typed hash holds %d entries, want the colliding pair", h.HashLen())
	}
	if len(h.Hash()) != 1 {
		t.Skip("display keys no longer collide; the smuggling shape this pins is gone")
	}
	return h
}

// TestBoundaryScansSeeCollidingTypedEntries pins that the data-only
// boundaries scan typed entries rather than the lossy display-key view. A
// hash carrying a callable under `:x` beside data under `"x"` presents an
// entry map holding only the data, so a scan that walks Value.Hash() clears
// it and the callable crosses a boundary meant to reject callables (#28).
func TestBoundaryScansSeeCollidingTypedEntries(t *testing.T) {
	t.Parallel()

	callable := NewBuiltin("danger", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
		return NewNil(), nil
	})

	t.Run("capability_data_only", func(t *testing.T) {
		t.Parallel()
		smuggled := buildCollidingTypedHash(t, callable)
		if err := validateCapabilityDataOnlyValue("payload", smuggled); err == nil {
			t.Fatal("data-only validation admitted a hash holding a callable under a colliding typed key")
		}
	})

	t.Run("strict_globals", func(t *testing.T) {
		t.Parallel()
		smuggled := buildCollidingTypedHash(t, callable)
		err := validateStrictGlobals(map[string]Value{"payload": smuggled})
		requireErrorContains(t, err, "must be data-only")
	})

	t.Run("nested_in_array", func(t *testing.T) {
		t.Parallel()
		smuggled := NewArray([]Value{buildCollidingTypedHash(t, callable)})
		if err := validateCapabilityDataOnlyValue("payload", smuggled); err == nil {
			t.Fatal("data-only validation admitted a nested hash holding a smuggled callable")
		}
	})

	t.Run("host_clone_detection", func(t *testing.T) {
		t.Parallel()
		smuggled := buildCollidingTypedHash(t, callable)
		if !valueNeedsHostClone(smuggled) {
			t.Fatal("host-clone detection missed a callable held under a colliding typed key")
		}
	})
}

// TestCapabilityDataCloneKeepsCollidingTypedEntries pins that the capability
// data clone rebuilds typed hashes entry by entry: rebuilding from the
// display-key view silently dropped whichever colliding entry lost the map
// slot, changing the data the adapter receives.
func TestCapabilityDataCloneKeepsCollidingTypedEntries(t *testing.T) {
	t.Parallel()

	h := NewTypedHash(2)
	if err := hashSet(h, NewSymbol("x"), NewInt(1)); err != nil {
		t.Fatalf("HashSet symbol key: %v", err)
	}
	if err := hashSet(h, NewString("x"), NewInt(2)); err != nil {
		t.Fatalf("HashSet string key: %v", err)
	}

	cloned, err := cloneCapabilityDataOnlyValue("payload", h)
	if err != nil {
		t.Fatalf("cloning a data-only hash failed: %v", err)
	}
	if cloned.HashLen() != 2 {
		t.Fatalf("clone holds %d entries, want both colliding keys preserved", cloned.HashLen())
	}
	got, ok, err := cloned.HashGet(NewSymbol("x"))
	if err != nil || !ok || got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("clone lost the symbol-keyed entry: %#v (found=%v, err=%v)", got, ok, err)
	}
	got, ok, err = cloned.HashGet(NewString("x"))
	if err != nil || !ok || got.Kind() != KindInt || got.Int() != 2 {
		t.Fatalf("clone lost the string-keyed entry: %#v (found=%v, err=%v)", got, ok, err)
	}
}

// TestCapabilityDataCloneKeepsTypedEntriesWithDefaults pins that attaching
// cloned defaults preserves the typed clone: rebuilding the wrapper from the
// display-key map discarded every entry the typed clone had written into the
// wrapper's typed table, so a populated Hash.new(0) crossed the boundary
// empty.
func TestCapabilityDataCloneKeepsTypedEntriesWithDefaults(t *testing.T) {
	t.Parallel()

	h := NewTypedHash(2)
	if err := hashSet(h, NewSymbol("x"), NewInt(1)); err != nil {
		t.Fatalf("HashSet symbol key: %v", err)
	}
	if err := hashSet(h, NewString("x"), NewInt(2)); err != nil {
		t.Fatalf("HashSet string key: %v", err)
	}
	h.SetHashDefaults(NewInt(0), NewNil())

	cloned, err := cloneCapabilityDataOnlyValue("payload", h)
	if err != nil {
		t.Fatalf("cloning a data-only hash with defaults failed: %v", err)
	}
	if cloned.HashLen() != 2 {
		t.Fatalf("clone holds %d entries, want both entries preserved alongside the default", cloned.HashLen())
	}
	got, ok, err := cloned.HashGet(NewSymbol("x"))
	if err != nil || !ok || got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("clone lost the symbol-keyed entry: %#v (found=%v, err=%v)", got, ok, err)
	}
	if def := hashDefaultValue(cloned); def.Kind() != KindInt || def.Int() != 0 {
		t.Fatalf("clone lost its default value: %#v", def)
	}
}

// TestCapabilityDepthGuardCountsTypedKeys pins that the traversal depth guard
// walks typed keys. Array keys nest arbitrarily and the clone recurses
// through them, so a key nested past the limit must be rejected by the guard
// rather than reached by an unbounded clone.
func TestCapabilityDepthGuardCountsTypedKeys(t *testing.T) {
	t.Parallel()

	key := NewArray([]Value{NewInt(1)})
	for range maxCapabilityDataOnlyDepth + 8 {
		key = NewArray([]Value{key})
	}
	h := NewTypedHash(1)
	if err := hashSet(h, key, NewInt(1)); err != nil {
		t.Skipf("deeply nested array keys are rejected earlier: %v", err)
	}

	if err := validateCapabilityTraversalDepth("payload", h); err == nil {
		t.Fatal("depth guard admitted a typed key nested past the limit")
	}
	if _, err := cloneCapabilityDataOnlyValue("payload", h); err == nil {
		t.Fatal("clone accepted a typed key nested past the depth limit")
	}
}

// TestCapabilityDataCloneKeepsStoredLookupIdentity pins that a typed clone
// carries each entry's stored lookup identity. A hash resolves an array key
// by the identity it had at insertion, so an array mutated afterwards still
// answers to what it was; rehashing the mutated key while cloning made the
// clone answer to what the array now is, resolving differently from the hash
// it was copied from.
func TestCapabilityDataCloneKeepsStoredLookupIdentity(t *testing.T) {
	t.Parallel()

	keyElems := []Value{NewInt(1)}
	key := NewArray(keyElems)
	h := NewTypedHash(1)
	if err := hashSet(h, key, NewString("stored")); err != nil {
		t.Fatalf("HashSet array key: %v", err)
	}
	setArrayElems(key, []Value{NewInt(2)})

	// The source still answers to the identity captured at insertion.
	if _, ok, err := h.HashGet(NewArray([]Value{NewInt(1)})); err != nil || !ok {
		t.Skipf("source no longer resolves the pre-mutation key (found=%v, err=%v)", ok, err)
	}

	cloned, err := cloneCapabilityDataOnlyValue("payload", h)
	if err != nil {
		t.Fatalf("cloning a data-only hash failed: %v", err)
	}
	got, ok, err := cloned.HashGet(NewArray([]Value{NewInt(1)}))
	if err != nil || !ok || got.Kind() != KindString || got.String() != "stored" {
		t.Fatalf("clone lost the stored lookup identity: %#v (found=%v, err=%v)", got, ok, err)
	}
	if _, ok, _ := cloned.HashGet(NewArray([]Value{NewInt(2)})); ok {
		t.Fatal("clone resolves the mutated key, which the source does not")
	}
}

// TestBoundaryClonesPreserveTypedHashOrder pins that clones keep Ruby-style
// insertion order. The typed entry map ranges arbitrarily, so a clone
// rebuilt from that order would record it and iterate differently from the
// hash it was copied from.
func TestBoundaryClonesPreserveTypedHashOrder(t *testing.T) {
	t.Parallel()

	build := func() Value {
		h := NewTypedHash(8)
		for _, name := range []string{"zeta", "alpha", "mike", "bravo", "yankee", "delta", "kilo", "echo"} {
			if err := hashSet(h, NewSymbol(name), NewInt(1)); err != nil {
				t.Fatalf("HashSet %s: %v", name, err)
			}
		}
		return h
	}
	order := func(v Value) []string {
		var out []string
		for _, entry := range v.HashEntries() {
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
