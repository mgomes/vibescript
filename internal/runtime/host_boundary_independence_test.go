package runtime

import (
	"context"
	"testing"
)

// TestCallResultIsNotAScriptSharedWrapper is the structural pin issue #1210
// asks for: a composite Call result whose graph some other durable slot still
// names must cross as a fresh clone, never as the shared wrapper itself. A
// result no other slot names may transfer out live -- its only owner is the
// call that just ended -- so the pin targets the shared shape, where handing
// the wrapper out would let a host write into script-reachable state.
func TestCallResultIsNotAScriptSharedWrapper(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def rows()
  rows = [[1], [2]]
  keep = rows
  rows
end

def names()
  names = {alpha: "a"}
  keep = names
  names
end
`)

	if got := callFunc(t, script, "rows", nil); !got.Unpublished() {
		t.Fatalf("rows() handed out the script's own shared array wrapper")
	}
	if got := callFunc(t, script, "names", nil); !got.Unpublished() {
		t.Fatalf("names() handed out the script's own shared hash wrapper")
	}
}

// yieldCrossingCapability drives the CallBlock boundary from both sides: its
// pass method yields a wrapper over a map the host retains, and its grab
// method stashes whatever the block returns.
type yieldCrossingCapability struct {
	retained map[string]Value
	stashed  *Value
}

func (c *yieldCrossingCapability) Bind(_ CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"cross": NewObject(map[string]Value{
			"pass": NewBuiltin("cross.pass", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return exec.CallBlock(block, []Value{NewHash(c.retained)})
			}),
			"grab": NewBuiltin("cross.grab", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				v, err := exec.CallBlock(block, nil)
				if err != nil {
					return NewNil(), err
				}
				*c.stashed = v
				return NewNil(), nil
			}),
			"install": NewBuiltin("cross.install", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				receiver.Hash()["data"] = NewHash(c.retained)
				return NewNil(), nil
			}),
			"remove": NewBuiltin("cross.remove", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				delete(receiver.Hash(), "data")
				return NewNil(), nil
			}),
			"observe": NewBuiltin("cross.observe", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				keys := make([]Value, 0, len(c.retained))
				for key := range c.retained {
					keys = append(keys, NewString(key))
				}
				return NewInt(int64(len(keys))), nil
			}),
		}),
	}, nil
}

func (c *yieldCrossingCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return nil
}

// TestCallBlockIsAHostBoundary pins both directions of the block channel: a
// value the host yields enters script state detached from the host's
// backing, and the block's return value crosses to the host as a copy, so
// neither side can write through the other afterwards.
func TestCallBlockIsAHostBoundary(t *testing.T) {
	t.Parallel()

	t.Run("yielded value is detached", func(t *testing.T) {
		t.Parallel()
		cap := &yieldCrossingCapability{retained: map[string]Value{"k": NewInt(1)}}
		script := compileScriptDefault(t, `def run()
  kept = nil
  cross.pass { |v| kept = v; nil }
  kept.inspect
end`)
		got, err := script.Call(context.Background(), "run", nil, callOptionsWithCapabilities(cap))
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		cap.retained["k"] = NewString("scribbled")
		second, err := script.Call(context.Background(), "run", nil, callOptionsWithCapabilities(cap))
		if err != nil {
			t.Fatalf("second call: %v", err)
		}
		if got.String() != "{k: 1}" || second.String() != `{k: "scribbled"}` {
			t.Fatalf("yield crossing = %s then %s, want {k: 1} then {k: scribbled}", got.String(), second.String())
		}
	})

	t.Run("block return is copied for the host", func(t *testing.T) {
		t.Parallel()
		var stashed Value
		cap := &yieldCrossingCapability{stashed: &stashed}
		script := compileScriptDefault(t, `def run()
  h = {k: 1}
  cross.grab { h }
  h
end`)
		result, err := script.Call(context.Background(), "run", nil, callOptionsWithCapabilities(cap))
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		stashed.Hash()["k"] = NewString("scribbled")
		if result.String() != "{k: 1}" {
			t.Fatalf("a write through the block-return handle reached the Call result: %s", result.String())
		}
	})
}

// TestFactoryInstalledWrapperDoesNotTransferLive pins the receiver
// carve-out's boundary: a wrapper the host installs into its live capability
// object mid-call is a host-held handle, so reading it out of the Call must
// yield an independent value.
func TestFactoryInstalledWrapperDoesNotTransferLive(t *testing.T) {
	t.Parallel()

	cap := &yieldCrossingCapability{retained: map[string]Value{"k": NewInt(1)}}
	script := compileScriptDefault(t, `def run()
  cross.install()
  cross[:data]
end`)
	result, err := script.Call(context.Background(), "run", nil, callOptionsWithCapabilities(cap))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	cap.retained["k"] = NewString("scribbled")
	if result.String() != "{k: 1}" {
		t.Fatalf("an installed wrapper transferred live out of the Call: %s", result.String())
	}
}

// TestFactoryInstallsSurviveRepeatedCalls pins the factory channel's other
// half: marking what a call installed must not make the next call copy the
// capability object out from under it -- installs land on the same live
// receiver every time, including when the installing call errors and the
// script rescues.
func TestFactoryInstallsSurviveRepeatedCalls(t *testing.T) {
	t.Parallel()

	cap := &yieldCrossingCapability{retained: map[string]Value{"k": NewInt(1)}}
	script := compileScriptDefault(t, `def run()
  cross.install()
  first = cross[:data]
  cross.install()
  second = cross[:data]
  first.inspect + " " + second.inspect
end`)
	got, err := script.Call(context.Background(), "run", nil, callOptionsWithCapabilities(cap))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "{k: 1} {k: 1}" {
		t.Fatalf("repeated installs = %s, want {k: 1} {k: 1}", got.String())
	}
}

// TestInstalledWrapperIsIndependentTheMomentTheDispatchEnds pins the timing
// of the boundary: a wrapper a factory installed is shared before any later
// statement runs, so a script write copies instead of reaching the host's
// map, a later host dispatch isolates it as an argument, and removing it
// from host state before the Call returns cannot un-mark it.
func TestInstalledWrapperIsIndependentTheMomentTheDispatchEnds(t *testing.T) {
	t.Parallel()

	t.Run("script write copies", func(t *testing.T) {
		t.Parallel()
		cap := &yieldCrossingCapability{retained: map[string]Value{"role": NewString("guest")}}
		script := compileScriptDefault(t, `def run()
  cross.install()
  s = cross[:data]
  s[:admin] = true
  cross.observe()
end`)
		got, err := script.Call(context.Background(), "run", nil, callOptionsWithCapabilities(cap))
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if got.Int() != 1 {
			t.Fatalf("a script write reached the host map: %d entries, want 1", got.Int())
		}
	})

	t.Run("install then remove stays independent", func(t *testing.T) {
		t.Parallel()
		cap := &yieldCrossingCapability{retained: map[string]Value{"k": NewInt(1)}}
		script := compileScriptDefault(t, `def run()
  cross.install()
  x = cross[:data]
  cross.remove()
  x
end`)
		result, err := script.Call(context.Background(), "run", nil, callOptionsWithCapabilities(cap))
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		cap.retained["k"] = NewString("scribbled")
		if result.String() != "{k: 1}" {
			t.Fatalf("a removed install transferred live: %s", result.String())
		}
	})
}

// TestSharedScriptReceiverStaysDetachedFromAHostWrite pins the receiver
// rule's other half: a host builtin dispatched on plain script data that a
// sibling binding names must not hand that data to the host live.
func TestSharedScriptReceiverStaysDetachedFromAHostWrite(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	engine.registerHostBuiltin("poke", MarkHostBuiltin(NewBuiltin("poke", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		args[0].Hash()["stamp"] = NewInt(9)
		return NewNil(), nil
	})))
	script, err := engine.Compile(`def run()
  mine = { f: 1 }
  other = mine
  poke(mine)
  other.keys.size
end`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.Int() != 1 {
		t.Fatalf("a host write reached the sibling binding: %d keys, want 1", got.Int())
	}
}

// TestGlobalsHostedBuiltinObjectIsHostState pins that a builtin-bearing
// global object gets capability-object treatment: its factory installs work
// in place, and what they install cannot transfer out of the Call live.
func TestGlobalsHostedBuiltinObjectIsHostState(t *testing.T) {
	t.Parallel()

	retained := []Value{NewInt(1)}
	svc := NewObject(map[string]Value{
		"install": MarkHostBuiltin(NewBuiltin("svc.install", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			receiver.Hash()["stash"] = NewArray(retained)
			return NewNil(), nil
		})),
	})
	script := compileScriptDefault(t, `def run()
  svc.install()
  svc[:stash]
end`)
	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Globals: map[string]Value{"svc": svc},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	retained[0] = NewInt(999)
	if result.String() != "[1]" {
		t.Fatalf("a globals-hosted install transferred live: %s", result.String())
	}
}

// TestDeclaredNonMutatingStillMarksNestedArguments pins that skipping the
// isolation copy under a non-mutation declaration does not skip retention
// marking: a nested wrapper the host stashes must not transfer out of the
// Call live.
func TestDeclaredNonMutatingStillMarksNestedArguments(t *testing.T) {
	t.Parallel()

	var stashed Value
	engine := MustNewEngine(Config{})
	engine.registerHostBuiltin("snap", DeclareNonMutating(NewBuiltin("snap", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		stashed = args[0].Hash()["inner"]
		return NewNil(), nil
	})))
	script, err := engine.Compile(`def run()
  h = {inner: [1, 2]}
  snap(h)
  h[:inner]
end`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	stashed.Array()[0] = NewString("scribbled")
	if result.String() != "[1, 2]" {
		t.Fatalf("a nested stash under a non-mutation declaration stayed live: %s", result.String())
	}
}

// TestDBCapabilityReturnIsIndependentOfHostState is the canary behind the
// db adapter's return proof: the proof lets the dispatcher skip the boundary
// detach, so it is only sound while every db call really clones its result
// out of host state. A future method that returns a host-backed wrapper
// without CloneMethodResult fails here.
func TestDBCapabilityReturnIsIndependentOfHostState(t *testing.T) {
	t.Parallel()

	retained := map[string]Value{"id": NewInt(1)}
	stub := &dbCapabilityStub{findResult: NewHash(retained)}
	script := compileScriptDefault(t, `def run()
  db.find("Player", 1)
end`)
	result, err := script.Call(context.Background(), "run", nil, callOptionsWithCapabilities(
		MustNewDBCapability("db", stub),
	))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	retained["id"] = NewString("scribbled")
	if result.String() != "{id: 1}" {
		t.Fatalf("a proof-marked db return stayed live against host state: %s", result.String())
	}
}

// TestHostBuiltinArgumentIndependentOfHostWrites pins the outbound
// direction for builtin arguments: a host writing through the live backing
// of a value it received must not change what the script observes, even
// when the argument was named by exactly one script slot.
func TestHostBuiltinArgumentIndependentOfHostWrites(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	engine.RegisterBuiltin("scribble", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		args[0].Hash()["k"] = NewString("scribbled")
		return NewNil(), nil
	})
	script, err := engine.Compile(`def run()
  h = {k: 1}
  scribble(h)
  h.inspect
end`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "{k: 1}" {
		t.Fatalf("a host write through a received argument reached script state: %s", got.String())
	}
}

// TestHostBuiltinReturnIndependentOfRetainedBacking pins the inbound
// direction: a host builtin that returns a wrapper over a map it still holds
// must not keep a live channel into script state -- mutating the retained
// backing after the call reaches nothing the script observes.
func TestHostBuiltinReturnIndependentOfRetainedBacking(t *testing.T) {
	t.Parallel()

	retained := map[string]Value{"k": NewInt(1)}
	engine := MustNewEngine(Config{})
	engine.RegisterBuiltin("give", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewHash(retained), nil
	})
	engine.RegisterBuiltin("corrupt", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		retained["k"] = NewString("scribbled")
		return NewNil(), nil
	})
	script, err := engine.Compile(`def run()
  h = give()
  corrupt()
  h.inspect
end`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "{k: 1}" {
		t.Fatalf("host-retained backing wrote into script state: %s", got.String())
	}
}

// TestCallResultIndependentOfARetainedHostHandle drives the same contract
// end to end: a host builtin legitimately retains an argument, the script
// returns that collection, and writes through the retained handle's live
// backing must not reach the Call result the host already holds.
func TestCallResultIndependentOfARetainedHostHandle(t *testing.T) {
	t.Parallel()

	var stashed Value
	engine := MustNewEngine(Config{})
	engine.RegisterBuiltin("stash", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		stashed = args[0]
		return NewNil(), nil
	})
	script, err := engine.Compile(`def run()
  a = {items: [1, 2]}
  stash(a)
  a
end`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	stashed.Hash()["items"] = NewString("mutated")

	if got := result.String(); got != "{items: [1, 2]}" {
		t.Fatalf("a write through the retained handle reached the Call result: %s", got)
	}
}
