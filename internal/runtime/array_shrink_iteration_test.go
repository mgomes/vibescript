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

def push_after_pop_each()
  a = [1, 2, 3]
  seen = []
  a.each do |x|
    seen.push(x)
    a.pop
    a.push(9)
  end
  seen
end

def push_after_pop_for_in()
  a = [1, 2, 3]
  seen = []
  for x in a
    seen.push(x)
    a.pop
    a.push(9)
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
		{"push_after_pop_each", `[1, 2, 3]`},
		{"push_after_pop_for_in", `[1, 2, 3]`},
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
		"each_pop": `a.each do |x|
    a.pop
  end`,
		"each_shift": `a.each do |x|
    a.shift
  end`,
		"for_in_pop": `for x in a
    a.pop
  end`,
		"for_in_shift": `for x in a
    a.shift
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

// TestRetainedSnapshotStaysOnTheQuota pins that a shrink beneath a host-driven
// frame keeps what it removed on the memory quota until the frame is done.
//
// Such a shrink leaves the storage untouched and narrows the array over it, so
// the removed payloads stay reachable through the backing while the claim is
// live. Charging only the window the array now shows would let a callback drain
// its receiver and build a second generation of the same size against a quota
// that had forgotten the first.
func TestRetainedSnapshotStaysOnTheQuota(t *testing.T) {
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
	if _, err := fits.Call(context.Background(), "run", []Value{seed}, CallOptions{}); err != nil {
		t.Fatalf("one generation must fit under the quota: %v", err)
	}

	drains := compileScriptWithConfig(t, config, "def run(seed)\n"+build+`  b = []
  driver.walk(a) do |x|
    a.pop(a.size)
    b.push(seed * 100)
  end
  b.size
end`)
	_, err := drains.Call(context.Background(), "run", []Value{seed}, CallOptions{
		Capabilities: []CapabilityAdapter{arrayArgDriver{}},
	})
	if err == nil {
		t.Fatal("a drained backing the callback still holds must stay on the quota")
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
	// walkReturned takes an array back from its first callback and walks that
	// header across later ones, so the header it holds is storage that did not
	// exist when the frame started.
	walkReturned := func(exec *Execution, _ Value, _ []Value, _ map[string]Value, block Value) (Value, error) {
		got, err := exec.CallBlock(block, []Value{NewInt(0)})
		if err != nil {
			return NewNil(), err
		}
		for _, item := range got.Array() {
			if _, err := exec.CallBlock(block, []Value{item}); err != nil {
				return NewNil(), err
			}
		}
		return NewNil(), nil
	}
	// walkAliased hands the script two array values over one Go slice, which
	// only host code can build.
	walkAliased := func(exec *Execution, _ Value, _ []Value, _ map[string]Value, block Value) (Value, error) {
		elems := []Value{NewInt(1), NewInt(2), NewInt(3)}
		_, err := exec.CallBlock(block, []Value{NewArray(elems), NewArray(elems)})
		return NewNil(), err
	}
	// walkAliasedBig is walkAliased with a payload worth measuring on the
	// element the script removes through both values. The payload is built here
	// rather than passed in, so the only thing that can hold it is the storage
	// the two values share.
	walkAliasedBig := func(exec *Execution, _ Value, args []Value, _ map[string]Value, block Value) (Value, error) {
		elems := []Value{NewInt(1), NewInt(2), NewString(strings.Repeat(args[0].String(), 800_000))}
		_, err := exec.CallBlock(block, []Value{NewArray(elems), NewArray(elems)})
		return NewNil(), err
	}
	// walkSpare hands the script an array showing one element over storage whose
	// spare capacity still holds payloads, which only host code can build.
	walkSpare := func(exec *Execution, _ Value, args []Value, _ map[string]Value, block Value) (Value, error) {
		elems := make([]Value, 4)
		elems[0] = NewInt(1)
		for i := 1; i < len(elems); i++ {
			// Distinct strings: the estimator deduplicates payloads by identity,
			// so one string in three slots would be charged once.
			elems[i] = NewString(strings.Repeat(args[0].String(), 200_000+i))
		}
		_, err := exec.CallBlock(block, []Value{NewArray(elems[:1])})
		return NewNil(), err
	}
	return map[string]Value{
		"driver": NewObject(map[string]Value{
			"walk_spare":       NewBuiltin("driver.walk_spare", walkSpare),
			"walk_aliased":     NewBuiltin("driver.walk_aliased", walkAliased),
			"walk_aliased_big": NewBuiltin("driver.walk_aliased_big", walkAliasedBig),
			"walk":             NewBuiltin("driver.walk", walk),
			"walk_kw":          NewBuiltin("driver.walk_kw", walk),
			"walk_in":          NewBuiltin("driver.walk_in", walkNested),
			"walk_inner":       NewBuiltin("driver.walk_inner", walkNested),
			"walk_returned":    NewBuiltin("driver.walk_returned", walkReturned),
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
// write took hold of. Such a frame claims every backing instead -- including
// storage created after it started, which it can be handed back as a callback
// result.
//
// push_after_pop is the case that is not about what a shrink writes but about
// what it leaves writable: narrowing in place left spare capacity inside the
// frame's window, and a later push reused it and rewrote an element the frame
// had yet to reach. Every one of these shapes yielded 1, 2, 9 on master.
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
end

def push_after_pop()
  a = [1, 2, 3]
  seen = []
  driver.walk(a) do |x|
    seen.push(x)
    a.pop
    a.push(9)
  end
  seen
end

def handed_back_push()
  a = [1, 2, 3, 4]
  seen = []
  driver.walk_returned do |x|
    if x == 0
      a.pop
      a.push(9)
      a
    else
      seen.push(x)
      a.pop
      a.push(8)
      0
    end
  end
  seen
end

def handed_back()
  a = [1, 2, 3, 4]
  seen = []
  driver.walk_returned do |x|
    if x == 0
      a.pop
      a
    else
      seen.push(x)
      a.pop
      0
    end
  end
  seen
end`)

	cases := []struct {
		function string
		want     string
	}{
		{"positional", `[1, 2, 3]`},
		{"keyword", `[1, 2, 3]`},
		{"inside_hash", `[1, 2, 3]`},
		{"inside_array", `[1, 2, 3]`},
		{"handed_back", `[1, 2, 3]`},
		{"push_after_pop", `[1, 2, 3]`},
		// The frame walks storage a push allocated after the claim began, so
		// the element it has yet to reach lives there. This is the case that
		// rules out skipping the bound for storage that postdates the claim,
		// which would otherwise return a pop-then-push rotation to linear.
		{"handed_back_push", `[1, 2, 3, 9]`},
	}
	for _, tc := range cases {
		t.Run(tc.function, func(t *testing.T) {
			t.Parallel()

			got, err := script.Call(context.Background(), tc.function, nil, CallOptions{
				Capabilities: []CapabilityAdapter{arrayArgDriver{}},
			})
			if err != nil {
				t.Fatalf("%s: %v", tc.function, err)
			}
			if got.Inspect() != tc.want {
				t.Fatalf("%s yielded %s, want %s", tc.function, got.Inspect(), tc.want)
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

	// pop and shift both have to be measured. shift moves the start of the
	// window, so keying the claim on that address made one drain look like a
	// series of new storage: it cost 68,735 steps over 400 elements against
	// 1,207 through pop, and a measurement that only ran pop saw none of it.
	for _, op := range []string{"a.pop", "a.shift"} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()

			small := minStepsToDrainUnderDriver(t, op, 100)
			large := minStepsToDrainUnderDriver(t, op, 400)
			// Four times the elements costs about four times the steps when the
			// drain narrows one piece of storage, and about sixteen when each
			// call copies.
			if limit := 6 * small; large > limit {
				t.Fatalf("draining 400 elements with %s under a host driver cost %d steps "+
					"against %d for 100; want at most %d", op, large, small, limit)
			}
		})
	}
}

// minStepsToDrainUnderDriver returns the smallest step quota that lets a pop
// drain of an n-element array run to completion inside a host adapter's
// callback.
func minStepsToDrainUnderDriver(t *testing.T, op string, n int) int {
	t.Helper()

	src := "def run(a)\n  driver.walk(a) do |x|\n    " + op + "\n  end\n  a.size\nend"
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
	for i := range maxHeldArrayHeaders * 3 {
		held.recordDetached(exec, []Value{NewInt(int64(i))})
	}

	if len(held.detached) != maxHeldArrayHeaders {
		t.Fatalf("claim walks %d headers, want at most %d", len(held.detached), maxHeldArrayHeaders)
	}
	if held.overflow <= 0 {
		t.Fatal("headers past the cap must still be charged, as a reserved total")
	}
	if exec.reservedScratchBytes != held.overflow {
		t.Fatalf("reserved %d bytes against a recorded overflow of %d",
			exec.reservedScratchBytes, held.overflow)
	}
}

// TestAliasedReceiversKeepTheirElements pins that releasing a claim does not
// disturb a second array built over the same storage.
//
// A shrink beneath a host-driven frame narrows the array over storage it leaves
// untouched, and moves it off that storage when the claim drops. It moves rather
// than clearing for this reason: which slots are dead is a question about every
// array behind the storage, not just the one that shrank, and a host can build
// two over one slice. Clearing by the first array's window left the second
// reading [2, nil] where it should read [2, 3].
func TestAliasedReceiversKeepTheirElements(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  kept = nil
  driver.walk_aliased do |a, b|
    a.pop
    b.shift
    kept = b
  end
  kept
end`)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{arrayArgDriver{}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := `[2, 3]`; got.Inspect() != want {
		t.Fatalf("the second array reads %s, want %s", got.Inspect(), want)
	}
}

// TestRetainedHeaderRecordIsBounded pins that one claim holds a bounded number
// of narrowed arrays, whatever runs beneath it.
//
// Every record keeps its array's storage alive and is walked on each memory
// check, so an unbounded list brings back the quadratic the cap exists to stop.
// Nothing hands records from a dropped claim to an enclosing one any more --
// moving an array off its storage is safe whoever else is walking that storage,
// so a claim below has no say in it -- which leaves this guard as the only way
// records are added.
func TestRetainedHeaderRecordIsBounded(t *testing.T) {
	t.Parallel()

	held := &heldArrayBacking{wildcard: true}
	for i := range maxHeldArrayHeaders * 3 {
		full := []Value{NewInt(int64(i))}
		receiver := NewArray(full)
		if !held.canRetain(full, receiver) {
			continue
		}
		held.retain(full, receiver)
	}
	if len(held.retained) != maxHeldArrayHeaders {
		t.Fatalf("claim holds %d narrowed arrays, want at most %d",
			len(held.retained), maxHeldArrayHeaders)
	}
}

// TestRetainedBackingIsChargedByCapacity pins that a record charges the storage
// it holds, not the window the array happened to show when the claim took it on.
//
// The record can be the only thing rooting an allocation. Billing it by that
// window would let an array showing one element sit in front of storage full of
// payloads and cost one element, which is the finding this file exists for
// arrived at from the other end.
func TestRetainedBackingIsChargedByCapacity(t *testing.T) {
	t.Parallel()

	// The hidden payloads are about 6 MiB and the second generation about 4;
	// either fits under the quota alone and together they do not.
	script := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: 8 << 20},
		`def run(seed)
  driver.walk_spare(seed) do |a|
    a.pop
    b = []
    i = 0
    while i < 2
      b.push(seed * 200_000)
      i = i + 1
    end
    b.size
  end
  0
end`)
	_, err := script.Call(context.Background(), "run", []Value{NewString("abcdefghij")}, CallOptions{
		Capabilities: []CapabilityAdapter{arrayArgDriver{}},
	})
	if err == nil {
		t.Fatal("storage held only by a retained record must be charged for what it holds")
	}
}

// TestRetainedBackingChargesOnlyWhatIsHidden pins that a record adds the part
// of an allocation the array no longer shows, and not the part it does.
//
// The graph walk already reaches the array and charges the window it shows,
// elements and slots alike. Adding the allocation whole on top bills that window
// twice, which turns a drain that copies nothing into one that has to fit twice
// the array: 10,000 integers needed 1,285,392 bytes against the 645,432 the
// array itself accounts for. A quota that turns away a program which fits is a
// defect in the same way as one that admits a program which does not.
func TestRetainedBackingChargesOnlyWhatIsHidden(t *testing.T) {
	t.Parallel()

	const n = 10000
	elems := make([]Value, n)
	for i := range n {
		elems[i] = NewInt(int64(i))
	}

	// Comfortably above what the array accounts for, comfortably below twice it.
	const quota = 900 << 10
	script := compileScriptWithConfig(t, Config{StepQuota: 500_000_000, MemoryQuotaBytes: quota},
		`def run(a)
  driver.walk(a) do |x|
    a.pop
  end
  a.size
end`)
	got, err := script.Call(context.Background(), "run", []Value{NewArray(elems)}, CallOptions{
		Capabilities: []CapabilityAdapter{arrayArgDriver{}},
	})
	if err != nil {
		t.Fatalf("a drain that copies nothing must fit a quota the array fits in: %v", err)
	}
	if got.Int() != 0 {
		t.Fatalf("array holds %d elements after the drain, want 0", got.Int())
	}
}

// TestRetainedRecordNeverLowersTheQuota pins that holding a record over storage
// an array has left cannot make a program fit that would not fit without it.
//
// The record charges the part of an allocation the array no longer shows, which
// it worked out by netting the array's own capacity off the allocation's. Once
// a push moves the array onto storage of its own that capacity is no measure of
// this allocation, and netting it off subtracts the new storage's slots --
// cancelling a charge the graph walk had correctly made. The same growth was
// admitted at 349,841 bytes with a record over the old storage and 398,737
// without, so a shrink was buying quota rather than costing it.
func TestRetainedRecordNeverLowersTheQuota(t *testing.T) {
	t.Parallel()

	const grow = `    i = 0
    while i < 4000
      a.push(i)
      i = i + 1
    end
    return a.size`
	// Below what this growth needs, so neither form may be admitted. A record
	// that netted off the array's new capacity let the shrinking form through.
	const quota = 380 << 10

	for _, tc := range []struct{ name, body string }{
		{"grow", grow},
		{"shrink_then_grow", "    a.pop\n" + grow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptWithConfig(t, Config{StepQuota: 500_000_000, MemoryQuotaBytes: quota},
				"def run(a)\n  driver.walk(a) do |x|\n"+tc.body+"\n  end\n  0\nend")
			_, err := script.Call(context.Background(), "run",
				[]Value{NewArray([]Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)})}, CallOptions{
					Capabilities: []CapabilityAdapter{arrayArgDriver{}},
				})
			if err == nil {
				t.Fatalf("%s was admitted under a quota it does not fit in", tc.name)
			}
		})
	}
}
