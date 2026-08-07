package runtime

import (
	"context"
	"fmt"
	"testing"
)

// TestArrayShrinkDuringIterationKeepsSnapshot pins that an in-place shrink made
// from inside a block cannot alter what the driver still has to yield.
//
// Array drivers snapshot receiver.Array() before the first yield and walk that
// header while the block runs (see TestArrayMutationDuringIteration), so a
// shrink that zeroes a slot the driver has not reached yet would hand the block
// a nil that was never in the array. pop clears from the tail, which a forward
// driver has not reached; shift clears from the head, which a reverse driver
// has not reached.
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

// minStepsToDrainDuringEach returns the smallest step quota that lets a
// block-driven pop drain of an n-element array run to completion.
func minStepsToDrainDuringEach(t *testing.T, n int) int {
	t.Helper()

	src := fmt.Sprintf(`def run()
  a = []
  i = 0
  while i < %d
    a.push(i)
    i = i + 1
  end
  a.each do |x|
    a.pop
  end
  a.size
end`, n)

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

// TestShrinkDuringIterationStaysLinear pins that only the first shrink inside
// an iteration copies. The copy leaves the driver's captured header alone by
// giving the receiver a new backing, which the driver is not walking, so every
// later shrink can go back to zeroing in place. Copying on every shrink would
// be just as correct and would make this drain quadratic, so the step cost of
// the copies is what the measurement is really watching.
func TestShrinkDuringIterationStaysLinear(t *testing.T) {
	t.Parallel()

	small := minStepsToDrainDuringEach(t, 100)
	large := minStepsToDrainDuringEach(t, 400)
	// Four times the elements costs about four times the steps when one copy
	// is amortized over the drain, and about sixteen when every pop copies.
	if limit := 6 * small; large > limit {
		t.Fatalf("draining 400 elements inside each cost %d steps against %d for 100; "+
			"want at most %d, or the copy is not amortized", large, small, limit)
	}
}
