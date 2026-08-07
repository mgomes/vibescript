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
// reserved against the memory quota before it is allocated. The copy is live
// alongside the receiver it is taken from, and an ephemeral receiver is
// reachable only from the Go stack for exactly that window, so the pre-call
// check sees no copy and the post-call check no longer sees the receiver.
//
// The quota is sized so that only the peak is over it: the ephemeral megabyte
// receiver fits on its own and so does the near-megabyte result, so an
// unreserved copy would run to completion.
func TestBytesliceCopyIsPricedBeforeItIsMade(t *testing.T) {
	t.Parallel()

	seed := strings.Repeat("abcdefghij", 500)
	const copies = 200
	script := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: 3 << 19},
		`def run(seed)
  (seed * 200).byteslice(0, seed.bytesize * 200 - 1)
end`)
	if _, err := script.Call(context.Background(), "run", []Value{NewString(seed)}, CallOptions{}); err == nil {
		t.Fatal("a copy that cannot fit beside its receiver must be rejected")
	}

	// The same receiver and the same result, one at a time, are each well under
	// the quota -- so the rejection above is the peak, not either endpoint.
	fits := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: 3 << 19},
		`def run(seed)
  (seed * 200).bytesize
end`)
	got, err := fits.Call(context.Background(), "run", []Value{NewString(seed)}, CallOptions{})
	if err != nil {
		t.Fatalf("the receiver alone must fit under the quota: %v", err)
	}
	if want := int64(len(seed) * copies); got.Int() != want {
		t.Fatalf("receiver bytesize = %d, want %d", got.Int(), want)
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

	// The same near-whole slice over an eight times longer receiver copies eight
	// times as many bytes and must cost meaningfully more. Comparing one
	// expression against itself is what isolates the copy: a wider expression
	// evaluated once would cost more for its own operators alone, whether or not
	// the bytes it copies are charged at all.
	const expr = "s.byteslice(0, s.bytesize - 1)"
	narrow := minStepsForStringOp(t, expr, 8<<10)
	wide := minStepsForStringOp(t, expr, 64<<10)
	if want := narrow + (64<<10-8<<10)/stringScanBytesPerStep; wide < want {
		t.Fatalf("copying 64 KiB cost %d steps and copying 8 KiB %d; want at least %d, one step "+
			"per %d copied bytes", wide, narrow, want, stringScanBytesPerStep)
	}
}
