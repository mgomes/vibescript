package runtime

import (
	"fmt"
	"strings"
	"testing"
)

// This file belongs to the unfinished retained-output basis change. It measures
// the cost that rules out the one-line fix applied alongside it, so whoever picks
// the work up starts from the number rather than from the idea.
//
// The defect: liveBaseline subtracts retainedAtStart, taken over the full graph,
// from a later reading that the region memo serves over the region PREFIX alone.
// Inside a block-iteration region the two are different bases, and a payload the
// enclosing block's scope holds -- present in the full graph, absent from the
// prefix -- reads as fresh growth on top of a baseline that already carries it.
//
// The one-line fix in memory_output.go stops serving the region memo for that
// reading, so both sides are on the full-graph basis. It is correct: a nested
// rest-binding lookup's surcharge over the flat spelling drops from one whole
// payload to a constant 561 bytes. It is also unaffordable, because the memo is
// exactly what keeps this path linear.
//
// The tension is structural, not incidental. Memoizing the roots requires
// committing them at a fixed point in the walk order, before the suffix that
// varies every iteration; putting them on the full-graph basis requires walking
// them AFTER that suffix. One committed state cannot do both.
//
// Two directions that would work:
//
//   - Reconstruct it: outputs_A = outputs_B + suffix_after_outputs - suffix_alone.
//     Exact, and linear, since the suffix is bounded by the block rather than by
//     the collection. It needs suffix_alone, the suffix walked against a
//     prefix-only state, which the single shared estimator does not keep.
//
//   - Stop reconstructing a live baseline from a frozen snapshot plus deltas and
//     read the session total instead. It is already computed correctly on every
//     memory check and is basis-free by construction, being a deduplicated union
//     rather than a difference.

// restBindingLookupKeys builds count array keys, so a values_at whose default proc
// destructures with a named rest misses on every one of them.
func restBindingLookupKeys(count int) string {
	keys := make([]string, count)
	for i := range keys {
		keys[i] = fmt.Sprintf("[%d, %d]", i, i)
	}
	return strings.Join(keys, ", ")
}

// A rest-binding callback is the only shape that reaches liveBaseline, so it is
// the only shape whose cost the basis fix can change. On the branch this file sits
// on it is quadratic; without the fix it is linear.
//
//	misses   linear (no fix)   with the fix
//	   200             9,642         95,634
//	   400            19,198        351,154
//	   800            38,292      1,342,242
//	growth      1.99x per doubling   3.8x
//
// Deliberately not parallel: the estimator visit counter is process-wide.
func TestRestBindingLookupStaysLinear(t *testing.T) {
	if estimatorVerify {
		t.Skip("the estimator oracle recomputes a reference walk per check, which is deliberately quadratic")
	}
	const small, large = 200, 400
	tmpl := "def run()\n  h = Hash.new { |g, (head, *tail)| 1 }\n  h.values_at(%s).length\nend"
	atSmall := lookupEstimatorVisits(t, fmt.Sprintf(tmpl, restBindingLookupKeys(small)))
	atLarge := lookupEstimatorVisits(t, fmt.Sprintf(tmpl, restBindingLookupKeys(large)))

	// Linear growth is 2x for a doubling; the headroom keeps this on the
	// complexity class rather than the exact count.
	if atLarge > atSmall*3 {
		t.Fatalf("a rest-binding lookup visited %d estimator nodes for %d misses and %d for %d, a %.1fx "+
			"rise for a doubling; putting the retained-output reading on the full-graph basis costs a "+
			"full graph walk per bind, which is the trade this change has to avoid",
			atSmall, small, atLarge, large, float64(atLarge)/float64(atSmall))
	}
}
