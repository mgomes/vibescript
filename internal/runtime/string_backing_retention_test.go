package runtime

import (
	"context"
	goruntime "runtime"
	"strings"
	"testing"
)

// retainedHeapBytes runs script's `run` function over seed and reports how much
// process heap the values it returned still hold after a collection. The script
// must return an array of wantKept elements so a run that quietly produced
// nothing cannot pass for a run that produced detached slices.
//
// Callers are not parallel: this measures process-wide heap.
func retainedHeapBytes(t *testing.T, script *Script, seed string, wantKept int) int64 {
	t.Helper()

	var before, after goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&before)
	kept, err := script.Call(context.Background(), "run", []Value{NewString(seed)}, CallOptions{})
	goruntime.GC()
	goruntime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("building the slices failed: %v", err)
	}
	if kept.Kind() != KindArray || len(kept.Array()) != wantKept {
		t.Fatalf("expected %d retained slices, got %#v", wantKept, kept)
	}
	held := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	goruntime.KeepAlive(kept)
	return held
}

// retentionScript wraps expr in a loop that builds a megabyte string, keeps
// only what expr extracts from it, and drops the rest.
func retentionScript(t *testing.T, expr string) *Script {
	t.Helper()

	return compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: 8 << 20},
		`def run(seed)
  kept = []
  i = 0
  while i < 200
    big = seed * 200
    kept.push(`+expr+`)
    i = i + 1
  end
  kept
end`)
}

func retentionSeed() string {
	return strings.Repeat("abcdefghij", 500)
}

// assertUnderRetentionLimit fails when the kept values hold more heap than an
// honestly-priced set of them ever could. Every sink here held about 192 MiB
// before it was fixed, so 16 MiB separates the two outcomes by an order of
// magnitude while leaving room for allocator noise.
func assertUnderRetentionLimit(t *testing.T, what string, held int64) {
	t.Helper()

	if limit := int64(16 << 20); held > limit {
		t.Fatalf("200 tiny %s retain %.2f MiB, want under %.2f MiB",
			what, float64(held)/(1<<20), float64(limit)/(1<<20))
	}
}

// TestBracketReadsDoNotRetainTheirBacking pins that str[...] stops holding the
// string it sliced.
//
// Every bracket form returns a slice of the receiver, so retaining `(big +
// "x")[0]` was charged about one byte while pinning the whole backing: 200 such
// reads held 192 MiB under an 8 MiB quota (#36).
//
// Not parallel: it measures process-wide heap.
func TestBracketReadsDoNotRetainTheirBacking(t *testing.T) {
	for _, tc := range []struct {
		name string
		expr string
	}{
		{"index", "big[0]"},
		{"range", "big[0..0]"},
		{"start and length", "big[0, 1]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held := retainedHeapBytes(t, retentionScript(t, tc.expr), retentionSeed(), 200)
			assertUnderRetentionLimit(t, "bracket reads", held)
		})
	}
}

// TestChainedBracketReadsDoNotRetainTheOriginal pins that halving a string with
// bracket reads cannot keep the first allocation alive. Copying only slices
// below some fraction of their source would never fire here, yet every
// intermediate result would still point at the original megabyte.
//
// Not parallel: it measures process-wide heap.
func TestChainedBracketReadsDoNotRetainTheOriginal(t *testing.T) {
	script := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: 64 << 20},
		`def run(seed)
  kept = []
  i = 0
  while i < 40
    s = seed * 200
    while s.length > 1
      s = s[0, (s.length + 1) / 2]
    end
    kept.push(s)
    i = i + 1
  end
  kept
end`)
	held := retainedHeapBytes(t, script, retentionSeed(), 40)
	if limit := int64(8 << 20); held > limit {
		t.Fatalf("40 chained one-character reads retain %.2f MiB, want under %.2f MiB",
			float64(held)/(1<<20), float64(limit)/(1<<20))
	}
}

// TestBracketReadsKeepTheirCharacters pins that detaching the backing did not
// change what a bracket read yields, including across multibyte boundaries and
// on invalid UTF-8.
func TestBracketReadsKeepTheirCharacters(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run(s)
  [s[0], s[1], s[-1], s[1, 2], s[1..2], s[1..], s[..1], s[0, s.length], s[0, 0], s[s.length], s[s.length, 1]]
end`)
	got, err := script.Call(context.Background(), "run", []Value{NewString("aé漢bc")}, CallOptions{})
	if err != nil {
		t.Fatalf("bracket read failed: %v", err)
	}
	want := `["a", "é", "c", "é漢", "é漢", "é漢bc", "aé", "aé漢bc", "", nil, ""]`
	if got.Inspect() != want {
		t.Fatalf("bracket reads = %s, want %s", got.Inspect(), want)
	}

	// Invalid UTF-8 still normalizes to replacement characters exactly as before.
	invalid, err := script.Call(context.Background(), "run", []Value{NewString("a\xffb\xfec")}, CallOptions{})
	if err != nil {
		t.Fatalf("bracket read over invalid UTF-8 failed: %v", err)
	}
	wantInvalid := `["a", "�", "c", "�b", "�b", "�b�c", "a�", "a�b�c", "", nil, ""]`
	if invalid.Inspect() != wantInvalid {
		t.Fatalf("bracket reads over invalid UTF-8 = %s, want %s", invalid.Inspect(), wantInvalid)
	}
}
