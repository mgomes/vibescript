package runtime

import (
	"context"
	"testing"
)

// literalEntryFor builds the key and entry a hash literal pair produces, so a
// test can drive the build accumulator the way evalHashLiteralWithValueTypes
// does.
func literalEntryFor(t *testing.T, name string, val Value) (string, hashLiteralEntry) {
	t.Helper()

	key, err := hashKeyString(NewSymbol(name))
	if err != nil {
		t.Fatalf("key for %q: %v", name, err)
	}
	return key, hashLiteralEntry{key: key, value: val}
}

// buildLiteralThroughReplacement drives `{a: 1, a: 2, b: 3}`: one distinct key,
// a duplicate that switches the accumulator into replacement accounting, and
// then a key it has not seen, which is the pair whose entry structure is added
// after admission. It reports whether the build was admitted and, if so, what
// the accumulator itself then says it is holding.
func buildLiteralThroughReplacement(t *testing.T, quota int) (admitted bool, used int) {
	t.Helper()

	exec := &Execution{ctx: context.Background(), memoryQuota: quota}
	acc, err := newHashLiteralBuildAccumulator(exec)
	if err != nil {
		return false, 0
	}
	if !acc.sessions {
		t.Fatal("the accumulator took snapshot mode; this test drives the sessions admission path")
	}
	if err := acc.reserveBacking(3); err != nil {
		return false, 0
	}

	current := map[string]hashLiteralEntry{}
	aKey, aEntry := literalEntryFor(t, "a", NewInt(1))
	if err := acc.addDistinctEntry(current, aKey, aEntry.value); err != nil {
		return false, 0
	}
	current[aKey] = aEntry

	_, aReplacement := literalEntryFor(t, "a", NewInt(2))
	if err := acc.replaceEntry(aKey, aReplacement.value, current); err != nil {
		return false, 0
	}
	current[aKey] = aReplacement

	bKey, bEntry := literalEntryFor(t, "b", NewInt(3))
	if err := acc.replaceEntry(bKey, bEntry.value, current); err != nil {
		return false, 0
	}
	current[bKey] = bEntry

	held, _ := acc.sessionUsedBytes(current, nil)
	return true, held
}

// Once a duplicate key switches the literal into replacement accounting, every
// later key goes through replaceEntry, including ones the literal has not seen.
// Those add an entry, and its structural bytes were added to the running total
// only after admission had already passed. Every pair but the last is caught by
// the next pair's check; the last one is not checked again at all, so a quota
// with less headroom than one entry's structure admitted a build that lands
// over it. addDistinctEntry, the neighboring path, folds the same bytes into
// what it weighs (#1).
//
// Sweeping the quota across the admission boundary is what makes this precise:
// at every quota the build is admitted at, the accumulator's own measure of
// what it now holds must fit.
func TestSessionLiteralAdmissionWeighsNewEntryStructure(t *testing.T) {
	t.Parallel()

	over, _ := buildLiteralThroughReplacement(t, 1<<20)
	if !over {
		t.Fatal("the build was refused under a 1 MiB quota; the fixture no longer fits")
	}

	// One entry's structure either side of wherever the boundary falls, so the
	// sweep straddles it whatever the estimator's constants are.
	_, held := buildLiteralThroughReplacement(t, 1<<20)
	structure := estimatedMapEntryStructuralBytes + estimatedStringHeaderBytes
	admittedAny := false
	for quota := held - 2*structure; quota <= held+structure; quota++ {
		admitted, used := buildLiteralThroughReplacement(t, quota)
		if !admitted {
			continue
		}
		admittedAny = true
		if used > quota {
			t.Fatalf("a %d byte quota admitted a literal the accumulator then measures at %d bytes; "+
				"admission must weigh the entry structure it is about to add", quota, used)
		}
	}
	if !admittedAny {
		t.Fatalf("no quota in the swept range admitted the build; the sweep missed the boundary "+
			"(measured %d bytes held at 1 MiB)", held)
	}
}
