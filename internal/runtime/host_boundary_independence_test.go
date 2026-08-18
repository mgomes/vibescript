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
