package runtime

import "testing"

// A block driven by a pure collection builtin runs under a block-iteration
// region, whose memo holds the reachable graph of the stable prefix -- the
// frame owning the receiver included -- so a check inside the block body costs
// O(block) rather than O(receiver). Dispatching any builtin from the block body
// bumped the mutation epoch unconditionally, which invalidates that prefix memo
// once per element, so the next check re-walked the whole receiver: O(n) walk
// work per element, O(n^2) overall, for one execution on one goroutine with no
// concurrency, no mutation and no second script.
//
// The reading is estimator nodes per receiver element. A shape whose per-element
// cost is flat across n is linear; one whose per-element cost tracks n is
// quadratic. The arithmetic shapes are the controls: they dispatch no builtin
// and were already flat, so they separate "the region memo works" from "the
// region memo survives a builtin call".
func TestBlockBuiltinDispatchKeepsRegionMemo(t *testing.T) {
	if estimatorVerify {
		t.Skip("the estimator oracle recomputes a reference walk on every region check, which is deliberately quadratic")
	}
	if builtinContractVerify {
		t.Skip("the contract verifier walks the reachable graph on every declared dispatch, which is deliberately quadratic")
	}

	cases := []struct {
		name string
		src  string
		// flat marks a shape whose per-element cost must not grow with n.
		// Every shape here is flat once dispatch stops bumping; the controls
		// were flat before it too.
		control bool
	}{
		{
			name:    "identity block",
			src:     "def run(a, n)\n  a.map { |x| x }.length\nend",
			control: true,
		},
		{
			name:    "arithmetic block",
			src:     "def run(a, n)\n  a.map { |x| x + 1 }.length\nend",
			control: true,
		},
		{
			name: "builtin to_s in block",
			src:  "def run(a, n)\n  a.map { |x| x.to_s }.length\nend",
		},
		{
			name: "builtin abs in block",
			src:  "def run(a, n)\n  a.map { |x| x.abs }.length\nend",
		},
		{
			name: "builtin in select block",
			src:  "def run(a, n)\n  a.select { |x| x.abs > 0 }.length\nend",
		},
		{
			name: "builtin in each block",
			src:  "def run(a, n)\n  t = 0\n  a.each { |x| t = t + x.abs }\n  t\nend",
		},
	}

	const small, mid, large = 1000, 2000, 4000
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			perElement := func(n int) float64 {
				return float64(estimatorVisitsFor(t, tc.src, loopMemoArray(n), n)) / float64(n)
			}
			atSmall, atMid, atLarge := perElement(small), perElement(mid), perElement(large)

			// Quadratic work shows up as per-element cost tracking n: a 4x
			// increase in n multiplies it by 4. Linear work leaves it flat.
			// The 2x bound sits between the two classes rather than pinning a
			// node count.
			if atLarge > atSmall*2 {
				t.Errorf("estimator visited %.1f nodes per element at n=%d, %.1f at n=%d and %.1f at n=%d; "+
					"per-element cost must not grow with n, so a builtin dispatched from the block "+
					"body is still invalidating the region's prefix memo",
					atSmall, small, atMid, mid, atLarge, large)
			}
			t.Logf("nodes per element: n=%d %.1f, n=%d %.1f, n=%d %.1f", small, atSmall, mid, atMid, large, atLarge)
		})
	}
}

// The same defect reached through an object receiver, which the array shapes
// above cannot see: the estimator walks an object's ivars rather than a scalar
// payload, so a stale prefix re-walk costs the whole object graph.
//
// Measured by difference, and it has to be. The only way to get an array of
// objects is to build one in script, and that build loop calls a constructor
// per iteration in a plain while loop, which is quadratic on its own and for a
// reason this change deliberately does not address: a constructor runs
// initialize, whose ivar store is a real mutation, so it stays conservative.
// Comparing the whole script against n would therefore measure the build loop
// and report a failure no matter what the iteration did. Subtracting a run that
// stops after the build leaves the map's own per-element cost and nothing else.
func TestBlockBuiltinDispatchKeepsRegionMemoForObjects(t *testing.T) {
	if estimatorVerify {
		t.Skip("the estimator oracle recomputes a reference walk on every region check, which is deliberately quadratic")
	}
	if builtinContractVerify {
		t.Skip("the contract verifier walks the reachable graph on every declared dispatch, which is deliberately quadratic")
	}

	const preamble = "class Row\n" +
		"  def initialize(id)\n    @id = id\n  end\n" +
		"  def id()\n    @id\n  end\n" +
		"end\n\n"
	const build = "  rows = []\n  j = 0\n  while j < n\n    rows << Row.new(j)\n    j = j + 1\n  end\n"

	buildOnly := preamble + "def run(a, n)\n" + build + "  rows.length\nend"
	withMap := preamble + "def run(a, n)\n" + build + "  rows.map { |r| r.id().to_s }.length\nend"

	const small, large = 500, 2000
	iterationCost := func(n int) float64 {
		base := estimatorVisitsFor(t, buildOnly, NewNil(), n)
		full := estimatorVisitsFor(t, withMap, NewNil(), n)
		if full < base {
			t.Fatalf("the mapping run visited fewer nodes (%d) than the build alone (%d); "+
				"the difference cannot be attributed to the iteration", full, base)
		}
		return float64(full-base) / float64(n)
	}
	atSmall, atLarge := iterationCost(small), iterationCost(large)
	if atLarge > atSmall*2 {
		t.Errorf("mapping an array of objects cost %.1f nodes per element at n=%d and %.1f at n=%d "+
			"beyond building it; per-element cost must not grow with n for an object receiver either",
			atSmall, small, atLarge, large)
	}
	t.Logf("iteration nodes per element beyond the build: n=%d %.1f, n=%d %.1f", small, atSmall, large, atLarge)
}

// Skipping the epoch bump is sound only if the estimate is unchanged by it.
// The estimator's own region oracle verifies a memo hit against a full
// reference walk, but it is disabled in the measuring tests above (it makes
// every shape quadratic by construction), so the totals are compared here at
// the point where a divergence is observable: the exact quota at which each
// script stops fitting, memoized against the uncached reference walk.
func TestBlockBuiltinDispatchEstimateMatchesUncachedWalk(t *testing.T) {
	sources := []struct {
		name string
		src  string
	}{
		{name: "to_s in block", src: "def run(a, n)\n  a.map { |x| x.to_s }.length\nend"},
		{name: "abs in block", src: "def run(a, n)\n  a.map { |x| x.abs }.length\nend"},
		{name: "string builtin in block", src: "def run(a, n)\n  a.map { |x| x.to_s.length }.length\nend"},
		{name: "builtin whose result is retained", src: "def run(a, n)\n  out = []\n  a.each { |x| out << x.to_s }\n  out.length\nend"},
		{name: "receiver-mutating builtin in block", src: "def run(a, n)\n  out = []\n  a.each { |x| out.push(x) }\n  out.length\nend"},
	}

	const n, hi = 200, 8 << 20
	for _, tc := range sources {
		t.Run(tc.name, func(t *testing.T) {
			arg := loopMemoArray(n)

			baseWalkCacheDisabled.Store(true)
			uncached := minMemoryQuotaToComplete(t, tc.src, arg, n, hi)
			baseWalkCacheDisabled.Store(false)

			memoized := minMemoryQuotaToComplete(t, tc.src, arg, n, hi)

			if memoized != uncached {
				t.Errorf("smallest quota that fits is %d with the memo and %d with the "+
					"reference walk; a dispatch that skips the epoch bump must not "+
					"change the estimate", memoized, uncached)
			}
		})
	}
}
