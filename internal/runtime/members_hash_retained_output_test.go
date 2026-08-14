package runtime

import (
	"context"
	"fmt"
	"testing"
)

// hash.map_with_index and hash.transform_values accumulate their results into a
// Go local that no reachable root points at, so the memory checks that run
// inside their block calls could not see what the loop had already kept. A block
// allocating a large temporary was weighed on its own, and the temporary and the
// results each fit a quota their combined live footprint exceeded.
//
// The quota below sits in that gap: above either the retained results or the
// in-block transient on its own, and below their sum. It must be rejected.
func TestHashResultAccumulatorsAreWeighedAgainstInBlockTransients(t *testing.T) {
	t.Parallel()

	const entryCount = 40
	const perEntry = 2048
	const retained = entryCount * perEntry
	const transient = 200_000
	const quota = 250_000

	if quota <= transient || quota <= retained {
		t.Fatalf("quota %d must exceed the transient %d and the retained results %d on their own",
			quota, transient, retained)
	}
	if quota >= transient+retained {
		t.Fatalf("quota %d must be below the %d-byte peak it is meant to reject", quota, transient+retained)
	}

	entries := make(map[string]Value, entryCount)
	for i := range entryCount {
		entries[fmt.Sprintf("k%03d", i)] = NewInt(int64(i))
	}
	receiver := NewHash(entries)

	body := fmt.Sprintf(`big = "y" * %d; z = big.size; "x" * %d`, transient, perEntry)
	tests := []struct {
		name string
		src  string
	}{
		{"map_with_index", fmt.Sprintf("def run(h)\n  h.map_with_index { |p, i| %s }\nend", body)},
		{"transform_values", fmt.Sprintf("def run(h)\n  h.transform_values { |v| %s }\nend", body)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: quota}, tc.src)
			_, err := script.Call(context.Background(), "run", []Value{receiver}, CallOptions{})
			if err == nil {
				t.Fatalf("hash.%s admitted a build whose %d retained bytes and %d-byte in-block "+
					"transient are live together, past its %d-byte quota; the checks inside the block "+
					"are measuring a graph the accumulated results are missing from",
					tc.name, retained, transient, quota)
			}
			requireErrorContains(t, err, "memory quota exceeded")
		})
	}
}
