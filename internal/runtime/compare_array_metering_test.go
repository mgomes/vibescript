package runtime

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

const sharedDAGSource = `
def build(d)
  cur = [1]
  i = 0
  while i < d
    cur = [cur, cur]
    i = i + 1
  end
  cur
end
`

// Deleting each pair from the on-stack set as soon as it returned made two
// sibling branches re-walk the same subtree, so comparing shared DAGs did 2^d
// work -- measured at 152ms, 551ms, and 2.1s for depths 20, 22, and 24, with
// the step quota never firing because nothing in the walk charged a step. A
// script could monopolize the runtime from inside <=> whatever its limits.
func TestSharedDAGComparisonIsNotExponential(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 5_000_000, MemoryQuotaBytes: 64 << 20}, sharedDAGSource+`
    def run()
      a = build(24)
      b = build(24)
      (a <=> b).inspect
    end
    `)
	done := make(chan struct{})
	var got Value
	var err error
	go func() {
		defer close(done)
		got, err = script.Call(context.Background(), "run", nil, CallOptions{})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("comparing two 24-deep shared DAGs did not finish: the walk is exponential again")
	}
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "0" {
		t.Fatalf("shared DAGs compared to %s, want 0", got.String())
	}
}

// Every compared element charges a step, so a long comparison is bounded by
// the step quota and observes cancellation.
func TestArrayComparisonChargesSteps(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 300, MemoryQuotaBytes: 64 << 20}, `
    def run(a, b)
      (a <=> b).inspect
    end
    `)
	wide := make([]Value, 20000)
	for i := range wide {
		wide[i] = NewInt(1)
	}
	other := append([]Value{}, wide...)
	if _, err := script.Call(context.Background(), "run", []Value{NewArray(wide), NewArray(other)}, CallOptions{}); err == nil {
		t.Fatalf("expected the step quota to stop a 20000-element comparison")
	}
}

// sort routes through the same comparator, so it is metered too.
func TestArraySortChargesStepsForElementComparisons(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 400, MemoryQuotaBytes: 64 << 20}, `
    def run(rows)
      rows.sort.length
    end
    `)
	// The rows must differ only at the end, so each comparison walks the whole
	// width rather than short-circuiting on equality or on the first element.
	rows := make([]Value, 300)
	for i := range rows {
		inner := make([]Value, 200)
		for j := range inner {
			inner[j] = NewInt(1)
		}
		inner[len(inner)-1] = NewInt(int64(i))
		rows[i] = NewArray(inner)
	}
	if _, err := script.Call(context.Background(), "run", []Value{NewArray(rows)}, CallOptions{}); err == nil {
		t.Fatalf("expected the step quota to stop sorting wide arrays")
	}
}

// Memoizing completed pairs must not change results: a pair compared under an
// open cycle uses the equal-on-cycle shortcut and is deliberately not cached.
func TestCyclicComparisonStillTerminatesAndAgrees(t *testing.T) {
	t.Parallel()

	tests := []struct {
		body string
		want string
	}{
		{"a = [1]\n  a.push(a)\n  (a <=> a).inspect", "0"},
		{"a = [1]\n  a.push(a)\n  b = [1]\n  b.push(b)\n  (a <=> b).inspect", "0"},
		{"inner = [1, 2]\n  (([inner, inner, 1]) <=> ([inner, inner, 2])).inspect", "-1"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.body+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%v", err)
			}
			if got.String() != tc.want {
				t.Fatalf("= %s, want %s", got.String(), tc.want)
			}
		})
	}
}

// The ordering members replace a comparison failure with their own "values are
// not comparable" message. Now that comparison charges steps, that replacement
// would relabel a quota exhaustion as an ordinary runtime error -- and an
// ordinary error is rescuable, so a sandbox limit would become catchable.
func TestSortPreservesSandboxErrors(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 400, MemoryQuotaBytes: 64 << 20}, `
    def run(rows)
      begin
        rows.sort.length
      rescue => e
        "rescued"
      end
    end
    `)
	rows := make([]Value, 300)
	for i := range rows {
		inner := make([]Value, 200)
		for j := range inner {
			inner[j] = NewInt(1)
		}
		inner[len(inner)-1] = NewInt(int64(i))
		rows[i] = NewArray(inner)
	}
	_, err := script.Call(context.Background(), "run", []Value{NewArray(rows)}, CallOptions{})
	if err == nil {
		t.Fatalf("the step quota was caught by rescue: a sandbox limit must stay uncatchable")
	}
	if !strings.Contains(err.Error(), "step quota") {
		t.Fatalf("error = %v, want the step-quota error rather than an incomparability message", err)
	}
}

// Every ordering member routes its comparison failures through the same
// translation, so a sandbox limit must survive all of them rather than being
// relabelled as an incomparability on some paths.
func TestEveryOrderingMemberPreservesSandboxErrors(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		"rows.sort.length",
		"rows.sort_by { |r| r }.length",
		"rows.sort!.length",
		"rows.min.length",
		"rows.max.length",
		"rows.minmax.length",
		"rows.min_by { |r| r }.length",
		"rows.max_by { |r| r }.length",
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: 400, MemoryQuotaBytes: 64 << 20},
				"def run(rows)\n  begin\n    "+body+"\n  rescue => e\n    \"rescued\"\n  end\nend")
			rows := make([]Value, 300)
			for i := range rows {
				inner := make([]Value, 200)
				for j := range inner {
					inner[j] = NewInt(1)
				}
				inner[len(inner)-1] = NewInt(int64(i))
				rows[i] = NewArray(inner)
			}
			_, err := script.Call(context.Background(), "run", []Value{NewArray(rows)}, CallOptions{})
			if err == nil {
				t.Fatalf("%s: the step quota was caught by rescue: a sandbox limit must stay uncatchable", body)
			}
			if !strings.Contains(err.Error(), "step quota") {
				t.Fatalf("%s: error = %v, want the step-quota error rather than an incomparability message", body, err)
			}
		})
	}
}

// A genuine incomparability still reports as one.
func TestSortStillReportsIncomparableValues(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      [[1, "a"], [1, 2]].sort
    end
    `)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected incomparable values to be reported")
	}
	if !strings.Contains(err.Error(), "not comparable") {
		t.Fatalf("error = %v, want the incomparability message", err)
	}
}

// The memo is a Go-local map the periodic memory check cannot see, so its
// size is fixed up front and reserved in one charge. Growing it per entry was
// the alternative and it needed a rollback on every dropped entry, a rule for
// which error wins when a reservation fails mid-unwind, and a quota check that
// walks the whole reachable heap -- O(N^2) unmetered work after an O(N)-step
// descent, because these comparisons run inside builtin dispatch where the
// base-walk cache is disabled.
func TestComparisonMemoFootprintIsBounded(t *testing.T) {
	t.Parallel()

	// Far more distinct nested pairs than the memo can hold, so an unbounded
	// memo would retain one entry each until the comparison finished.
	build := func() Value {
		rows := make([]Value, arrayCompareMemoMaxEntries*8)
		for i := range rows {
			rows[i] = NewArray([]Value{NewInt(int64(i)), NewInt(int64(i))})
		}
		return NewArray(rows)
	}
	a, b := build(), build()

	script := compileScriptWithConfig(t, Config{StepQuota: 100_000_000, MemoryQuotaBytes: 8 << 20}, `
    def run(a, b)
      (a <=> b).inspect
    end
    `)
	got, err := script.Call(context.Background(), "run", []Value{a, b}, CallOptions{})
	if err != nil {
		t.Fatalf("comparing many distinct pairs: %v", err)
	}
	if got.String() != "0" {
		t.Fatalf("comparison = %s, want 0", got.String())
	}
}

// A quota too small to hold the memo runs the comparison without one: correct,
// slower on shared structures, and still bounded by the step quota. Failing
// the whole comparison because a cache did not fit would be the wrong trade.
func TestComparisonWithoutRoomForTheMemoStillAnswers(t *testing.T) {
	t.Parallel()

	build := func(last int64) Value {
		rows := make([]Value, 50)
		for i := range rows {
			rows[i] = NewArray([]Value{NewInt(int64(i))})
		}
		rows[len(rows)-1] = NewArray([]Value{NewInt(last)})
		return NewArray(rows)
	}

	// Below the memo's own footprint, so the reservation cannot be taken.
	const quota = arrayCompareMemoMaxEntries * arrayCompareMemoEntryBytes / 2
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000_000, MemoryQuotaBytes: quota}, `
    def run(a, b)
      (a <=> b).inspect
    end
    `)
	got, err := script.Call(context.Background(), "run", []Value{build(1), build(2)}, CallOptions{})
	if err != nil {
		t.Fatalf("a comparison with no room for the memo was rejected: %v", err)
	}
	if got.String() != "-1" {
		t.Fatalf("comparison = %s, want -1", got.String())
	}
}

// The memo's reservation must not change the answer for an ordinary
// comparison that fits comfortably, and must be released afterwards so a
// later comparison has its full budget.
func TestComparisonStaysCorrectWithTheMemoReservation(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000_000, MemoryQuotaBytes: 2 << 20}, `
    def run(a, b)
      first = (a <=> b).inspect
      second = (a <=> b).inspect
      "#{first}#{second}"
    end
    `)
	build := func(last int64) Value {
		rows := make([]Value, 200)
		for i := range rows {
			rows[i] = NewArray([]Value{NewInt(int64(i))})
		}
		rows[len(rows)-1] = NewArray([]Value{NewInt(last)})
		return NewArray(rows)
	}
	got, err := script.Call(context.Background(), "run", []Value{build(1), build(2)}, CallOptions{})
	if err != nil {
		t.Fatalf("repeated comparisons exhausted the quota through a leaked reservation: %v", err)
	}
	if got.String() != "-1-1" {
		t.Fatalf("comparisons = %s, want -1-1", got.String())
	}
}

// A deeply nested single-child comparison memoizes only during its final
// unwind. The bound applies there too: it stops caching rather than growing,
// so the deep case costs no more host memory than any other. The depth is
// well past arrayCompareMemoMaxEntries, so the unwind runs out of budget and
// keeps going.
func TestDeepUnwindMemoIsStillBounded(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000_000, MemoryQuotaBytes: 8 << 20}, `
    def build(d)
      cur = [1]
      i = 0
      while i < d
        cur = [cur]
        i = i + 1
      end
      cur
    end
    def run()
      a = build(2000)
      b = build(2000)
      (a <=> b).inspect
    end
    `)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("a 20000-deep comparison was rejected: %v", err)
	}
	if got.String() != "0" {
		t.Fatalf("comparison = %s, want 0", got.String())
	}
}

// The ordering members must get the memo too. Building the state with a
// literal left its budget at zero, so every completed-pair insertion was
// refused and sort's comparisons stayed exponential on shared DAGs while
// <=> did not.
func TestOrderingMembersGetTheMemoToo(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		"rows.sort.length",
		"rows.sort_by { |r| r }.length",
		"rows.sort!.length",
		"rows.min.length",
		"rows.max.length",
		"rows.minmax.length",
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: 5_000_000, MemoryQuotaBytes: 64 << 20}, sharedDAGSource+`
    def run()
      rows = [build(22), build(22)]
      `+body+`
    end
    `)
			done := make(chan error, 1)
			go func() {
				_, err := script.Call(context.Background(), "run", nil, CallOptions{})
				done <- err
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("%s: %v", body, err)
				}
			case <-time.After(30 * time.Second):
				t.Fatalf("%s did not finish: the ordering members' walk is exponential", body)
			}
		})
	}
}

// The ordering members compare scalars far more often than arrays, so the memo
// must not be set up for them. Reserving up front made every scalar comparison
// take the reservation and run checkMemory -- a full reachable-graph walk,
// since builtin dispatch disables the base-walk cache -- turning linear
// extrema over a scalar array into quadratic work.
func TestScalarComparisonsDoNotSetUpTheMemo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		left, right Value
		wantMemo    bool
	}{
		{name: "ints", left: NewInt(1), right: NewInt(2)},
		{name: "strings", left: NewString("a"), right: NewString("b")},
		{name: "floats", left: NewFloat(1.5), right: NewFloat(2.5)},
		{name: "arrays", left: NewArray([]Value{NewInt(1)}), right: NewArray([]Value{NewInt(2)}), wantMemo: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := newArrayCompareState(nil)
			if _, _, err := compareOrderForSort(tc.left, tc.right, state); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if state.memoTried != tc.wantMemo {
				t.Fatalf("%s: memo set up = %v, want %v", tc.name, state.memoTried, tc.wantMemo)
			}
		})
	}
}

// Scalar extrema must stay linear in the receiver size. The eager reservation
// made this superlinear: 30ms, 61ms, and 174ms for 2000, 4000, and 8000
// elements, against roughly zero once the setup became lazy.
func TestScalarExtremaStayLinear(t *testing.T) {
	t.Parallel()

	items := make([]Value, 8000)
	for i := range items {
		items[i] = NewInt(int64(i))
	}
	receiver := NewArray(items)

	for _, body := range []string{"a.min", "a.max", "a.minmax", "a.sort"} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: 64 << 20},
				"def run(a)\n  "+body+"\nend")
			done := make(chan error, 1)
			go func() {
				_, err := script.Call(context.Background(), "run", []Value{receiver}, CallOptions{})
				done <- err
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("%s: %v", body, err)
				}
			case <-time.After(20 * time.Second):
				t.Fatalf("%s over 8000 scalars did not finish: the per-comparison setup is not linear", body)
			}
		})
	}
}

// The reservation must stay above the map's real footprint. The key and result
// structs are 32 bytes each, but the map also carries control data, table and
// directory headers, and load-factor slack, so charging the structs alone let
// a filled memo exceed the quota.
func TestMemoChargeCoversTheMapFootprint(t *testing.T) {
	// HeapAlloc is process-wide, so concurrent tests inflate any single
	// reading. Noise only ever adds, so the smallest of several trials is the
	// closest estimate of the map alone.
	measure := func() uint64 {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)

		memo := make(map[arrayComparePair]arrayCompareResult, arrayCompareMemoMaxEntries)
		for i := range arrayCompareMemoMaxEntries {
			memo[arrayComparePair{leftPtr: uintptr(i), rightPtr: uintptr(i * 3), leftLen: i, rightLen: i}] = arrayCompareResult{order: i}
		}

		runtime.GC()
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(memo)
		if after.HeapAlloc < before.HeapAlloc {
			return 0
		}
		return after.HeapAlloc - before.HeapAlloc
	}

	actual := measure()
	for range 4 {
		if got := measure(); got > 0 && got < actual {
			actual = got
		}
	}

	charged := uint64(arrayCompareMemoMaxEntries * arrayCompareMemoEntryBytes)
	if actual > 0 && charged < actual {
		t.Fatalf("a full memo occupies %d bytes but only %d are charged", actual, charged)
	}
}

// One state serves a whole sort or extrema pass. A state per comparison made
// every comparator call that saw two arrays allocate a fresh memo and take a
// fresh reservation, whose check walks the whole reachable graph. Measured on
// sorting tiny arrays: 694ms, 6.5s, and 27.6s at 2000, 6000, and 12000
// elements against 97ms, 801ms, and 3.4s once the state was shared -- and the
// per-comparison form also exhausted the memory quota, because each call took
// its own 192KB reservation.
func TestSortingManySmallArraysSharesOneMemo(t *testing.T) {
	t.Parallel()

	items := make([]Value, 6000)
	for i := range items {
		items[i] = NewArray([]Value{NewInt(int64(i % 7)), NewInt(int64(i))})
	}
	receiver := NewArray(items)

	for _, body := range []string{"a.sort.length", "a.sort_by { |r| r }.length", "a.min.length", "a.minmax.length"} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: 256 << 20},
				"def run(a)\n  "+body+"\nend")
			done := make(chan error, 1)
			go func() {
				_, err := script.Call(context.Background(), "run", []Value{receiver}, CallOptions{})
				done <- err
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("%s: %v", body, err)
				}
			case <-time.After(60 * time.Second):
				t.Fatalf("%s over 6000 small arrays did not finish: the memo is being rebuilt per comparison", body)
			}
		})
	}
}

// The shared state must actually reuse its memo across comparisons, which is
// what makes one reservation serve the whole pass.
func TestSharedStateReusesItsMemoAcrossComparisons(t *testing.T) {
	t.Parallel()

	left := NewArray([]Value{NewInt(1), NewInt(2)})
	right := NewArray([]Value{NewInt(1), NewInt(3)})

	state := newArrayCompareState(nil)
	if _, err := arraySortCompareValuesWith(state, left, right); err != nil {
		t.Fatalf("first comparison: %v", err)
	}
	after := len(state.done)
	if after == 0 {
		t.Fatalf("the first comparison memoized nothing")
	}
	if _, err := arraySortCompareValuesWith(state, left, right); err != nil {
		t.Fatalf("second comparison: %v", err)
	}
	if len(state.done) != after {
		t.Fatalf("the second comparison added %d memo entries, want it served from the memo", len(state.done)-after)
	}
}

// min_by and max_by run their block between comparisons, so a key the block
// already returned can be mutated before the next one. A memo entry is keyed
// by backing address and length, so it then describes a value that no longer
// holds, and the extrema picked the wrong element. Ruby answers "c" here.
func TestByExtremaDoNotReuseStaleComparisons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "min_by after the key is mutated smaller",
			body: `shared = [1]
  ["a", "b", "c"].min_by { |name|
    if name == "a"
      [0]
    elsif name == "b"
      shared
    else
      shared[0] = -1
      shared
    end
  }`,
			want: "c",
		},
		{
			name: "max_by after the key is mutated larger",
			body: `shared = [1]
  ["a", "b", "c"].max_by { |name|
    if name == "a"
      [2]
    elsif name == "b"
      shared
    else
      shared[0] = 9
      shared
    end
  }`,
			want: "c",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.body+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.name, got.String(), tc.want)
			}
		})
	}
}

// sort_by computes every key before it sorts, so no block runs between
// comparisons and sharing one state stays correct there.
func TestSortByComputesKeysBeforeComparing(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run()
  shared = [1]
  ["a", "b", "c"].sort_by { |name|
    if name == "a"
      [0]
    elsif name == "b"
      shared
    else
      [2]
    end
  }.join(",")
end`)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "a,b,c" {
		t.Fatalf("sort_by = %q, want a,b,c", got.String())
	}
}

// The _by extrema clear their memo between comparisons rather than rebuilding
// the state, so a mutated key cannot reuse a stale entry and the memo is still
// paid for once. Rebuilding per comparison cost 67ms and 327ms over 2000 and
// 6000 array-valued keys, against 27ms and 223ms when the state is reused.
func TestResetMemoClearsResultsButKeepsTheReservation(t *testing.T) {
	t.Parallel()

	left := NewArray([]Value{NewInt(1), NewInt(2)})
	right := NewArray([]Value{NewInt(1), NewInt(3)})

	state := newArrayCompareState(nil)
	if _, err := arraySortCompareValuesWith(state, left, right); err != nil {
		t.Fatalf("first comparison: %v", err)
	}
	if len(state.done) == 0 {
		t.Fatalf("the first comparison memoized nothing")
	}
	memo := state.done

	state.resetMemo()
	if len(state.done) != 0 {
		t.Fatalf("resetMemo left %d entries", len(state.done))
	}
	if state.done == nil {
		t.Fatalf("resetMemo discarded the map instead of clearing it")
	}
	if fmt.Sprintf("%p", state.done) != fmt.Sprintf("%p", memo) {
		t.Fatalf("resetMemo replaced the map, so the allocation is not reused")
	}
	if state.memoBudget != arrayCompareMemoMaxEntries {
		t.Fatalf("resetMemo left the budget at %d, want %d", state.memoBudget, arrayCompareMemoMaxEntries)
	}
}
