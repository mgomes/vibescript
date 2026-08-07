package runtime

import (
	"testing"
	"time"
)

// checkWarningsWithin runs both check passes the vibes run -check -e path
// performs and fails instead of stalling the suite when the checker no longer
// terminates. Every script below finishes in milliseconds, so the deadline is
// far above the expected cost and cannot be tripped by CI noise; an unbounded
// walk hangs or exhausts the stack and the deadline reports it as a failure.
func checkWarningsWithin(t *testing.T, script *Script, label string) []CheckWarning {
	t.Helper()
	done := make(chan []CheckWarning, 1)
	go func() {
		warnings := script.CheckWarnings()
		done <- append(warnings, script.CheckOrderIndependentWarnings()...)
	}()
	select {
	case warnings := <-done:
		return warnings
	case <-time.After(30 * time.Second):
		t.Fatalf("checking %s did not finish within 30s", label)
		return nil
	}
}

// requireOnlyUndefinedMissingWarning accepts the warning once per check pass,
// since both passes walk the same body.
func requireOnlyUndefinedMissingWarning(t *testing.T, warnings []CheckWarning, label string) {
	t.Helper()
	if len(warnings) == 0 {
		t.Fatalf("%s: expected the undefined variable warning, got none", label)
	}
	for _, warning := range warnings {
		if warning.Message != "undefined variable missing" {
			t.Fatalf("%s: expected only undefined variable warnings, got %v", label, warnings)
		}
	}
}

// TestCheckWarningsSelfInvokedLambdaRegionEffectsTerminate pins the repeated
// region ivar-effect walk on a lambda stored in the local it calls. The walk
// resolved `h.call` back to the same body and re-entered it once per nested
// call, so this four-line script never finished checking.
func TestCheckWarningsSelfInvokedLambdaRegionEffectsTerminate(t *testing.T) {
	t.Parallel()

	const source = `
def f()
  h = -> { h.call }
  h.call
  0
end
`
	script := compileScript(t, source)
	if warnings := checkWarningsWithin(t, script, "a self-invoking stored lambda"); len(warnings) != 0 {
		t.Fatalf("expected no warnings for a self-invoking stored lambda, got %v", warnings)
	}

	// The bound stops the descent only; the body itself is still checked.
	diagnosed := compileScript(t, `
def f()
  h = -> {
    h.call
    missing
  }
  h.call
  0
end
`)
	requireOnlyUndefinedMissingWarning(
		t,
		checkWarningsWithin(t, diagnosed, "a self-invoking stored lambda with a bad body"),
		"self-invoking stored lambda",
	)
}
