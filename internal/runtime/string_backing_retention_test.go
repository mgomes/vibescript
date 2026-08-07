package runtime

import (
	"context"
	goruntime "runtime"
	"strings"
	"testing"
	"unsafe"
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

// TestSliceDoesNotRetainItsBacking pins that String#slice stops holding the
// string it sliced.
//
// slice built its result from []rune -- which copied by construction -- until it
// moved to byte-offset slicing. normalizeInvalidUTF8 returns valid UTF-8
// unchanged, so the result aliased the receiver again and 200 one-character
// slices of a megabyte held 192 MiB under an 8 MiB quota (#50).
//
// Not parallel: it measures process-wide heap.
func TestSliceDoesNotRetainItsBacking(t *testing.T) {
	for _, tc := range []struct {
		name string
		expr string
	}{
		{"index", "big.slice(0)"},
		{"range", "big.slice(0..0)"},
		{"start and length", "big.slice(0, 1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held := retainedHeapBytes(t, retentionScript(t, tc.expr), retentionSeed(), 200)
			assertUnderRetentionLimit(t, "slices", held)
		})
	}
}

// TestChainedSlicesDoNotRetainTheOriginal pins that halving a string with
// String#slice cannot keep the first allocation alive. A copy threshold would
// never fire here -- each step keeps half -- yet every intermediate result would
// still point at the original megabyte.
//
// Not parallel: it measures process-wide heap.
func TestChainedSlicesDoNotRetainTheOriginal(t *testing.T) {
	script := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: 64 << 20},
		`def run(seed)
  kept = []
  i = 0
  while i < 40
    s = seed * 200
    while s.length > 1
      s = s.slice(0, (s.length + 1) / 2)
    end
    kept.push(s)
    i = i + 1
  end
  kept
end`)
	held := retainedHeapBytes(t, script, retentionSeed(), 40)
	if limit := int64(8 << 20); held > limit {
		t.Fatalf("40 chained one-character slices retain %.2f MiB, want under %.2f MiB",
			float64(held)/(1<<20), float64(limit)/(1<<20))
	}
}

// TestSliceKeepsItsCharacters pins that detaching the backing did not change
// what String#slice yields, including across multibyte boundaries, on invalid
// UTF-8, and for the substring selector that returns its argument rather than a
// window into the receiver.
func TestSliceKeepsItsCharacters(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run(s)
  [s.slice(0), s.slice(1), s.slice(-1), s.slice(1, 2), s.slice(1..2), s.slice(1..), s.slice(..1),
   s.slice(0, s.length), s.slice(0, 0), s.slice(s.length), s.slice(s.length, 1), s.slice(s), s.slice("zz")]
end`)
	got, err := script.Call(context.Background(), "run", []Value{NewString("aé漢bc")}, CallOptions{})
	if err != nil {
		t.Fatalf("slice failed: %v", err)
	}
	want := `["a", "é", "c", "é漢", "é漢", "é漢bc", "aé", "aé漢bc", "", nil, "", "aé漢bc", nil]`
	if got.Inspect() != want {
		t.Fatalf("slices = %s, want %s", got.Inspect(), want)
	}

	invalid, err := script.Call(context.Background(), "run", []Value{NewString("a\xffb\xfec")}, CallOptions{})
	if err != nil {
		t.Fatalf("slice over invalid UTF-8 failed: %v", err)
	}
	// The substring selector hands back its argument, so the raw invalid bytes
	// survive there while every window normalizes to replacement characters.
	wantInvalid := "[\"a\", \"�\", \"c\", \"�b\", \"�b\", \"�b�c\", \"a�\", " +
		"\"a�b�c\", \"\", nil, \"\", \"a\xffb\xfec\", nil]"
	if invalid.Inspect() != wantInvalid {
		t.Fatalf("slices over invalid UTF-8 = %s, want %s", invalid.Inspect(), wantInvalid)
	}
}

// TestPartitionComponentsDoNotRetainTheReceiver pins that a partition component
// stops holding the string it was cut from.
//
// head and tail are windows onto the receiver, so a separator near either edge
// leaves the retained side tiny while it pins the whole receiver: each shape
// below retained 192 MiB under an 8 MiB quota (#42).
//
// Not parallel: it measures process-wide heap.
func TestPartitionComponentsDoNotRetainTheReceiver(t *testing.T) {
	for _, tc := range []struct {
		name string
		expr string
	}{
		{"partition head", `("a|" + big).partition("|")[0]`},
		{"partition tail", `(big + "|a").partition("|")[2]`},
		{"rpartition head", `("a|" + big).rpartition("|")[0]`},
		{"rpartition tail", `(big + "|a").rpartition("|")[2]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held := retainedHeapBytes(t, retentionScript(t, tc.expr), retentionSeed(), 200)
			assertUnderRetentionLimit(t, "partition components", held)
		})
	}
}

// TestEmptyComponentsAreDetachedToo pins that a zero-length component does not
// keep a pointer into the string it was cut from.
//
// An empty partition component is charged nothing at all, so nothing bounds
// what it could pin; a detach that skipped it as already free would leave that
// open (#42). The heap measurements above cannot see this case because storing
// the empty string in a Value boxes it through Go's shared zero value and drops
// the pointer on the way, so this compares the backing pointers directly.
func TestEmptyComponentsAreDetachedToo(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("x", 4<<10)
	parts := []string{text[:0], text[len(text):]}
	if err := detachSubstrings(nil, text, parts, NewString(text), nil, nil, NewNil()); err != nil {
		t.Fatalf("detaching failed: %v", err)
	}
	for i, part := range parts {
		if part != "" {
			t.Fatalf("component %d = %q, want it to stay empty", i, part)
		}
		if unsafe.StringData(part) == unsafe.StringData(text) {
			t.Fatalf("empty component %d still points at its %d-byte backing", i, len(text))
		}
	}
}

// TestPartitionKeepsItsComponents pins that detaching the components did not
// change what partition and rpartition return, including a missing separator,
// an empty separator, and multibyte boundaries.
func TestPartitionKeepsItsComponents(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run(s)
  [s.partition("漢"), s.rpartition("漢"), s.partition("zz"), s.rpartition("zz"),
   s.partition(""), s.rpartition(""), s.partition(s), s.rpartition(s)]
end`)
	got, err := script.Call(context.Background(), "run", []Value{NewString("a漢b漢c")}, CallOptions{})
	if err != nil {
		t.Fatalf("partition failed: %v", err)
	}
	want := `[["a", "漢", "b漢c"], ["a漢b", "漢", "c"], ["a漢b漢c", "", ""], ["", "", "a漢b漢c"], ` +
		`["", "", "a漢b漢c"], ["a漢b漢c", "", ""], ["", "a漢b漢c", ""], ["", "a漢b漢c", ""]]`
	if got.Inspect() != want {
		t.Fatalf("partitions = %s, want %s", got.Inspect(), want)
	}
}

// TestSquishDoesNotRetainAnOversizedBuffer pins that a heavily collapsing
// squish stops holding the buffer it was built in.
//
// squish grew its builder by the receiver's length before it knew how much
// output there would be, and strings.Builder hands its whole backing array to
// the string it returns. A megabyte of padding around one character therefore
// produced a one-byte string still holding a megabyte: 200 of them held 192 MiB
// under an 8 MiB quota (#51).
//
// Not parallel: it measures process-wide heap.
func TestSquishDoesNotRetainAnOversizedBuffer(t *testing.T) {
	for _, tc := range []struct {
		name string
		expr string
	}{
		{"squish", `(big + "x").squish`},
		{"squish!", `(big + "x").squish!`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held := retainedHeapBytes(t, retentionScript(t, tc.expr), strings.Repeat(" ", 5_000), 200)
			assertUnderRetentionLimit(t, "squished strings", held)
		})
	}
}

// TestSquishReservesExactlyWhatItWrites pins squishedLen against the output
// stringSquish actually produces. A projection that drifts low is silent: the
// builder simply grows again, to twice its capacity plus the write, and the
// oversized buffer comes back (#51).
func TestSquishReservesExactlyWhatItWrites(t *testing.T) {
	t.Parallel()

	texts := []string{
		"", " ", "   ", " \n\t", "x", " x ", "hello world", "  hello \n\t world  ",
		"hello  world", "a\xff  b", " ", "a b  c   d", "a\n\n\nb",
		strings.Repeat(" ", 1000) + "x", "x" + strings.Repeat("\t", 1000),
	}
	// Random inputs over an alphabet of letters and assorted whitespace catch a
	// drift the hand-written cases above would miss.
	alphabet := []string{"a", "b", " ", "\t", "\n", " ", " ", "\xff"}
	next := uint64(1)
	for range 500 {
		var b strings.Builder
		for range 16 {
			next = next*6364136223846793005 + 1442695040888963407
			b.WriteString(alphabet[int(next>>33)%len(alphabet)])
		}
		texts = append(texts, b.String())
	}

	for _, text := range texts {
		if got, want := squishedLen(text), len(stringSquish(text)); got != want {
			t.Fatalf("squishedLen(%q) = %d, but stringSquish wrote %d bytes", text, got, want)
		}
	}
}
