package runtime

import (
	"strings"
	"testing"
)

// A block belongs to the call it was written on. ADR-006 owed a host contract
// saying so; a promise the runtime cannot check is the failure shape that
// decision exists to remove, so the contract is enforced instead: the block is
// invalidated when its receiving call returns, and a later invocation fails
// loudly rather than running a body whose frame has unwound.
func TestBlockInvokedAfterItsCallReturnsFailsLoudly(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	var retained Value
	engine.RegisterBuiltin("stash", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, block Value) (Value, error) {
		retained = block
		return NewNil(), nil
	})
	engine.RegisterBuiltin("run_stashed", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return exec.CallBlock(retained, []Value{NewInt(1)})
	})

	script, err := engine.Compile(`def run
  stash { |n| n }
  run_stashed()
end`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err = script.Call(t.Context(), "run", nil, CallOptions{}); err == nil {
		t.Fatal("expected a late block invocation to fail")
	}
	if !strings.Contains(err.Error(), "block invoked after the call it was given to returned") {
		t.Fatalf("error = %v, want it to name the retired block", err)
	}
}

// The same block invoked repeatedly inside its own call keeps working: the
// enforcement is about lifetime, not about a single use.
func TestBlockRunsManyTimesWithinItsOwnCall(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	engine.RegisterBuiltin("thrice", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, block Value) (Value, error) {
		total := int64(0)
		for i := range 3 {
			result, err := exec.CallBlock(block, []Value{NewInt(int64(i))})
			if err != nil {
				return NewNil(), err
			}
			total += result.Int()
		}
		return NewInt(total), nil
	})

	script, err := engine.Compile(`def run
  thrice { |n| n + 1 }
end`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	result, err := script.Call(t.Context(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if result.Int() != 6 {
		t.Fatalf("result = %d, want 6", result.Int())
	}
}

// A host that keeps a block across two separate Script.Call invocations is the
// case the inbound rebinder used to re-root; retirement now rejects it before
// any of that machinery runs.
func TestBlockRetainedAcrossCallsIsRejected(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	var retained Value
	engine.RegisterBuiltin("stash", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, block Value) (Value, error) {
		retained = block
		return NewNil(), nil
	})
	engine.RegisterBuiltin("run_stashed", func(exec *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return exec.CallBlock(retained, []Value{NewInt(1)})
	})

	script, err := engine.Compile(`def capture
  stash { |n| n }
end

def replay
  run_stashed()
end`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err = script.Call(t.Context(), "capture", nil, CallOptions{}); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, err = script.Call(t.Context(), "replay", nil, CallOptions{}); err == nil {
		t.Fatal("expected the replayed block to fail")
	} else if !strings.Contains(err.Error(), "block invoked after the call it was given to returned") {
		t.Fatalf("error = %v, want it to name the retired block", err)
	}
}
