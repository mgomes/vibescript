package runtime

import (
	"context"
	goruntime "runtime"
	"strings"
	"testing"
)

// TestIsTypeAtomIdentRejectsDotsWithoutSplitting pins that deciding an atom's
// segment count costs no memory proportional to the atom.
//
// The check used to strings.Split on "." and reject more than two segments
// only afterwards, so the split materialized one string header per dot before
// the count was ever looked at. An 8 MiB run of dots, an argument that fits
// the 16 MiB default memory quota, allocated 128 MiB of Go slice backing that
// the script's memory quota never sees: it lives in the Go builtin rather than
// in the Value graph, and the atom is rejected, so nothing charges it (#17).
//
// The limit sits well below the atom's own size, so no parse that copies its
// input can meet it, and well above the kilobytes a constant-space rejection
// spends, so unrelated allocation cannot trip it.
//
// Not parallel: it measures process-wide allocation.
func TestIsTypeAtomIdentRejectsDotsWithoutSplitting(t *testing.T) {
	atom := strings.Repeat(".", 8<<20)

	var before, after goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&before)
	ident := isTypeAtomIdent(atom)
	goruntime.ReadMemStats(&after)
	if ident {
		t.Fatal("a run of dots is not a type atom")
	}

	allocated := after.TotalAlloc - before.TotalAlloc
	if limit := uint64(4 << 20); allocated > limit {
		t.Fatalf("rejecting a %d byte dotted atom allocated %.2f MiB, want under %.2f MiB",
			len(atom), float64(allocated)/(1<<20), float64(limit)/(1<<20))
	}
}

// TestOversizedIsTypeAtomIsRejectedWithoutCopyingIt pins the length bound on
// the atom: an argument far longer than any type name is turned away before
// the parse scans it or quotes it into an error.
//
// Rejection used to render the atom back with %q, so an argument with no dots
// in it at all — nothing for the segment split to multiply — still cost 24 MiB
// of unmetered Go heap at 8 MiB of input (#17).
//
// The limit is bounded the same way as the splitting regression above: under
// the atom, far over what a rejection by size costs.
//
// Not parallel: it measures process-wide allocation.
func TestOversizedIsTypeAtomIsRejectedWithoutCopyingIt(t *testing.T) {
	script := compileScriptDefault(t, "def run(atom)\n  1.is_type?(atom)\nend")
	arg := NewString(strings.Repeat("a", 8<<20))

	var before, after goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&before)
	_, err := script.Call(context.Background(), "run", []Value{arg}, CallOptions{})
	goruntime.ReadMemStats(&after)

	// Asserted before the message is touched: rendering a rejection that did
	// quote the atom back would put megabytes into the failure output.
	allocated := after.TotalAlloc - before.TotalAlloc
	if limit := uint64(4 << 20); allocated > limit {
		t.Fatalf("rejecting an 8 MiB atom allocated %.2f MiB, want under %.2f MiB",
			float64(allocated)/(1<<20), float64(limit)/(1<<20))
	}
	if err == nil {
		t.Fatal("an atom longer than any type name must be rejected")
	}
	if size := len(err.Error()); size > 4096 {
		t.Fatalf("the rejection is %d bytes long, so it carries the atom rather than describing it", size)
	}
	if !strings.Contains(err.Error(), "supports type atoms only") {
		t.Fatalf("rejection = %q, want an oversized atom error", err.Error())
	}
}
