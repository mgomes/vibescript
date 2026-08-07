package runtime

import (
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
