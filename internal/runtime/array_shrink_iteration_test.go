package runtime

import (
	"context"
	"fmt"
	"testing"
)

// TestArrayShrinkDuringIterationKeepsSnapshot pins that an in-place shrink made
// from inside a driver's body cannot alter what the driver still has to yield.
//
// A driver snapshots the element header before it runs any script and walks
// that header while the script runs (see TestArrayMutationDuringIteration), so
// a shrink that zeroes a slot the driver has not reached yet would hand it a
// nil that was never in the array. pop clears from the tail, which a forward
// driver has not reached; shift clears from the head, which a reverse driver
// has not reached.
//
// The cases cover both kinds of driver: the block-driving member functions, and
// the evaluator's `for x in a`, which never reaches builtin dispatch. The
// for-loop cases also run the shrink from inside a script function, since the
// claim has to outlast a call that leaves the builtin depth alone.
func TestArrayShrinkDuringIterationKeepsSnapshot(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def pop_during_each()
  a = [1, 2, 3]
  seen = []
  a.each do |x|
    seen.push(x)
    a.pop
  end
  seen
end

def pop_count_during_each()
  a = [1, 2, 3, 4]
  seen = []
  a.each do |x|
    seen.push(x)
    a.pop(1)
  end
  seen
end

def shift_during_reverse_each()
  a = [1, 2, 3]
  seen = []
  a.reverse_each do |x|
    seen.push(x)
    a.shift
  end
  seen
end

def shift_count_during_reverse_each()
  a = [1, 2, 3, 4]
  seen = []
  a.reverse_each do |x|
    seen.push(x)
    a.shift(1)
  end
  seen
end

def shift_during_each()
  a = [1, 2, 3]
  seen = []
  a.each do |x|
    seen.push(x)
    a.shift
  end
  seen
end

def drain(z)
  z.pop
end

def pop_during_for_in()
  a = [1, 2, 3]
  seen = []
  for x in a
    seen.push(x)
    a.pop
  end
  seen
end

def pop_count_during_for_in()
  a = [1, 2, 3, 4]
  seen = []
  for x in a
    seen.push(x)
    a.pop(1)
  end
  seen
end

def shift_during_for_in()
  a = [1, 2, 3]
  seen = []
  for x in a
    seen.push(x)
    a.shift
  end
  seen
end

def pop_during_for_in_via_function()
  a = [1, 2, 3]
  seen = []
  for x in a
    seen.push(x)
    drain(a)
  end
  seen
end

def pop_during_for_in_inside_each()
  a = [1, 2, 3]
  seen = []
  a.each do |x|
    for y in a
      seen.push(y)
      a.pop
    end
  end
  seen
end`)

	cases := []struct {
		function string
		want     string
	}{
		{"pop_during_each", `[1, 2, 3]`},
		{"pop_count_during_each", `[1, 2, 3, 4]`},
		{"shift_during_reverse_each", `[3, 2, 1]`},
		{"shift_count_during_reverse_each", `[4, 3, 2, 1]`},
		{"shift_during_each", `[1, 2, 3]`},
		{"pop_during_for_in", `[1, 2, 3]`},
		{"pop_count_during_for_in", `[1, 2, 3, 4]`},
		{"shift_during_for_in", `[1, 2, 3]`},
		{"pop_during_for_in_via_function", `[1, 2, 3]`},
		{"pop_during_for_in_inside_each", `[1, 2, 3]`},
	}
	for _, tc := range cases {
		t.Run(tc.function, func(t *testing.T) {
			t.Parallel()

			got, err := script.Call(context.Background(), tc.function, nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.function, err)
			}
			if got.Inspect() != tc.want {
				t.Fatalf("%s yielded %s, want %s", tc.function, got.Inspect(), tc.want)
			}
		})
	}
}

// minStepsToDrainInside returns the smallest step quota that lets a pop drain
// of an n-element array run to completion inside the given driver, whose body
// is the single statement `a.pop`.
func minStepsToDrainInside(t *testing.T, driver string, n int) int {
	t.Helper()

	src := fmt.Sprintf(`def run()
  a = []
  i = 0
  while i < %d
    a.push(i)
    i = i + 1
  end
  %s
  a.size
end`, n, driver)

	lo, hi := 1, n*n
	for lo < hi {
		mid := (lo + hi) / 2
		script := compileScriptWithConfig(t, Config{StepQuota: mid, MemoryQuotaBytes: Unlimited}, src)
		if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// TestShrinkDuringIterationStaysLinear pins that only the first shrink inside a
// driver's body copies. The copy leaves the driver's captured header alone by
// giving the receiver a new backing, which the driver is not walking, so every
// later shrink can go back to zeroing in place. Copying on every shrink would
// be just as correct and would make this drain quadratic, so the step cost of
// the copies is what the measurement is really watching.
func TestShrinkDuringIterationStaysLinear(t *testing.T) {
	t.Parallel()

	drivers := map[string]string{
		"each": `a.each do |x|
    a.pop
  end`,
		"for_in": `for x in a
    a.pop
  end`,
	}
	for name, driver := range drivers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			small := minStepsToDrainInside(t, driver, 100)
			large := minStepsToDrainInside(t, driver, 400)
			// Four times the elements costs about four times the steps when one
			// copy is amortized over the drain, and about sixteen when every pop
			// copies.
			if limit := 6 * small; large > limit {
				t.Fatalf("draining 400 elements inside %s cost %d steps against %d for 100; "+
					"want at most %d, or the copy is not amortized", name, large, small, limit)
			}
		})
	}
}

// TestZeroCountShrinkDoesNoWork pins that pop(0) and shift(0) cost the same
// whatever the receiver holds. They remove nothing, but they used to reach the
// shrink path anyway, which inside an iterator copies the whole receiver and
// bills its elements: a no-op over 800 elements cost 700 steps more than the
// same no-op over 100.
func TestZeroCountShrinkDoesNoWork(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{"a.pop(0)", "a.shift(0)"} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			small := minStepsForShrinkInsideFind(t, expr, 100)
			large := minStepsForShrinkInsideFind(t, expr, 800)
			if small != large {
				t.Fatalf("%s cost %d steps over 100 elements and %d over 800; a call that "+
					"removes nothing must not follow the receiver", expr, small, large)
			}
		})
	}
}

// minStepsForShrinkInsideFind returns the smallest step quota that lets expr
// run once inside a find over an n-element array. find stops at the first
// truthy block result, so the measurement is one shrink rather than n, and the
// array arrives as an argument so building it costs no steps.
func minStepsForShrinkInsideFind(t *testing.T, expr string, n int) int {
	t.Helper()

	src := fmt.Sprintf("def run(a)\n  a.find do |x|\n    %s\n    true\n  end\n  0\nend", expr)
	elems := make([]Value, n)
	for i := range n {
		elems[i] = NewInt(int64(i))
	}

	lo, hi := 1, 100*n
	for lo < hi {
		mid := (lo + hi) / 2
		script := compileScriptWithConfig(t, Config{StepQuota: mid, MemoryQuotaBytes: Unlimited}, src)
		if _, err := script.Call(context.Background(), "run", []Value{NewArray(elems)}, CallOptions{}); err != nil {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// arrayArgDriver drives a script block from an array argument, the way a host
// adapter walking a collection the script handed it does. It is dispatched with
// no receiver, so the receiver claim alone never sees the array it walks.
type arrayArgDriver struct{}

func (arrayArgDriver) Bind(CapabilityBinding) (map[string]Value, error) {
	walk := func(exec *Execution, _ Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		items := args[0].Array()
		if len(items) == 0 {
			items = kwargs["over"].Array()
		}
		for _, item := range items {
			if _, err := exec.CallBlock(block, []Value{item}); err != nil {
				return NewNil(), err
			}
		}
		return NewNil(), nil
	}
	return map[string]Value{
		"driver": NewObject(map[string]Value{
			"walk":    NewBuiltin("driver.walk", walk),
			"walk_kw": NewBuiltin("driver.walk_kw", walk),
		}),
	}, nil
}

// TestArrayShrinkDuringCallbackKeepsSnapshot pins that the arguments a frame
// holds are claimed like its receiver.
//
// A capability method and a global builtin are dispatched with no receiver, so
// a driver that walks an array argument across exec.CallBlock had nothing
// claiming the header it was reading, and a shrink from inside the block zeroed
// slots it had not reached.
func TestArrayShrinkDuringCallbackKeepsSnapshot(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def positional()
  a = [1, 2, 3]
  seen = []
  driver.walk(a) do |x|
    seen.push(x)
    a.pop
  end
  seen
end

def keyword()
  a = [1, 2, 3]
  seen = []
  driver.walk_kw([], over: a) do |x|
    seen.push(x)
    a.pop
  end
  seen
end`)

	for _, function := range []string{"positional", "keyword"} {
		t.Run(function, func(t *testing.T) {
			t.Parallel()

			got, err := script.Call(context.Background(), function, nil, CallOptions{
				Capabilities: []CapabilityAdapter{arrayArgDriver{}},
			})
			if err != nil {
				t.Fatalf("%s: %v", function, err)
			}
			if want := `[1, 2, 3]`; got.Inspect() != want {
				t.Fatalf("%s yielded %s, want %s", function, got.Inspect(), want)
			}
		})
	}
}
