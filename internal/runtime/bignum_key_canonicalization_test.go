package runtime

import (
	"context"
	"testing"
)

// Canonicalizing a big-integer key (hash set/get/delete, set-op membership,
// aggregation buckets) is linear work in the payload's words and is charged
// against the step quota with the arithmetic convention (1 + words/8). These
// tests pin the adversarial shape: thousands of aliases of a ~400k-bit value
// pushed through every canonicalizing surface must trip the 50k step quota
// fast instead of burning tens of seconds of uncharged conversion CPU (the
// pre-fix behavior ran 14-45s per case to completion).
func requireBigKeyStepTrip(t *testing.T, name, body string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		t.Parallel()
		cfg := Config{StepQuota: 50_000, MemoryQuotaBytes: 8 * 1024 * 1024}
		script := compileScriptWithConfig(t, cfg, `
def run
  x = 2 ** 400_000
  a = []
  3000.times { a.push(x) }
  `+body+`
end
`)
		requireCallErrorContains(t, script, "run", nil, CallOptions{}, "step quota exceeded")
	})
}

func TestBignumKeyCanonicalizationChargesSteps(t *testing.T) {
	t.Parallel()
	requireBigKeyStepTrip(t, "intersect", "(a & a).size")
	requireBigKeyStepTrip(t, "array difference", "(a - a).size")
	requireBigKeyStepTrip(t, "uniq", "a.uniq.size")
	requireBigKeyStepTrip(t, "union", "a.union([x]).size")
	requireBigKeyStepTrip(t, "tally", "a.tally.size")
	requireBigKeyStepTrip(t, "group_by", "a.group_by { |v| v }.size")
	requireBigKeyStepTrip(t, "hash write", `
  h = {}
  i = 0
  3000.times { h[x] = i; i = i + 1 }
  h.size`)
	requireBigKeyStepTrip(t, "hash read", `
  h = {}
  h[x] = 1
  total = 0
  3000.times { total = total + h[x] }
  total`)
	requireBigKeyStepTrip(t, "hash key probe", `
  h = {}
  h[x] = 1
  n = 0
  3000.times { n = n + (h.key?(x) ? 1 : 0) }
  n`)
}

func TestBignumKeyCanonicalizationInQuotaResultsUnchanged(t *testing.T) {
	t.Parallel()
	// Small big keys stay well inside the default quotas and keep their exact
	// semantics through every canonicalizing surface.
	script := compileScript(t, `
    def run
      big = 2 ** 100
      h = {}
      h[big] = "hit"
      [
        h[2 ** 100],
        h.key?(big),
        h.fetch(big),
        ([big, 2 ** 100, big + 1, 0] & [big]).size,
        [big, 2 ** 100, big + 1].uniq.size,
        [big, big, 5].tally[big],
        ([big] - [big]).size
      ]
    end
  `)
	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("in-quota big keys failed: %v", err)
	}
	if got := result.String(); got != "[hit, true, hit, 1, 2, 2, 0]" {
		t.Fatalf("in-quota big-key results = %s", got)
	}
}
