package runtime

import (
	"context"
	"fmt"
	goruntime "runtime"
	"strings"
	"testing"
)

// TestArrayShrinkDropsRemovedPayloads pins that an element removed from an
// array in place stops being reachable through the array it left.
//
// pop and shift shrink the receiver by reslicing its backing, and in Go a
// backing array stays live as a whole while any slice into it is live. An
// element parked in a slot outside the new length is therefore still held by
// the receiver, while the estimator charges the array's structure by capacity
// and recurses only into the visible len(values) range. Popping a megabyte
// string off each of 200 arrays and keeping the emptied arrays held 192 MiB
// under an 8 MiB quota, none of it visible to the quota walk (#22).
//
// The heap is sampled from inside the call, while the arrays are still
// script-reachable: Script.Call deep-clones its result for the host and the
// clone rebuilds each array from its visible elements, which would drop the
// stranded payloads before the host could ever see them.
//
// Not parallel: it measures process-wide heap.
func TestArrayShrinkDropsRemovedPayloads(t *testing.T) {
	cases := []struct {
		name   string
		holder string
		remove string
	}{
		{"pop", "[0, seed * 200]", "holder.pop"},
		{"pop_count", "[0, seed * 200]", "holder.pop(1)"},
		{"shift", "[seed * 200, 0]", "holder.shift"},
		{"shift_count", "[seed * 200, 0]", "holder.shift(1)"},
	}
	seed := strings.Repeat("abcdefghij", 500)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var samples []uint64
			engine := MustNewEngine(Config{StepQuota: 50_000_000, MemoryQuotaBytes: 8 << 20})
			engine.RegisterBuiltin("probe_heap", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
				var stats goruntime.MemStats
				goruntime.GC()
				goruntime.ReadMemStats(&stats)
				samples = append(samples, stats.HeapAlloc)
				return NewNil(), nil
			})
			// The second holder slot keeps the surviving element between the
			// removed one and the end of the backing, so the receiver's slice
			// still points inside the allocation after the shrink.
			script, err := engine.Compile(fmt.Sprintf(`def run(seed)
  kept = []
  i = 0
  probe_heap()
  while i < 200
    holder = %s
    %s
    kept.push(holder)
    i = i + 1
  end
  probe_heap()
  kept.size
end`, tc.holder, tc.remove))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}

			got, err := script.Call(context.Background(), "run", []Value{NewString(seed)}, CallOptions{})
			if err != nil {
				t.Fatalf("%s loop failed: %v", tc.name, err)
			}
			if got.Int() != 200 {
				t.Fatalf("kept %d arrays, want 200", got.Int())
			}
			if len(samples) != 2 {
				t.Fatalf("probe_heap ran %d times, want 2", len(samples))
			}

			held := int64(samples[1]) - int64(samples[0])
			// Retaining every removed string held about 192 MiB here.
			if limit := int64(16 << 20); held > limit {
				t.Fatalf("200 emptied arrays retain %.2f MiB, want under %.2f MiB",
					float64(held)/(1<<20), float64(limit)/(1<<20))
			}
		})
	}
}

// TestArrayShrinkKeepsSurvivingElements pins that zeroing the vacated slots
// does not disturb the elements the receiver keeps, the values pop and shift
// hand back, or the slots a later push reuses.
func TestArrayShrinkKeepsSurvivingElements(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  a = [1, 2, 3, 4]
  popped = a.pop
  tail = a.pop(2)
  shifted = [5, 6, 7, 8]
  first = shifted.shift
  head = shifted.shift(2)
  a.push(9)
  shifted.push(10)
  [a, popped, tail, shifted, first, head]
end`)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := `[[1, 9], 4, [2, 3], [8, 10], 5, [6, 7]]`; got.Inspect() != want {
		t.Fatalf("run = %s, want %s", got.Inspect(), want)
	}
}

// TestReleasedClaimDropsItsHeader pins that the claim stack stops holding an
// array once the frame that walked it has returned.
//
// A claim carries the header it walks so a detached one can still be charged,
// which makes the stack itself a place where the bug this file is about can
// recur: shortening it would leave those pointers in its backing, keeping the
// array reachable from the execution while the walk over the live claims stops
// counting it. One iteration over a 48 MiB array, dropped straight afterwards,
// held all of it.
//
// The heap is sampled from inside the call. A released slot is overwritten by
// the next claim taken at the same depth, and the execution is gone by the time
// Call returns, so nothing is visible from outside.
//
// Not parallel: it measures process-wide heap.
func TestReleasedClaimDropsItsHeader(t *testing.T) {
	var samples []uint64
	engine := MustNewEngine(Config{StepQuota: 50_000_000, MemoryQuotaBytes: Unlimited})
	engine.RegisterBuiltin("probe_heap", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		var stats goruntime.MemStats
		goruntime.GC()
		goruntime.ReadMemStats(&stats)
		samples = append(samples, stats.HeapAlloc)
		return NewNil(), nil
	})
	// probe_heap is a global builtin, so it claims nothing and cannot overwrite
	// the slot each's claim was released from.
	script, err := engine.Compile(`def run(seed)
  probe_heap()
  a = []
  i = 0
  while i < 100
    a.push(seed * 100)
    i = i + 1
  end
  a.each do |x|
  end
  a = nil
  probe_heap()
  0
end`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	seed := NewString(strings.Repeat("abcdefghij", 500))
	if _, err := script.Call(context.Background(), "run", []Value{seed}, CallOptions{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("probe_heap ran %d times, want 2", len(samples))
	}

	held := int64(samples[1]) - int64(samples[0])
	// A stale claim held about 48 MiB here.
	if limit := int64(16 << 20); held > limit {
		t.Fatalf("a dropped array is still held to %.2f MiB, want under %.2f MiB",
			float64(held)/(1<<20), float64(limit)/(1<<20))
	}
}

// TestWildcardShrinkReclaimsOnRelease pins that a shrink beneath a host-driven
// frame still frees what it removed, once the frame that may have been walking
// it is done.
//
// Such a shrink cannot clear the slots it vacates while the frame is live, and
// cannot move the array onto a copy either, since the frame can be handed the
// copy back. It leaves the storage alone and narrows the array over it, so the
// clearing is deferred to the moment the claim drops. Without that the removed
// payloads would sit in the backing, reachable through the array and invisible
// to the quota, which is the retention this whole file is about.
//
// The heap is sampled after the host call returns but while the array is still
// script-reachable, which is the only window where the difference shows.
//
// Not parallel: it measures process-wide heap.
func TestWildcardShrinkReclaimsOnRelease(t *testing.T) {
	var samples []uint64
	engine := MustNewEngine(Config{StepQuota: 50_000_000, MemoryQuotaBytes: Unlimited})
	engine.RegisterBuiltin("probe_heap", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		var stats goruntime.MemStats
		goruntime.GC()
		goruntime.ReadMemStats(&stats)
		samples = append(samples, stats.HeapAlloc)
		return NewNil(), nil
	})
	script, err := engine.Compile(`def run(seed)
  a = []
  i = 0
  while i < 100
    a.push(seed * 100)
    i = i + 1
  end
  probe_heap()
  driver.walk(a) do |x|
    a.pop(a.size)
  end
  probe_heap()
  a.size
end`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	seed := NewString(strings.Repeat("abcdefghij", 500))
	got, err := script.Call(context.Background(), "run", []Value{seed}, CallOptions{
		Capabilities: []CapabilityAdapter{arrayArgDriver{}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.Int() != 0 {
		t.Fatalf("array holds %d elements after the drain, want 0", got.Int())
	}
	if len(samples) != 2 {
		t.Fatalf("probe_heap ran %d times, want 2", len(samples))
	}

	// The fifty megabytes the drain removed must be gone once the claim drops.
	freed := int64(samples[0]) - int64(samples[1])
	if want := int64(32 << 20); freed < want {
		t.Fatalf("the drain released %.2f MiB, want at least %.2f MiB",
			float64(freed)/(1<<20), float64(want)/(1<<20))
	}
}
