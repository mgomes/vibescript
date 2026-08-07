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

// TestCheckWarningsSelfInvokedLambdaSummaryTerminates covers the same lambda
// once a caller makes the checker summarize f. The summary walk re-checked the
// stored lambda for every exact invocation, and the lambda invoking itself made
// that walk re-enter the same body forever (#7).
func TestCheckWarningsSelfInvokedLambdaSummaryTerminates(t *testing.T) {
	t.Parallel()

	const source = `
def f()
  h = -> { h.call }
  h.call
  0
end

def main()
  f()
end
`
	script := compileScript(t, source)
	if warnings := checkWarningsWithin(t, script, "a summarized self-invoking stored lambda"); len(warnings) != 0 {
		t.Fatalf("expected no warnings for a summarized self-invoking stored lambda, got %v", warnings)
	}

	diagnosed := compileScript(t, `
def f()
  h = -> {
    h.call
    missing
  }
  h.call
  0
end

def main()
  f()
end
`)
	requireOnlyUndefinedMissingWarning(
		t,
		checkWarningsWithin(t, diagnosed, "a summarized self-invoking lambda with a bad body"),
		"summarized self-invoking lambda",
	)
}

// TestCheckWarningsInvokedLambdaYieldsStillPoisonSummary keeps the summary
// re-check the bound protects: an executed lambda that yields must still make
// the enclosing function's result unknown instead of reporting the literal
// result the body falls through to.
func TestCheckWarningsInvokedLambdaYieldsStillPoisonSummary(t *testing.T) {
	t.Parallel()

	yielding := compileScript(t, `
def f()
  h = -> { yield }
  h.call
  0
end

def main() -> string
  f() { 1 }
end
`)
	if warnings := checkWarningsWithin(t, yielding, "a yielding invoked lambda"); len(warnings) != 0 {
		t.Fatalf("expected the yield to leave f's summary unknown, got %v", warnings)
	}

	plain := compileScript(t, `
def f()
  h = -> { 1 }
  h.call
  0
end

def main() -> string
  f()
end
`)
	warnings := checkWarningsWithin(t, plain, "a non-yielding invoked lambda")
	if len(warnings) != 1 || warnings[0].Message != "return value expected string, got int" {
		t.Fatalf("expected f to summarize as int, got %v", warnings)
	}
}
