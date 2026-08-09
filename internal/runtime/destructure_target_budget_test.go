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

// blockRestDestructureSource is destructureRestTargetsSource as a block
// parameter list, which walks the same targets through the block's own bind
// charge rather than through the statement path.
func blockRestDestructureSource(nested int) string {
	targets := make([]string, 0, nested+1)
	targets = append(targets, "z")
	for range nested {
		targets = append(targets, "(*a)")
	}
	return "def run(v, n)\n  t = 0\n  v.each { |" + strings.Join(targets, ", ") + "| t = t + 1 }\n  t\nend"
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

	cfg := Config{MemoryQuotaBytes: 64 << 20}
	narrow := minStepQuotaToComplete(t, cfg, src, repeatedArrayValue(nested, narrowWidth), 1, 1<<22)
	wide := minStepQuotaToComplete(t, cfg, src, repeatedArrayValue(nested, wideWidth), 1, 1<<22)

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

// The step quota and the memory quota are independent, and a block's bind charge
// is built only when memory is bounded and the block binds a rest. Taking the
// destructuring step charge from that charge therefore switched the CPU metering
// off for the memory-unlimited, steps-finite configuration the CLI runs by
// default: 256 nested rests in a block parameter list cost 12 steps over
// 20k-element values, exactly what they cost over 100-element ones (#49). The
// charge has to be installed from the execution instead.
func TestBlockParamDestructureChargesWithoutAMemoryQuota(t *testing.T) {
	t.Parallel()

	const nested, narrowWidth, wideWidth = 256, 100, 20000
	src := blockRestDestructureSource(nested)
	cfg := Config{MemoryQuotaBytes: Unlimited}
	yield := func(width int) Value {
		return NewArray([]Value{repeatedArrayValue(nested, width)})
	}

	narrow := minStepQuotaToComplete(t, cfg, src, yield(narrowWidth), 1, 1<<22)
	wide := minStepQuotaToComplete(t, cfg, src, yield(wideWidth), 1, 1<<22)

	copied := (nested - 1) * (wideWidth - narrowWidth)
	want := narrow + copied/destructureUnitsPerStep/2
	if wide < want {
		t.Errorf("with memory unlimited the same block cost %d steps over %d-element values and %d "+
			"over %d-element ones; want at least %d, one step per %d slots the bind copies",
			narrow, narrowWidth, wide, wideWidth, want, destructureUnitsPerStep)
	}
}

// nestedIndexTargetSource builds a target nested depth deep whose deepest leaf
// is an index write. That is the shape that makes the snapshot decision rescan:
// at every level the scan recurses the whole remaining subtree looking for a
// container write, and only finds one at the bottom.
func nestedIndexTargetSource(depth int) string {
	inner := "a[0], b"
	for range depth {
		inner = "(" + inner + "), b"
	}
	return "def run(v, n)\n  a = [0, 0]\n  b = 0\n  j = 0\n  while j < n\n    z, (" +
		inner + ") = v\n    j = j + 1\n  end\n  b\nend"
}

// nestedValue matches that target: an array at every level, since the scan only
// runs when the right-hand side of that level is an array.
func nestedValue(depth int) Value {
	v := NewArray([]Value{NewInt(1), NewInt(2)})
	for range depth {
		v = NewArray([]Value{v, NewInt(0)})
	}
	return NewArray([]Value{NewInt(0), v})
}

// Whether a target needs a defensive snapshot of its right-hand side is a
// property of its syntax, but it was recomputed at every level of every
// assignment, and the scan recurses over the whole remaining subtree. A target
// nested d deep therefore did O(d squared) helper visits per assignment while
// the per-level charge bills O(d), and destructuring nesting has no depth cap.
// 200 iterations over a 4000-deep target did 1,604,401,800 scan visits for a
// loop costing tens of thousands of steps; memoizing the fact per execution
// leaves 8,022,009, the one scan the whole call now pays (#49).
func TestNestedDestructureScansItsTargetOncePerCall(t *testing.T) {
	t.Parallel()

	const depth = 1000
	cfg := Config{MemoryQuotaBytes: 64 << 20}
	src := nestedIndexTargetSource(depth)

	once := minStepQuotaToComplete(t, cfg, src, nestedValue(depth), 1, 1<<26)
	repeated := minStepQuotaToComplete(t, cfg, src, nestedValue(depth), 200, 1<<26)

	// The scan visits about depth squared over two nodes. Half of the ideal
	// charge leaves room for the sub-step remainder while staying far above the
	// couple of hundred steps an uncharged scan leaves.
	wantScan := depth * depth / 2 / destructureUnitsPerStep / 2
	if once < wantScan {
		t.Errorf("one assignment over a %d-deep target cost %d steps; want at least %d, one step per "+
			"%d nodes the snapshot scan visits", depth, once, wantScan, destructureUnitsPerStep)
	}
	// Repeating the assignment must not repeat the scan. Recomputing it per
	// iteration would bill 200 scans instead of one.
	if repeated > once*10 {
		t.Errorf("1 assignment cost %d steps and 200 cost %d; the snapshot decision is syntax, so "+
			"it must be scanned once per target rather than once per assignment", once, repeated)
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
