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

// TestChainedByteslicesDoNotRetainTheOriginal pins that repeatedly slicing a
// string down cannot keep the first allocation alive.
//
// Copying only slices below some fraction of their source looks sufficient
// until the waste composes: halving keeps at least half every time, so no
// single step trips a threshold, yet every intermediate result still points
// at the original allocation and the last one is charged a byte for it.
//
// Not parallel: it measures process-wide heap.
func TestChainedByteslicesDoNotRetainTheOriginal(t *testing.T) {
	script := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: 64 << 20},
		`def run(seed)
  kept = []
  i = 0
  while i < 40
    s = seed * 200
    while s.bytesize > 1
      s = s.byteslice(0, (s.bytesize + 1) / 2)
    end
    kept.push(s)
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
		t.Fatalf("chained slicing failed: %v", err)
	}
	if kept.Kind() != KindArray || len(kept.Array()) != 40 {
		t.Fatalf("expected 40 retained slices, got %#v", kept)
	}

	held := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	// Forty one-byte results each pinning a megabyte would be about 40 MiB.
	if limit := int64(8 << 20); held > limit {
		t.Fatalf("40 chained one-byte slices retain %.2f MiB, want under %.2f MiB",
			float64(held)/(1<<20), float64(limit)/(1<<20))
	}
	goruntime.KeepAlive(kept)
}

// TestBytesliceCopyIsPricedBeforeItIsMade pins that the detaching copy is
// reserved against the memory quota before it is allocated. The copy coexists
// with the receiver it is taken from, and an ephemeral receiver is live for
// exactly that window, so neither the pre-call nor the post-call check sees
// both of them at once.
func TestBytesliceCopyIsPricedBeforeItIsMade(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: 2 << 20},
		`def run(seed)
  s = seed * 200
  s.reverse.byteslice(0, s.bytesize - 1)
end`)
	seed := strings.Repeat("abcdefghij", 500)
	if _, err := script.Call(context.Background(), "run", []Value{NewString(seed)}, CallOptions{}); err == nil {
		t.Fatal("a copy that cannot fit beside its receiver must be rejected")
	}
}

// TestBytesliceChargesTheBytesItCopies pins that the copy is billed by what it
// extracts rather than by the string it came from. Charging the receiver would
// make a one-byte slice of a large host string cost the whole string, which is
// work byteslice never does; charging nothing would let an unmetered copy run
// in a loop.
func TestBytesliceChargesTheBytesItCopies(t *testing.T) {
	t.Parallel()

	// A one-byte slice must cost the same regardless of receiver size.
	small := minStepsForStringOp(t, "s.byteslice(0, 1)", 8<<10)
	large := minStepsForStringOp(t, "s.byteslice(0, 1)", 64<<10)
	if small != large {
		t.Fatalf("a one-byte slice cost %d steps over 8 KiB and %d over 64 KiB; the charge must "+
			"follow the copy, not the receiver", small, large)
	}

	// A slice that copies most of its receiver must cost more than that.
	wide := minStepsForStringOp(t, "s.byteslice(0, s.bytesize - 1)", 64<<10)
	if wide <= large {
		t.Fatalf("a near-whole slice cost %d steps and a one-byte slice %d; copying more must cost more",
			wide, large)
	}
}
