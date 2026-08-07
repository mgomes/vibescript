package runtime

import (
	"fmt"
	"strings"
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

// TestCheckWarningsYieldReachableOnlyAtRecursionDepthPoisonsSummary pins the
// case the summary bound must not lose. The lambda's false branch changes the
// captured state before calling itself, so the yield in its true branch runs
// only on the nested invocation: the outer walk prunes that branch, and a
// bound that merely skipped the nested walk would summarize f as int and
// report a contradiction that the runtime never produces.
func TestCheckWarningsYieldReachableOnlyAtRecursionDepthPoisonsSummary(t *testing.T) {
	t.Parallel()

	reachable := compileScript(t, `
def f()
  x = false
  h = -> {
    if x
      yield
    else
      x = true
      h.call
    end
  }
  h.call
  0
end

def main() -> string
  f() { 1 }
end
`)
	warnings := checkWarningsWithin(t, reachable, "a yield reachable only at recursion depth")
	if len(warnings) != 0 {
		t.Fatalf("expected the nested yield to leave f's summary unknown, got %v", warnings)
	}

	// A yield no invocation can reach keeps the summary exact, so the bound
	// widens on recursion rather than on every conditional yield.
	unreachable := compileScript(t, `
def f()
  x = false
  h = -> {
    if x
      yield
    end
  }
  h.call
  0
end

def main() -> string
  f() { 1 }
end
`)
	warnings = checkWarningsWithin(t, unreachable, "an unreachable yield")
	if len(warnings) != 1 || warnings[0].Message != "return value expected string, got int" {
		t.Fatalf("expected f to summarize as int for an unreachable yield, got %v", warnings)
	}
}

// forwardedSendChainSource builds `C.send(:send, ..., :build)` with depth
// forwarding hops, the flat shape that made the forwarded-target resolver
// recurse once per argument.
func forwardedSendChainSource(depth int) string {
	var b strings.Builder
	b.WriteString("class C\n  def self.build() -> int\n    1\n  end\nend\n\ndef main() -> string\n  C.send(")
	for range depth {
		b.WriteString(":send, ")
	}
	b.WriteString(":build)\nend\n")
	return b.String()
}

// TestCheckWarningsForwardedSendChainAtCapResolves keeps forwarding exact for
// every chain the bound admits: the checker still follows the hops to C.build
// and reports its int result against main's string annotation.
func TestCheckWarningsForwardedSendChainAtCapResolves(t *testing.T) {
	t.Parallel()

	for _, depth := range []int{0, 1, maxCheckNestingDepth - 1} {
		script := compileScript(t, forwardedSendChainSource(depth))
		warnings := checkWarningsWithin(t, script, fmt.Sprintf("a depth-%d send chain", depth))
		if len(warnings) != 1 || warnings[0].Message != "return value expected string, got int" {
			t.Fatalf("depth %d: expected the forwarded call to resolve to C.build, got %v", depth, warnings)
		}
	}
}

// TestCheckWarningsForwardedSendChainOverCapStaysGradual pins the bound on the
// shape from the report. Each `:send` consumed one argument and recursed, so a
// flat chain descended once per argument and copied the remaining arguments at
// every hop: 60000 hops, well inside the 1MB source limit, took over ten
// seconds of check time before this bound, and the growth was quadratic.
// Past the cap the call goes gradual instead, which reports nothing rather
// than reporting something false.
func TestCheckWarningsForwardedSendChainOverCapStaysGradual(t *testing.T) {
	t.Parallel()

	for _, depth := range []int{60000, maxCheckNestingDepth} {
		script := compileScript(t, forwardedSendChainSource(depth))
		warnings := checkWarningsWithin(t, script, fmt.Sprintf("a depth-%d send chain", depth))
		if len(warnings) != 0 {
			t.Fatalf("depth %d: expected an unresolved forwarded call, got %v", depth, warnings)
		}
	}
}
