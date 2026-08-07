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
