package runtime

import (
	"context"
	goruntime "runtime"
	"strings"
	"testing"
)

// TestDiscardedMemberMissesDoNotResplitTheSource pins that a member lookup
// which misses on the typed table and succeeds through the universal fallback
// does not cost the whole source.
//
// Building an error formatted its code frame by splitting the entire source
// into lines, and member resolution constructs errors on paths that discard
// them: respond_to? probes the typed table before answering false from the
// universal helper. A loop over `"x".respond_to?(:missing)` therefore re-split
// the source every iteration, and none of that Go heap work is visible to the
// script's memory quota — 200 calls allocated 127 MiB on a 240 KB source (#5).
//
// Not parallel: it measures process-wide allocation.
func TestDiscardedMemberMissesDoNotResplitTheSource(t *testing.T) {
	source := strings.Repeat("# pad\n", 40000) + `def run()
  i = 0
  total = 0
  while i < 200
    if "x".respond_to?(:missing)
      total = total + 1
    end
    i = i + 1
  end
  total
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: Unlimited}, source)

	var before, after goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&before)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	goruntime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("respond_to? probes must succeed: %v", err)
	}
	if got.Kind() != KindInt || got.Int() != 0 {
		t.Fatalf("result = %#v, want 0 (no member matched)", got)
	}

	allocated := after.TotalAlloc - before.TotalAlloc
	// Re-splitting per miss allocated about 127 MiB here.
	if limit := uint64(32 << 20); allocated > limit {
		t.Fatalf("200 discarded member misses allocated %.2f MiB over a %d byte source, want under %.2f MiB",
			float64(allocated)/(1<<20), len(source), float64(limit)/(1<<20))
	}
}

// TestMemberErrorsStillCarryTheirCodeFrame pins that memoizing the line index
// did not cost the diagnostics: a real member error still reports the source
// line it happened on.
func TestMemberErrorsStillCarryTheirCodeFrame(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, "def run()\n  \"x\".no_such_member\nend")
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatal("an unknown member must error")
	}
	if !strings.Contains(err.Error(), "no_such_member") {
		t.Fatalf("error lost its message: %v", err)
	}
	if !strings.Contains(err.Error(), "\"x\".no_such_member") {
		t.Fatalf("error lost its code frame: %v", err)
	}
}
