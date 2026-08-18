package runtime

import (
	"context"
	"testing"
)

// Repro A: two mutating host dispatches on a builtin-bearing global.
// The first dispatch's markHostWritableState marks svc shared; the lazy
// globals path never called recordHostStateRoot, so the second dispatch
// sees a shared non-host-state receiver and detaches it. The second
// install should then land in a detached copy and be lost.
func TestHostStateBoundaryRegressionGlobalSecondInstallLost(t *testing.T) {
	t.Parallel()

	svc := NewObject(map[string]Value{
		"a": MarkHostBuiltin(NewBuiltin("svc.a", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			receiver.Hash()["x"] = NewInt(1)
			return NewNil(), nil
		})),
		"b": MarkHostBuiltin(NewBuiltin("svc.b", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			receiver.Hash()["y"] = NewInt(2)
			return NewNil(), nil
		})),
	})
	script := compileScriptDefault(t, `def run()
  svc.a()
  svc.b()
  svc[:y]
end`)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{
		Globals: map[string]Value{"svc": svc},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.Kind() != KindInt || got.Int() != 2 {
		t.Fatalf("second install lost: svc[:y] = %s (kind %v), want 2", got.String(), got.Kind())
	}
}

// Repro B: aliasing the global before the first mutating dispatch makes the
// receiver shared, so even the first install detaches and is lost.
func TestHostStateBoundaryRegressionGlobalAliasedInstallLost(t *testing.T) {
	t.Parallel()

	svc := NewObject(map[string]Value{
		"a": MarkHostBuiltin(NewBuiltin("svc.a", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			receiver.Hash()["x"] = NewInt(1)
			return NewNil(), nil
		})),
	})
	script := compileScriptDefault(t, `def run()
  s = svc
  svc.a()
  svc[:x]
end`)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{
		Globals: map[string]Value{"svc": svc},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("aliased install lost: svc[:x] = %s (kind %v), want 1", got.String(), got.Kind())
	}
}

// Repro C: mid-dispatch yield window. A mutating capability builtin installs
// a wrapper around a host-retained map and then yields to a script block
// before returning; markHostWritableState has not run yet, so a script
// write inside the block should (per the boundary invariant) copy but may
// instead land in the host's retained map.
type yieldWindowCapability struct {
	retained map[string]Value
}

func (c *yieldWindowCapability) Name() string { return "wincross" }

func (c *yieldWindowCapability) Bind(_ CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"wincross": NewObject(map[string]Value{
			"install_and_yield": NewBuiltin("wincross.install_and_yield", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				receiver.Hash()["data"] = NewHash(c.retained)
				if _, err := exec.CallBlock(block, nil); err != nil {
					return NewNil(), err
				}
				return NewNil(), nil
			}),
			"observe": NewBuiltin("wincross.observe", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return NewInt(int64(len(c.retained))), nil
			}),
		}),
	}, nil
}

func TestHostStateBoundaryRegressionMidDispatchYieldWindow(t *testing.T) {
	t.Parallel()

	cap := &yieldWindowCapability{retained: map[string]Value{"role": NewString("guest")}}
	script := compileScriptDefault(t, `def run()
  wincross.install_and_yield() do
    s = wincross[:data]
    s[:admin] = true
  end
  wincross.observe()
end`)
	got, err := script.Call(context.Background(), "run", nil, callOptionsWithCapabilities(cap))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.Int() != 1 {
		t.Fatalf("script write inside yield reached the host map: %d entries, want 1", got.Int())
	}
}

// Repro D: declared-non-mutating but retaining capability builtin. The
// removed end-of-call sweep used to mark everything reachable from the
// capability roots before the result crossed; markHostWritableState only
// runs for mutating dispatches, so a wrapper the host stashed during a
// non-mutating dispatch can transfer out of the Call live.
type snoopCapability struct {
	stash Value
}

func (c *snoopCapability) Name() string { return "snoopcross" }

func (c *snoopCapability) Bind(_ CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"snoopcross": NewObject(map[string]Value{
			"snoop": DeclareNonMutating(NewBuiltin("snoopcross.snoop", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				c.stash = receiver.Hash()["cfg"]
				return NewNil(), nil
			})),
		}),
	}, nil
}

func TestHostStateBoundaryRegressionNonMutatingRetainedReceiverTransfersLive(t *testing.T) {
	t.Parallel()

	cap := &snoopCapability{}
	script := compileScriptDefault(t, `def run()
  snoopcross[:cfg] = { a: 1 }
  snoopcross.snoop()
  snoopcross[:cfg]
end`)
	result, err := script.Call(context.Background(), "run", nil, callOptionsWithCapabilities(cap))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !cap.stash.IsNil() && cap.stash.Kind() == KindHash {
		cap.stash.Hash()["a"] = NewInt(999)
	}
	if result.String() != "{a: 1}" {
		t.Fatalf("retained receiver sub-wrapper transferred live: %s", result.String())
	}
}

// Repro E: hostStateIdentities pollution. markHostWritableState adds the
// dispatched receiver's graph to the host-state identity set even when the
// receiver is plain script data (any capability being bound makes the set
// non-nil). A later mutating dispatch on that data then crosses live even
// though a sibling binding names it, so the host write reaches the sibling.
type inertCapability struct{}

func (c *inertCapability) Name() string { return "inert" }

func (c *inertCapability) Bind(_ CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"inert": NewObject(map[string]Value{}),
	}, nil
}

func TestHostStateBoundaryRegressionScriptDataJoinsHostStateIdentities(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	engine.registerHostBuiltin("svc", NewObject(map[string]Value{
		"stampme": MarkHostBuiltin(NewBuiltin("svc.stampme", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			receiver.Hash()[args[0].String()] = NewInt(9)
			return NewNil(), nil
		})),
	}))
	script, err := engine.Compile(`def run()
  mine = { f: 1, stampme: svc[:stampme] }
  mine.stampme("s1")
  other = mine
  mine.stampme("s2")
  other[:s2] == nil
end`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !got.Bool() {
		// After the first stampme, mine joined hostStateIdentities; the second
		// stampme then crossed live despite the alias, so the host write
		// reached other.
		t.Fatalf("sibling observed a host write: other[:s2] is set")
	}
}

// Repro F: a wrapper reachable from BOTH host state and a plain-data
// receiver must be recorded as host state. The receiver walk runs with no
// recorder; if it reached the shared wrapper first through the shared seen
// map, the roots walk would skip it unrecorded, and the next dispatch on it
// would detach it as script data -- losing the host's install.
func TestHostStateBoundaryRegressionSharedSeenDoesNotShadowRootRecording(t *testing.T) {
	t.Parallel()
	// The host-state rule keys on wrapper identity, and this shape depends
	// on a script write landing in the host object in place before any
	// marking; the always-copy oracle rebinds a copy on every write, so the
	// premise cannot hold there.
	skipNoCopyPin(t)

	svc := NewObject(map[string]Value{
		"stamp": MarkHostBuiltin(NewBuiltin("svc.stamp", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if receiver.Kind() == KindHash || receiver.Kind() == KindObject {
				receiver.Hash()["s"] = NewInt(9)
			}
			return NewNil(), nil
		})),
	})
	script := compileScriptDefault(t, `def run()
  svc[:slot] = { stamp: svc[:stamp] }
  box = { m: svc[:stamp], w: svc[:slot] }
  box.m()
  svc[:slot].stamp()
  svc[:slot][:s]
end`)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{
		Globals: map[string]Value{"svc": svc},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.Kind() != KindInt || got.Int() != 9 {
		t.Fatalf("the host's install landed in a detached copy: %#v, want 9", got)
	}
}
