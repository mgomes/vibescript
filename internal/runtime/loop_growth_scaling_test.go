package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/vibes/value"
)

// The #1129 build-loop shapes: each appends one element per iteration with
// `<<`, so before the charged append every iteration's epoch bump forced a
// whole-graph re-walk at the next check — O(n) walk work per iteration, O(n²)
// overall. With the literal-side session accounting (hash literals no longer
// snapshot a reference walk or bump the epoch mid-build) and the charged
// append (the shovel commits its delta into the memo instead of invalidating
// it), doubling the iterations must roughly double the estimator's visits.
func TestGrowthLoopsKeepBaseWalkMemo(t *testing.T) {
	sources := []struct {
		name string
		src  string
	}{
		{
			name: "int append",
			src:  "def run(a, n)\n  out = []\n  j = 0\n  while j < n\n    out << j\n    j = j + 1\n  end\n  out.length\nend",
		},
		{
			name: "string append",
			src:  "def run(a, n)\n  out = []\n  j = 0\n  while j < n\n    out << \"xxxxxxxx\"\n    j = j + 1\n  end\n  out.length\nend",
		},
		{
			name: "array of hashes build",
			src:  "def run(a, n)\n  out = []\n  j = 0\n  while j < n\n    out << {id: j, name: \"x\"}\n    j = j + 1\n  end\n  out.length\nend",
		},
		{
			// Integer keys, deliberately: a builtin call in the loop body (a
			// j.to_s key, for example) runs bypass memory checks at builtin
			// depth that discard the memo, which is the documented remaining
			// contributor — the charged store keeps builtin-free store loops
			// linear.
			name: "hash store loop",
			src:  "def run(a, n)\n  h = {}\n  j = 0\n  while j < n\n    h[j] = j\n    j = j + 1\n  end\n  h.length\nend",
		},
	}

	if estimatorVerify {
		t.Skip("the estimator oracle recomputes a reference walk per charged append, which is deliberately quadratic")
	}

	const small, large = 400, 800
	for _, tc := range sources {
		t.Run(tc.name, func(t *testing.T) {
			atSmall := estimatorVisitsFor(t, tc.src, NewNil(), small)
			atLarge := estimatorVisitsFor(t, tc.src, NewNil(), large)
			if atLarge > atSmall*3 {
				t.Errorf("estimator visited %d nodes for %d iterations and %d for %d; "+
					"doubling the iterations should roughly double the walk, so the "+
					"append is still invalidating the base-walk memo", atSmall, small, atLarge, large)
			}
		})
	}
}

// A loop that builds and discards a hash literal per iteration allocates
// nothing that survives, so with the literal builder's unpublished writes and
// session-based accounting the memo must stay valid across every iteration:
// no bump, no re-walk, linear visits. This isolates the literal-side fixes
// from the charged append, which the shapes above also exercise.
func TestDiscardedHashLiteralLoopKeepsBaseWalkMemo(t *testing.T) {
	if estimatorVerify {
		t.Skip("the estimator oracle recomputes a reference walk per charged append, which is deliberately quadratic")
	}

	src := "def run(a, n)\n  t = false\n  j = 0\n  while j < n\n    t = ({id: a[j], name: \"x\"} == nil)\n    j = j + 1\n  end\n  t\nend"

	const small, large = 400, 800
	atSmall := estimatorVisitsFor(t, src, loopMemoArray(large), small)
	atLarge := estimatorVisitsFor(t, src, loopMemoArray(large), large)
	if atLarge > atSmall*3 {
		t.Errorf("estimator visited %d nodes for %d iterations and %d for %d; "+
			"a discarded literal must not invalidate the memo", atSmall, small, atLarge, large)
	}
}

// The incremental paths are sound only if they price what the reference walk
// prices. Compare the smallest quota at which each shape completes with the
// memo (charged append + literal sessions engaged) against the uncached
// reference walk (both disabled via the kill switch, which also forces the
// literal accumulator's snapshot mode).
//
// Not parallel: baseWalkCacheDisabled is process-wide.
func TestGrowthLoopEstimateMatchesUncachedWalk(t *testing.T) {
	sources := []struct {
		name string
		src  string
		arg  func(int) Value
	}{
		{
			name: "shovel ints",
			src:  "def run(a, n)\n  out = []\n  j = 0\n  while j < n\n    out << j\n    j = j + 1\n  end\n  out.length\nend",
			arg:  func(int) Value { return NewNil() },
		},
		{
			name: "shovel hashes",
			src:  "def run(a, n)\n  out = []\n  j = 0\n  while j < n\n    out << {id: j, name: \"x\"}\n    j = j + 1\n  end\n  out.length\nend",
			arg:  func(int) Value { return NewNil() },
		},
		{
			name: "shovel aliases of reachable data",
			src:  "def run(a, n)\n  out = []\n  j = 0\n  while j < n\n    out << a\n    j = j + 1\n  end\n  out.length\nend",
			arg:  loopMemoArray,
		},
		{
			name: "shovel self",
			src:  "def run(a, n)\n  out = []\n  j = 0\n  while j < n\n    out << out\n    j = j + 1\n  end\n  out.length\nend",
			arg:  func(int) Value { return NewNil() },
		},
		{
			name: "hash literal with reachable container value",
			src:  "def run(a, n)\n  h = {}\n  j = 0\n  while j < n\n    h = {payload: a, id: j}\n    j = j + 1\n  end\n  h.length\nend",
			arg:  loopMemoArray,
		},
		{
			name: "hash literal with duplicate keys",
			src:  "def run(a, n)\n  h = {}\n  j = 0\n  while j < n\n    h = {id: j, id: j + 1, name: \"x\"}\n    j = j + 1\n  end\n  h.length\nend",
			arg:  func(int) Value { return NewNil() },
		},
		{
			name: "nested hash literals",
			src:  "def run(a, n)\n  out = []\n  j = 0\n  while j < n\n    out << {id: j, inner: {name: \"x\", tags: [j, j]}}\n    j = j + 1\n  end\n  out.length\nend",
			arg:  func(int) Value { return NewNil() },
		},
		{
			name: "hash store loop",
			src:  "def run(a, n)\n  h = {}\n  j = 0\n  while j < n\n    h[j.to_s] = j\n    j = j + 1\n  end\n  h.length\nend",
			arg:  func(int) Value { return NewNil() },
		},
		{
			name: "hash store of strings",
			src:  "def run(a, n)\n  h = {}\n  j = 0\n  while j < n\n    h[j.to_s] = \"payload-\" + j.to_s\n    j = j + 1\n  end\n  h.length\nend",
			arg:  func(int) Value { return NewNil() },
		},
		{
			name: "hash store replacements",
			src:  "def run(a, n)\n  h = {}\n  j = 0\n  while j < n\n    h[(j % 7).to_s] = j\n    j = j + 1\n  end\n  h.length\nend",
			arg:  func(int) Value { return NewNil() },
		},
		{
			// A single store that grows the order backing right after a
			// literal: the charged path must not impose a realloc peak the
			// epoch-bumping fallback never charges.
			name: "single store after literal",
			src:  "def run(a, n)\n  h = {a: 1}\n  h[1] = 1\n  h.length\nend",
			arg:  func(int) Value { return NewNil() },
		},
		{
			// Every value is the same immutable string (a literal's backing
			// is a shared Go constant), so the reference walk counts it once
			// while per-entry sessions would bill it per pair without the
			// entry-local dedup bound.
			name: "literal with repeated immutable values",
			src: "def w()\n  \"wwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwww\"\nend\n\n" +
				"def run(a, n)\n  h = {a: w(), b: w(), c: w(), d: w(), e: w(), f: w(), g: w(), h: w()}\n  h.length\nend",
			arg: func(int) Value { return NewNil() },
		},
		{
			// A value whose aliases split between the reachable base and an
			// earlier entry: w() is shared with entry x, a is reachable, so
			// the union dedup must cover both at once.
			name: "literal with mixed base and entry aliases",
			src: "def w()\n  \"wwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwww\"\nend\n\n" +
				"def run(a, n)\n  h = {x: w(), y: [a, w()]}\n  h.length\nend",
			arg: loopMemoArray,
		},
		{
			// The second entry's value expression publishes the first
			// entry's shared payload into a reachable root mid-literal; the
			// retained side must stop counting it once the base does.
			name: "literal whose entry publishes an earlier payload",
			src: "def w()\n  \"wwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwww\"\nend\n\n" +
				"def retain(a)\n  a[0] = w()\n  1\nend\n\n" +
				"def run(a, n)\n  h = {x: w(), y: retain(a)}\n  h.length\nend",
			arg: loopMemoArray,
		},
		{
			// A duplicate key switches the accumulator into replacement mode
			// before the last entry's value expression publishes the shared
			// payload into a reachable root; the replacement-mode accounting
			// must keep re-deduplicating against the live base.
			name: "literal with duplicate key before a publishing entry",
			src: "def w()\n  \"wwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwww\"\nend\n\n" +
				"def retain(a)\n  a[0] = w()\n  1\nend\n\n" +
				"def run(a, n)\n  h = {x: w(), d: 0, d: 1, y: retain(a)}\n  h.length\nend",
			arg: loopMemoArray,
		},
		{
			// Keys first seen AFTER a duplicate switched the accumulator into
			// replacement accounting, with one of them not last. Snapshot mode
			// never charged such a key its entry structure at all, because
			// rebuildRetainedEntries only counts the entries that existed when
			// replacement began, while sessions mode charged every one but the
			// last. The two modes must price the entry the same.
			name: "literal with new keys after a duplicate",
			src:  "def run(a, n)\n  h = {x: 1, x: 2, y: 3, z: 4}\n  h.length\nend",
			arg:  func(int) Value { return NewNil() },
		},
		{
			// Replacing a large value: the old allocation stays in the
			// unpublished hash until the write, so admission must hold both.
			name: "literal replacing a large value",
			src:  "def run(a, n)\n  h = {x: \"a\" * 2000, x: \"b\" * 2000}\n  h.length\nend",
			arg:  func(int) Value { return NewNil() },
		},
		{
			name: "literal inside block region",
			src:  "def run(a, n)\n  out = a.map { |x| {id: x, name: \"x\"} }\n  out.length\nend",
			arg:  loopMemoArray,
		},
	}

	const n, hi = 200, 8 << 20
	for _, tc := range sources {
		t.Run(tc.name, func(t *testing.T) {
			arg := tc.arg(n)

			baseWalkCacheDisabled.Store(true)
			uncached := minMemoryQuotaToComplete(t, tc.src, arg, n, hi)
			baseWalkCacheDisabled.Store(false)

			memoized := minMemoryQuotaToComplete(t, tc.src, arg, n, hi)

			if memoized != uncached {
				t.Errorf("smallest quota that fits is %d with the incremental paths and "+
					"%d with the reference walk; the charged append and literal sessions "+
					"must not change the estimate", memoized, uncached)
			}
		})
	}
}

// The charged append must decline a capacity-full receiver before its
// element walk touches the committed seen-state: a reallocation abandons the
// old backing only when nothing else aliases it, which the memo cannot know
// (a host builtin or capability can retain an overlapping view that keeps
// the old backing reachable), so the ordinary epoch-bumping path must price
// the growth instead.
func TestChargedAppendDeclinesAtCapacity(t *testing.T) {
	t.Parallel()

	backing := make([]Value, 3)
	for i := range backing {
		backing[i] = NewInt(int64(i))
	}
	arr := NewArray(backing)
	exec := &Execution{ctx: context.Background(), memoryQuota: 1 << 20}
	id := sliceBackingIdentity(backing)
	exec.memoryEst.seenSlices = map[uintptr]struct{}{id: {}}
	exec.baseWalkCache = &baseWalkCache{
		valid:          true,
		epoch:          value.MutationEpoch(),
		topo:           exec.baseTopoVersion,
		regionBoundary: noBlockRegion,
	}

	handled, err := exec.appendArrayCharged(arr, NewInt(9))
	if handled || err != nil {
		t.Fatalf("appendArrayCharged = (%v, %v), want a declined (false, nil) append for a capacity-full receiver", handled, err)
	}
	if got := len(arr.Array()); got != 3 {
		t.Fatalf("a declined append must not mutate the receiver; length = %d", got)
	}
	if len(exec.memoryEst.seenSlices) != 1 {
		t.Fatalf("a declined append must not touch the committed seen-state; %d identities recorded", len(exec.memoryEst.seenSlices))
	}
	if _, ok := exec.memoryEst.seenSlices[id]; !ok {
		t.Fatal("the receiver's committed backing identity was retired by a declined append")
	}
}

// A host value can hold overlapping views of one backing: appending to the
// capacity-clipped view reallocates while the wider view keeps the old
// backing reachable — or, with spare capacity, overwrites a slot the wider
// view still exposes. The charged append declines both shapes; the smallest
// admitting quota must agree with the reference walk's. Fresh views are
// built per run because the loop mutates them.
//
// Not parallel: baseWalkCacheDisabled is process-wide.
func TestChargedAppendDeclinesOverAliasedBacking(t *testing.T) {
	const src = "def run(a, n)\n  clipped = a[1]\n  j = 0\n  while j < n\n    clipped << j\n    j = j + 1\n  end\n  clipped.length\nend"
	cases := []struct {
		name    string
		clipCap int
	}{
		{name: "realloc abandons aliased backing", clipCap: 3},
		{name: "in-capacity overwrite of exposed slot", clipCap: 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			aliasedViews := func() Value {
				backing := make([]Value, 4)
				for i := range backing {
					backing[i] = NewString(strings.Repeat("x", 64))
				}
				return NewArray([]Value{NewArray(backing[:4:4]), NewArray(backing[:3:tc.clipCap])})
			}
			minQuota := func() int {
				lo, hi := 1, 8<<20
				for lo < hi {
					mid := (lo + hi) / 2
					script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: mid, StepQuota: Unlimited}, src)
					if _, err := script.Call(context.Background(), "run", []Value{aliasedViews(), NewInt(64)}, CallOptions{}); err != nil {
						lo = mid + 1
					} else {
						hi = mid
					}
				}
				return lo
			}

			baseWalkCacheDisabled.Store(true)
			uncached := minQuota()
			baseWalkCacheDisabled.Store(false)
			memoized := minQuota()
			if memoized != uncached {
				t.Errorf("smallest quota that fits is %d with the charged append and %d with "+
					"the reference walk; an append over an aliased backing must keep the "+
					"memo byte-identical to a fresh walk", memoized, uncached)
			}
		})
	}
}

// The charged append must decline when the backing's spare slot holds a
// non-nil value: a longer host-held view can expose that slot as a live
// element, and overwriting a counted payload would leave the memo carrying
// bytes the graph no longer holds. The decline runs before the element walk
// touches the committed seen-state.
func TestChargedAppendDeclinesOverwritingSpareSlot(t *testing.T) {
	t.Parallel()

	backing := make([]Value, 4)
	for i := range backing {
		backing[i] = NewString(strings.Repeat("y", 64))
	}
	arr := NewArray(backing[:3:4])
	exec := &Execution{ctx: context.Background(), memoryQuota: 1 << 20}
	id := sliceBackingIdentity(backing[:3:4])
	exec.memoryEst.seenSlices = map[uintptr]struct{}{id: {}}
	exec.baseWalkCache = &baseWalkCache{
		valid:          true,
		epoch:          value.MutationEpoch(),
		topo:           exec.baseTopoVersion,
		regionBoundary: noBlockRegion,
	}

	handled, err := exec.appendArrayCharged(arr, NewInt(9))
	if handled || err != nil {
		t.Fatalf("appendArrayCharged = (%v, %v), want a declined (false, nil) append over an occupied spare slot", handled, err)
	}
	if got := len(arr.Array()); got != 3 {
		t.Fatalf("a declined append must not mutate the receiver; length = %d", got)
	}
	if len(exec.memoryEst.seenSlices) != 1 {
		t.Fatalf("a declined append must not touch the committed seen-state; %d identities recorded", len(exec.memoryEst.seenSlices))
	}
}

// A shovel loop that genuinely grows reachable memory must still be stopped
// by the quota: the charged append prices every element it admits, so growth
// past the quota fails exactly as the invalidate-and-rewalk path did.
func TestShovelLoopUnderQuotaStillFailsWhenItGrows(t *testing.T) {
	t.Parallel()

	src := "def run(a, n)\n  out = []\n  j = 0\n  while j < n\n    out << {id: j, name: \"xxxxxxxxxxxxxxxx\"}\n    j = j + 1\n  end\n  out.length\nend"
	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 256 << 10, StepQuota: Unlimited}, src)
	if _, err := script.Call(context.Background(), "run", []Value{NewNil(), NewInt(1_000_000)}, CallOptions{}); err == nil {
		t.Fatal("a loop appending a million hashes completed under a 256 KiB memory quota")
	}
}
