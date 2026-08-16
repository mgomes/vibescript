package parser

import (
	"runtime"
	"strings"
	"testing"
)

// Parsing runs before any sandbox exists: no step quota, no memory quota, and
// no cancellation applies to it, and the only bound on the input is the
// embedder's source-size limit. The parse-error cap is what keeps a source
// full of mistakes from turning into unbounded diagnostic work, so the cap has
// to be reached before any of that work happens.

// capOverflowSource builds a module whose name is large and whose body repeats
// a rejected declaration, so every declaration past the parse-error cap is a
// discarded diagnostic that would quote that name three times.
func capOverflowSource(name string, declarations int) string {
	var b strings.Builder
	b.Grow(len(name) + declarations*12 + 16)
	b.WriteString("module M")
	b.WriteString(name)
	b.WriteString("\n")
	for range declarations {
		b.WriteString("  def d\n  end\n")
	}
	b.WriteString("end\n")
	return b.String()
}

// parseAllocatedBytes reports the bytes one parse allocates. It reads
// process-wide allocation, so it must not run in parallel with anything else.
func parseAllocatedBytes(source string) int64 {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, errs := Parse(source)
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(errs)
	return int64(after.TotalAlloc - before.TotalAlloc)
}

// discardedErrorBytes reports what a run of declarations past the parse-error
// cap costs, holding everything but the length of the quoted name fixed. The
// declarations themselves still cost ordinary parse work, so the measurement
// is a difference against the same source stopping at the cap.
func discardedErrorBytes(name string, discarded int) int64 {
	atCap := parseAllocatedBytes(capOverflowSource(name, maxParseErrors))
	pastCap := parseAllocatedBytes(capOverflowSource(name, maxParseErrors+discarded))
	return pastCap - atCap
}

// TestParseErrorsPastTheCapAllocateNothingPerError pins that a diagnostic the
// cap will discard costs no diagnostic work. Formatting the message before
// consulting the cap made every extra mistake pay for another copy of whatever
// source text the message quotes, so a single large identifier turned a
// bounded error list into unbounded allocation at parse time.
func TestParseErrorsPastTheCapAllocateNothingPerError(t *testing.T) {
	const discarded = 1000
	const name = 100_000

	short := discardedErrorBytes(strings.Repeat("A", 8), discarded)
	long := discardedErrorBytes(strings.Repeat("A", name), discarded)

	// Both runs discard the same number of errors and parse the same number of
	// declarations; only the length of the name the discarded message would
	// have quoted differs. A discarded error that costs anything proportional
	// to that name shows up here as the whole difference.
	if long > short+name {
		t.Fatalf("%d discarded parse errors cost %d bytes against a %d-byte name and %d against a short one",
			discarded, long, name, short)
	}
}

// TestParseErrorMessagesBoundQuotedSourceText pins the other half: the errors
// the cap does keep quote a bounded amount of source. A full error list, each
// message quoting a large identifier in full, is a large allocation the cap
// alone does not bound.
func TestParseErrorMessagesBoundQuotedSourceText(t *testing.T) {
	t.Parallel()

	name := strings.Repeat("A", 100_000)
	_, errs := Parse(capOverflowSource(name, maxParseErrors))
	if len(errs) == 0 {
		t.Fatal("expected parse errors")
	}
	for i, err := range errs {
		if len(err.Error()) > maxDiagnosticSourceBytes*8 {
			t.Fatalf("error %d is %d bytes; a diagnostic must not quote an identifier in full", i, len(err.Error()))
		}
	}
}
