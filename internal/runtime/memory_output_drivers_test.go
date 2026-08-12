package runtime

import (
	"context"
	"fmt"
	"testing"
)

// The drivers below accumulate their results into a Go local that no reachable
// root points at, so the memory checks that run inside their callbacks reach
// those results only through a registered output walk root (see
// memory_output.go). The two tests here pin the two directions that root has to
// get right, because the mechanisms that preceded it each got one right and the
// other wrong: pricing the output as scratch measured the peak correctly but
// double-charged a result the callback also stored somewhere reachable, while
// not pricing it at all avoided the double charge and lost the peak.
const (
	outputDriverEntries  = 40
	outputDriverPayload  = 2048
	outputDriverRetained = outputDriverEntries * outputDriverPayload
)

func outputDriverReceivers(t *testing.T) (Value, Value) {
	t.Helper()
	arr := make([]Value, outputDriverEntries)
	for i := range outputDriverEntries {
		arr[i] = NewInt(int64(i))
	}
	entries := make(map[string]Value, outputDriverEntries)
	for i := range outputDriverEntries {
		entries[fmt.Sprintf("k%03d", i)] = NewInt(int64(i))
	}
	return NewArray(arr), NewHash(entries)
}

// A callback that keeps its own result -- memoizing it into a reachable
// container and returning it -- hands the same bytes to two views: the driver's
// Go-local output and the reachable graph. A walk root deduplicates them on
// identity, so they are charged once.
//
// Reserving the output as scratch could not: the reservation reaches a check
// through estimateScalarBase as a plain byte count while the graph walk reaches
// the callback's own copy, and nothing relates the two. That charged this shape
// about twice its real cost, and the quota below sits in the gap -- comfortably
// above what the retained bytes actually need and comfortably below twice it, so
// a driver that double-charges rejects a script whose live memory always fit.
func TestMemoizingCallbackIsNotChargedTwice(t *testing.T) {
	t.Parallel()

	arrayReceiver, hashReceiver := outputDriverReceivers(t)
	const quota = 130_000

	body := fmt.Sprintf(`s = "x" * %d; memo.push(s); s`, outputDriverPayload)
	tests := []struct {
		name     string
		src      string
		receiver Value
	}{
		{"array.map", fmt.Sprintf("def run(src)\n  memo = []\n  src.map { |i| %s }\nend", body), arrayReceiver},
		{"hash.map", fmt.Sprintf("def run(h)\n  memo = []\n  h.map { |k, v| %s }\nend", body), hashReceiver},
		{"hash.map_with_index", fmt.Sprintf("def run(h)\n  memo = []\n  h.map_with_index { |p, i| %s }\nend", body), hashReceiver},
		{"hash.transform_values", fmt.Sprintf("def run(h)\n  memo = []\n  h.transform_values { |v| %s }\nend", body), hashReceiver},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: quota}, tc.src)
			if _, err := script.Call(context.Background(), "run", []Value{tc.receiver}, CallOptions{}); err != nil {
				t.Fatalf("%s rejected a build whose %d retained bytes fit the %d-byte quota: %v; "+
					"a result the callback both returned and stored is being charged once per view "+
					"instead of once per identity", tc.name, outputDriverRetained, quota, err)
			}
		})
	}
}

// The other direction. A callback that allocates a large transient while the
// driver already holds a long output is live for both at once, so the quota has
// to see their sum. Without a registered root the in-block checks measured a
// graph the output was missing from, and the transient and the output each fit a
// quota their sum exceeds.
//
// The quota below sits in that gap: above either one alone, below their sum. It
// must be rejected.
func TestRetainedOutputIsWeighedAgainstInBlockTransients(t *testing.T) {
	t.Parallel()

	arrayReceiver, hashReceiver := outputDriverReceivers(t)
	const transient = 200_000
	const quota = 250_000

	if quota <= transient || quota <= outputDriverRetained {
		t.Fatalf("quota %d must exceed the transient %d and the retained output %d on their own",
			quota, transient, outputDriverRetained)
	}
	if quota >= transient+outputDriverRetained {
		t.Fatalf("quota %d must be below the %d-byte peak it is meant to reject",
			quota, transient+outputDriverRetained)
	}

	body := fmt.Sprintf(`big = "y" * %d; z = big.size; "x" * %d`, transient, outputDriverPayload)
	tests := []struct {
		name     string
		src      string
		receiver Value
	}{
		{"array.map", fmt.Sprintf("def run(src)\n  src.map { |i| %s }\nend", body), arrayReceiver},
		{"hash.map", fmt.Sprintf("def run(h)\n  h.map { |k, v| %s }\nend", body), hashReceiver},
		{"hash.map_with_index", fmt.Sprintf("def run(h)\n  h.map_with_index { |p, i| %s }\nend", body), hashReceiver},
		{"hash.transform_values", fmt.Sprintf("def run(h)\n  h.transform_values { |v| %s }\nend", body), hashReceiver},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: quota}, tc.src)
			_, err := script.Call(context.Background(), "run", []Value{tc.receiver}, CallOptions{})
			if err == nil {
				t.Fatalf("%s admitted a build whose %d retained bytes and %d-byte in-block transient "+
					"are live together, past its %d-byte quota; the checks inside the callback are "+
					"measuring a graph the retained output is missing from",
					tc.name, outputDriverRetained, transient, quota)
			}
			requireErrorContains(t, err, "memory quota exceeded")
		})
	}
}
