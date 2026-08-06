package runtime

import (
	"context"
	goruntime "runtime"
	"strings"
	"testing"
)

// TestBytesliceDoesNotRetainItsBacking pins that a small byte slice stops
// holding the string it came from.
//
// A Go substring keeps its whole backing allocation alive, while the memory
// estimator prices a string by its own length. A script could therefore build
// a large string, keep a one-byte slice of it, drop the original, and be
// charged one byte for something holding megabytes: 200 such slices retained
// 192 MiB under an 8 MiB quota, none of it visible to the quota walk (#2).
//
// Not parallel: it measures process-wide heap.
func TestBytesliceDoesNotRetainItsBacking(t *testing.T) {
	script := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: 8 << 20},
		`def run(seed)
  kept = []
  i = 0
  while i < 200
    big = seed * 200
    kept.push(big.byteslice(0, 1))
    i = i + 1
  end
  kept
end`)
	seed := strings.Repeat("abcdefghij", 500)

	var before, after goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&before)
	kept, err := script.Call(context.Background(), "run", []Value{NewString(seed)}, CallOptions{})
	goruntime.GC()
	goruntime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("building the slices failed: %v", err)
	}
	if kept.Kind() != KindArray || len(kept.Array()) != 200 {
		t.Fatalf("expected 200 retained slices, got %#v", kept)
	}

	held := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	// Sharing the backings held about 192 MiB here.
	if limit := int64(16 << 20); held > limit {
		t.Fatalf("200 one-byte slices retain %.2f MiB, want under %.2f MiB",
			float64(held)/(1<<20), float64(limit)/(1<<20))
	}
	goruntime.KeepAlive(kept)
}

// TestBytesliceKeepsItsContents pins that detaching the backing did not change
// what a slice contains, at and around the copy threshold.
func TestBytesliceKeepsItsContents(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run(s)
  [s.byteslice(0, 1), s.byteslice(2, 3), s.byteslice(0, s.bytesize), s.byteslice(-2, 2)]
end`)
	got, err := script.Call(context.Background(), "run", []Value{NewString("abcdefgh")}, CallOptions{})
	if err != nil {
		t.Fatalf("byteslice failed: %v", err)
	}
	if want := `["a", "cde", "abcdefgh", "gh"]`; got.Inspect() != want {
		t.Fatalf("byteslice = %s, want %s", got.Inspect(), want)
	}
}
