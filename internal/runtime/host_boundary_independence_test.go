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
