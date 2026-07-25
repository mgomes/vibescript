package runtime

import (
	"context"
	"strings"
	"testing"
)

// hostBuiltHash returns a hash constructed through the host API rather than by a
// script. Such a hash keeps legacy untyped entries -- reads never promote it --
// so it exercises the hash drivers' untyped branches, which is the path every
// hash an embedder passes in actually takes.
func hostBuiltHash(entries int, payload string) Value {
	m := make(map[string]Value, entries)
	for i := range entries {
		m[hostHashKey(i)] = NewString(payload)
	}
	return NewHash(m)
}

func hostHashKey(i int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz"
	return string([]byte{'k', alphabet[i%26], alphabet[(i/26)%26], alphabet[(i/676)%26]})
}

// untypedHashDriverScripts covers the hash block drivers whose untyped branch now
// opens a block-iteration region. Each takes the receiver as a host argument so
// the untyped branch is the one exercised, and each ends by allocating a large
// value inside the block, so the peak check runs while a memoized prefix is live.
// A region that wrongly suppressed an invalidation would read a stale prefix
// there and shift the script's exact pass/fail quota away from the unmemoized
// run's.
var untypedHashDriverScripts = func() map[string]string {
	peak := strings.Repeat("z", 3000)
	payload := strings.Repeat("p", 2000)
	return map[string]string{
		"untyped_each": "def run(v)\n  v.each do |k, x|\n    " +
			quoteVibe(peak) + "\n  end\nend",
		"untyped_each_key": "def run(v)\n  v.each_key do |k|\n    " +
			quoteVibe(peak) + "\n  end\nend",
		"untyped_each_value": "def run(v)\n  v.each_value do |x|\n    " +
			quoteVibe(peak) + "\n  end\nend",
		"untyped_select": "def run(v)\n  v.select do |k, x|\n    " +
			quoteVibe(peak) + "\n    true\n  end\nend",
		"untyped_reject": "def run(v)\n  v.reject do |k, x|\n    " +
			quoteVibe(peak) + "\n    true\n  end\nend",
		"untyped_transform_values": "def run(v)\n  v.transform_values do |x|\n    " +
			quoteVibe(peak) + "\n  end\nend",
		// Mutation cases. Each block mutates an outer container reachable from the
		// memoized prefix and THEN allocates a bare peak literal, whose
		// per-statement check is the peak observation while the prefix is live.
		// Cumulative growth makes a stale prefix detectably wrong: iteration k's
		// correct prefix is strictly larger than iteration k-1's, so a region that
		// failed to invalidate on the append undercounts here and shifts the
		// memoized threshold below the unmemoized one. The bare literal is what
		// gives these teeth -- folding the payload into the mutation's argument
		// instead leaves nothing observing the stale prefix.
		"untyped_each_outer_push": "def run(v)\n  buf = []\n  v.each do |k, x|\n    buf.push(" +
			quoteVibe(payload) + ")\n    " + quoteVibe(peak) + "\n  end\n  buf.size\nend",
		"untyped_each_value_outer_push": "def run(v)\n  buf = []\n  v.each_value do |x|\n    buf.push(" +
			quoteVibe(payload) + ")\n    " + quoteVibe(peak) + "\n  end\n  buf.size\nend",
		"untyped_select_outer_push": "def run(v)\n  buf = []\n  v.select do |k, x|\n    buf.push(" +
			quoteVibe(payload) + ")\n    " + quoteVibe(peak) + "\n    true\n  end\n  buf.size\nend",
		"untyped_transform_values_outer_push": "def run(v)\n  buf = []\n  v.transform_values do |x|\n    buf.push(" +
			quoteVibe(payload) + ")\n    " + quoteVibe(peak) + "\n  end\n  buf.size\nend",
		// Block rebinds an outer local to a growing string: resolves up the parent
		// chain to a prefix scope, which must bump.
		"untyped_each_outer_rebind": "def run(v)\n  acc = \"seed\"\n  v.each do |k, x|\n    acc = acc + " +
			quoteVibe(payload) + "\n    " + quoteVibe(peak) + "\n  end\n  acc.size\nend",
	}
}()

func quoteVibe(s string) string {
	return `"` + s + `"`
}

func hashRegionQuotaRun(t *testing.T, source string, receiver Value, quota int) error {
	t.Helper()
	script := compileScriptWithConfig(t, Config{StepQuota: 1 << 20, MemoryQuotaBytes: quota}, source)
	_, err := script.Call(context.Background(), "run", []Value{receiver}, CallOptions{})
	return err
}

// hashRegionMinimalPassingQuota bisects the smallest quota the script passes
// under. Pass/fail is monotone in the quota, so the boundary is exact and any
// change to any estimate during the run moves it.
func hashRegionMinimalPassingQuota(t *testing.T, source string, entries int) int {
	t.Helper()
	const upper = 1 << 22
	payload := strings.Repeat("p", 200)
	if err := hashRegionQuotaRun(t, source, hostBuiltHash(entries, payload), upper); err != nil {
		t.Fatalf("script failed under generous quota: %v", err)
	}
	if err := hashRegionQuotaRun(t, source, hostBuiltHash(entries, payload), 1); err == nil {
		t.Fatalf("script passed under 1-byte quota; bisection has no boundary")
	}
	lo, hi := 2, upper
	for lo < hi {
		mid := lo + (hi-lo)/2
		if hashRegionQuotaRun(t, source, hostBuiltHash(entries, payload), mid) == nil {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// TestUntypedHashDriverRegionsPreserveQuotaThresholds bisects the exact pass/fail
// quota for each untyped hash driver with the base-walk memo enabled and
// disabled and requires identical thresholds. Opening a block-iteration region
// on these branches may only change how fast an estimate is computed, never its
// value at any check site: an estimate that drifted low would be an undercount,
// which is a quota escape rather than a mere slowdown.
func TestUntypedHashDriverRegionsPreserveQuotaThresholds(t *testing.T) {
	for name, source := range untypedHashDriverScripts {
		t.Run(name, func(t *testing.T) {
			memoized := hashRegionMinimalPassingQuota(t, source, 8)

			baseWalkCacheDisabled.Store(true)
			unmemoized := hashRegionMinimalPassingQuota(t, source, 8)
			baseWalkCacheDisabled.Store(false)

			if memoized != unmemoized {
				t.Fatalf("quota threshold drifted: memoized=%d unmemoized=%d", memoized, unmemoized)
			}
		})
	}
}
