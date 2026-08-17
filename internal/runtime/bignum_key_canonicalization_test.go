package runtime

import (
	"context"
	"testing"
)

// Keying a big integer for an array set operation is linear work in the
// payload's words and is charged against the step quota with the arithmetic
// convention (1 + words/8). These tests pin the adversarial shape: thousands of
// aliases of a ~400k-bit value pushed through every set-op surface must trip
// the 50k step quota fast instead of burning tens of seconds of uncharged
// conversion CPU (the pre-fix behavior ran 14-45s per case to completion). Big
// integers are not hash keys, so the hash surfaces are not in this list.
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
}

func TestBignumKeyCanonicalizationInQuotaResultsUnchanged(t *testing.T) {
	t.Parallel()
	// Small big values stay well inside the default quotas and keep their exact
	// set-operation semantics.
	script := compileScript(t, `
    def run
      big = 2 ** 100
      [
        ([big, 2 ** 100, big + 1, 0] & [big]).size,
        [big, 2 ** 100, big + 1].uniq.size,
        ([big] - [big]).size
      ]
    end
  `)
	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("in-quota big values failed: %v", err)
	}
	if got := result.String(); got != "[1, 2, 0]" {
		t.Fatalf("in-quota big-value results = %s", got)
	}
}
