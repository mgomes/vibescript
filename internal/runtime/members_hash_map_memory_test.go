package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// The result backing was charged twice. Reserving it before building the
// accumulator put it in the accumulator's baseline -- which reads
// exec.reservedScratchBytes -- and reserveSlots and every cap(out) projection
// then added it again, so a build whose receiver, scratch, and one backing all
// fit the quota was rejected anyway.
func TestHashMapDoesNotChargeTheResultBackingTwice(t *testing.T) {
	t.Parallel()

	entries := map[string]Value{}
	for i := range 400 {
		entries[fmt.Sprintf("k%03d", i)] = NewInt(int64(i))
	}
	receiver := NewHash(entries)

	// Each quota sits between the double-charged threshold and the correct
	// one: the 400-slot backing is about 6.4KB, and charging it twice moved
	// every threshold by that much.
	tests := []struct {
		name  string
		body  string
		quota int
	}{
		{name: "separate key and value", body: "h.map { |k, v| v }", quota: 66000},
		{name: "collapsed pair", body: "h.map { |p| p }", quota: 135000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: tc.quota},
				"def run(h)\n  "+tc.body+"\nend")
			if _, err := script.Call(context.Background(), "run", []Value{receiver}, CallOptions{}); err != nil {
				t.Fatalf("%s: a build that fits the quota was rejected: %v", tc.name, err)
			}
		})
	}
}

// A bind charge snapshots its baseline once, at construction. A driver that
// raises its scratch reservation while iterating -- an incremental result
// backing -- was therefore weighed against a stale baseline by every later
// call, so a tail bound late was checked as if nothing had accumulated.
func TestBindChargeSeesReservationGrowth(t *testing.T) {
	t.Parallel()

	exec := &Execution{memoryQuota: 1 << 20}
	charge := &blockBindCharge{exec: exec, baseline: 1000, reservedAtStart: exec.reservedScratchBytes}

	if got := charge.liveBaseline(); got != 1000 {
		t.Fatalf("liveBaseline with no growth = %d, want the construction baseline 1000", got)
	}

	exec.reserveLoopScratch(4096)
	if got := charge.liveBaseline(); got != 1000+4096 {
		t.Fatalf("liveBaseline after a 4096-byte reservation = %d, want %d", got, 1000+4096)
	}

	// Bytes the charge's own baseline already carries are not counted twice.
	charge.noteSelfReservation(4096)
	if got := charge.liveBaseline(); got != 1000 {
		t.Fatalf("liveBaseline with a self-reservation = %d, want the construction baseline 1000", got)
	}

	charge.noteSelfReservation(0)
	exec.releaseLoopScratch(4096)
	if got := charge.liveBaseline(); got != 1000 {
		t.Fatalf("liveBaseline after release = %d, want the construction baseline 1000", got)
	}
}

// blockPositionalArity treats a lone rest parameter as arity 1 so a hash
// iterator yields it the collapsed pair, matching Ruby. That branch is
// unreachable today because the grammar has no top-level rest parameter for
// blocks or lambdas; this pins that, so the branch becomes live only when the
// grammar deliberately changes.
func TestBlockRestParametersAreNotParseable(t *testing.T) {
	t.Parallel()

	for _, src := range []string{
		"({a: 1}).map { |*args| args }",
		"({a: 1}).map { |k, *rest| [k, rest] }",
		"->(*args) { args }",
	} {
		t.Run(src, func(t *testing.T) {
			t.Parallel()
			engine := MustNewEngine(Config{})
			_, err := engine.Compile("def run()\n  " + src + "\nend")
			if err == nil {
				t.Fatalf("%s now parses: blockPositionalArity's lone-rest branch is live and needs coverage", src)
			}
			if !strings.Contains(err.Error(), "expected block parameter") {
				t.Fatalf("%s failed with %v, want the block-parameter parse error", src, err)
			}
		})
	}
}

// The destructuring forms the grammar does allow already match Ruby exactly.
func TestDestructuringBlockParametersMatchRuby(t *testing.T) {
	t.Parallel()

	tests := []struct {
		body string
		want string
	}{
		{body: "({a: 1}).map { |(k, v)| [k, v] }.inspect", want: `[[:a, 1]]`},
		{body: "({a: 1}).map { |(k, *v)| [k, v] }.inspect", want: `[[:a, [1]]]`},
		{body: "({a: 1}).map { |(x)| x }.inspect", want: `[:a]`},
		{body: "({a: 1}).map { |x| x }.inspect", want: `[[:a, 1]]`},
	}

	for _, tc := range tests {
		t.Run(tc.body, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.body+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.body, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %s, want %s", tc.body, got.String(), tc.want)
			}
		})
	}
}
