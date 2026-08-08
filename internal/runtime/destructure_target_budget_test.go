package runtime

import (
	"strings"
	"testing"
)

// destructureRestTargetsSource builds one assignment statement whose targets are
// a leading name followed by nested rest captures. Each nested capture copies
// its own window out of the value it is handed, so the statement's work is the
// target count times the width of those values. The leading name is required
// only because a statement cannot begin with a nested target group.
func destructureRestTargetsSource(nested int) string {
	targets := make([]string, 0, nested+1)
	targets = append(targets, "z")
	for range nested {
		targets = append(targets, "(*a)")
	}
	return "def run(v, n)\n  " + strings.Join(targets, ", ") + " = v\n  a.length\nend"
}

// repeatedArrayValue returns an array of count references to one width-element
// array, so the value's own footprint is a single backing however many targets
// read it: what grows is the copying, not the memory the quota can see.
func repeatedArrayValue(count, width int) Value {
	row := make([]Value, count)
	inner := loopMemoArray(width)
	for i := range row {
		row[i] = inner
	}
	return NewArray(row)
}

// A destructuring assignment is one statement but arbitrarily many targets, and
// every nested rest copies its own window. Nothing charged the step quota or
// polled the context while that ran, so one statement could copy a large
// host-supplied array once per nested target: 256 nested rests over a repeated
// 20k-element array cost 5 steps, exactly what the same statement cost over a
// 100-element one, while doing 5,074,500 slot copies (#49). The charge is
// amortized, so the cost has to follow the slots copied.
func TestNestedRestDestructureChargesTheSlotsItCopies(t *testing.T) {
	t.Parallel()

	const nested, narrowWidth, wideWidth = 256, 100, 20000
	src := destructureRestTargetsSource(nested)

	narrow := minStepQuotaToComplete(t, src, repeatedArrayValue(nested, narrowWidth), 1, 1<<22)
	wide := minStepQuotaToComplete(t, src, repeatedArrayValue(nested, wideWidth), 1, 1<<22)

	// The last target reads past the end of the value and copies nothing, so
	// nested-1 targets copy the extra width. Half the ideal charge leaves room
	// for the residue the last partial step carries, while staying far above
	// the zero an uncharged copy produces.
	copied := (nested - 1) * (wideWidth - narrowWidth)
	want := narrow + copied/destructureUnitsPerStep/2
	if wide < want {
		t.Errorf("the same statement cost %d steps over %d-element values and %d over %d-element ones; "+
			"want at least %d, one step per %d slots the assignment copies",
			narrow, narrowWidth, wide, wideWidth, want, destructureUnitsPerStep)
	}
}

// The charge is amortized so that everyday destructuring keeps costing what it
// did: a handful of targets and a short rest window round to no steps at all.
// Pin that such an assignment still binds what Ruby's rules say it does inside
// the default profile's quotas.
func TestOrdinaryDestructureStaysWithinDefaultQuotas(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, "def run()\n  a, b, *rest = [1, 2, 3, 4, 5]\n"+
		"  e, (c, d) = [8, [6, 7]]\n  [a, b, rest.length, rest[0], c, d, e]\nend")
	got := callFunc(t, script, "run", nil)
	compareArrays(t, got, []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(3), NewInt(6), NewInt(7), NewInt(8)})
}
