package runtime

import (
	"context"
	"fmt"
	"strings"
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
	// walkNested reaches the array through a container it was handed rather
	// than being handed the array itself.
	walkNested := func(exec *Execution, _ Value, args []Value, _ map[string]Value, block Value) (Value, error) {
		inner := args[0]
		if inner.Kind() == KindHash {
			inner = inner.Hash()["items"]
		} else {
			inner = inner.Array()[0]
		}
		for _, item := range inner.Array() {
			if _, err := exec.CallBlock(block, []Value{item}); err != nil {
				return NewNil(), err
			}
		}
		return NewNil(), nil
	}
	return map[string]Value{
		"driver": NewObject(map[string]Value{
			"walk":       NewBuiltin("driver.walk", walk),
			"walk_kw":    NewBuiltin("driver.walk_kw", walk),
			"walk_in":    NewBuiltin("driver.walk_in", walkNested),
			"walk_inner": NewBuiltin("driver.walk_inner", walkNested),
		}),
	}, nil
}

// TestArrayShrinkDuringCallbackKeepsSnapshot pins that a host-driven frame is
// claimed whatever it walks.
//
// A capability method and a global builtin are dispatched with no receiver, so
// a driver that walks an array argument across exec.CallBlock had nothing
// claiming the header it was reading, and a shrink from inside the block zeroed
// slots it had not reached. Naming the arguments was not enough either: the
// array a host body walks can be reached through a hash, an object, or an outer
// array it was handed, and the runtime cannot enumerate what a body it did not
// write took hold of. Such a frame claims every backing instead.
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
end

def inside_hash()
  a = [1, 2, 3]
  seen = []
  driver.walk_in({ items: a }) do |x|
    seen.push(x)
    a.pop
  end
  seen
end

def inside_array()
  a = [1, 2, 3]
  seen = []
  driver.walk_inner([a]) do |x|
    seen.push(x)
    a.pop
  end
  seen
end`)

	for _, function := range []string{"positional", "keyword", "inside_hash", "inside_array"} {
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

// TestDetachedSnapshotStaysOnTheQuota pins that a header a shrink copied away
// from keeps costing while its holder is still walking it.
//
// The holder keeps that header alive on its Go stack, but the receiver no
// longer points at it, so nothing the estimator walks reaches it. A block that
// drained its receiver and dropped the result could then build a second
// generation of the same size against a receiver the drain had just emptied,
// with both live and only one counted.
func TestDetachedSnapshotStaysOnTheQuota(t *testing.T) {
	t.Parallel()

	// Eight half-megabyte strings fit the quota; two such generations do not.
	const build = `  a = []
  i = 0
  while i < 8
    a.push(seed * 100)
    i = i + 1
  end
`
	seed := NewString(strings.Repeat("abcdefghij", 500))
	config := Config{StepQuota: 50_000_000, MemoryQuotaBytes: 6 << 20}

	fits := compileScriptWithConfig(t, config, "def run(seed)\n"+build+"  a.size\nend")
	got, err := fits.Call(context.Background(), "run", []Value{seed}, CallOptions{})
	if err != nil {
		t.Fatalf("one generation must fit under the quota: %v", err)
	}
	if got.Int() != 8 {
		t.Fatalf("built %d elements, want 8", got.Int())
	}

	drains := compileScriptWithConfig(t, config, "def run(seed)\n"+build+`  b = []
  a.each do |x|
    a.pop(a.size)
    b.push(seed * 100)
  end
  b.size
end`)
	if _, err := drains.Call(context.Background(), "run", []Value{seed}, CallOptions{}); err == nil {
		t.Fatal("a drained snapshot the iterator still holds must stay on the quota")
	}
}

// TestShrinkUnderWildcardClaimStaysLinear pins the exemption that keeps a
// host-driven drain from copying on every call.
//
// A host-driven frame claims every backing, because what its Go body captured
// is not knowable from the call. Taken literally that would copy the survivors
// on every shrink and make a drain quadratic. It does not, because a frame
// cannot be walking storage that did not exist when it started: the backing the
// first copy allocates is exempt from the claim, so the rest of the drain zeroes
// in place.
func TestShrinkUnderWildcardClaimStaysLinear(t *testing.T) {
	t.Parallel()

	small := minStepsToDrainUnderDriver(t, 100)
	large := minStepsToDrainUnderDriver(t, 400)
	// Four times the elements costs about four times the steps when one copy is
	// amortized over the drain, and about sixteen when every pop copies.
	if limit := 6 * small; large > limit {
		t.Fatalf("draining 400 elements under a host driver cost %d steps against %d for "+
			"100; want at most %d, or the wildcard claim is copying every time",
			large, small, limit)
	}
}

// minStepsToDrainUnderDriver returns the smallest step quota that lets a pop
// drain of an n-element array run to completion inside a host adapter's
// callback.
func minStepsToDrainUnderDriver(t *testing.T, n int) int {
	t.Helper()

	const src = `def run(a)
  driver.walk(a) do |x|
    a.pop
  end
  a.size
end`
	elems := make([]Value, n)
	for i := range n {
		elems[i] = NewInt(int64(i))
	}

	lo, hi := 1, 40*n*n
	for lo < hi {
		mid := (lo + hi) / 2
		script := compileScriptWithConfig(t, Config{StepQuota: mid, MemoryQuotaBytes: Unlimited}, src)
		if _, err := script.Call(context.Background(), "run", []Value{NewArray(elems)}, CallOptions{
			Capabilities: []CapabilityAdapter{arrayArgDriver{}},
		}); err != nil {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// TestDetachedHeaderRecordIsBounded pins that one claim accounts for a bounded
// number of headers by walking them.
//
// A wildcard claim matches every array shrunk beneath it, including the
// short-lived ones a callback builds and drops, so the list of headers it holds
// for accounting has no natural bound. Walking a list that grows with the
// callback makes each memory check cost what the callback has done so far: a
// loop shrinking 8000 short-lived arrays inside one host call took 8.0s against
// a 0.8s baseline for the same script shape, on 24k charged steps. Past the cap
// a header is charged as a flat reserved total, which costs nothing per check.
func TestDetachedHeaderRecordIsBounded(t *testing.T) {
	t.Parallel()

	exec := &Execution{}
	held := &heldArrayBacking{wildcard: true}
	for i := range maxDetachedHeaders * 3 {
		held.recordDetached(exec, []Value{NewInt(int64(i))})
	}

	if len(held.detached) != maxDetachedHeaders {
		t.Fatalf("claim walks %d headers, want at most %d", len(held.detached), maxDetachedHeaders)
	}
	if held.overflow <= 0 {
		t.Fatal("headers past the cap must still be charged, as a reserved total")
	}
	if exec.reservedScratchBytes != held.overflow {
		t.Fatalf("reserved %d bytes against a recorded overflow of %d",
			exec.reservedScratchBytes, held.overflow)
	}
}
