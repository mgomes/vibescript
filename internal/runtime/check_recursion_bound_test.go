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

// TestCheckWarningsRecursionReachesYieldsWithoutAssumingThem pins both sides of
// the summary bound. The second walk forgets the locals the body rebinds, so it
// reaches a yield no fixed number of nested invocations would expose and a yield
// that only a sibling lambda performs, while recursion that yields nothing keeps
// its exact summary instead of being widened on the mere fact of recursion.
func TestCheckWarningsRecursionReachesYieldsWithoutAssumingThem(t *testing.T) {
	t.Parallel()

	// Three levels of state changes before the yield becomes reachable.
	deep := compileScript(t, `
def f()
  x = 0
  h = -> {
    if x == 2
      yield
    elsif x == 1
      x = 2
      h.call
    else
      x = 1
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
	if warnings := checkWarningsWithin(t, deep, "a yield three levels deep"); len(warnings) != 0 {
		t.Fatalf("expected the deep yield to leave f's summary unknown, got %v", warnings)
	}

	// The recursion enables a sibling lambda that performs the yield.
	sibling := compileScript(t, `
def f()
  x = false
  g = -> { yield }
  h = -> {
    if x
      g.call
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
	if warnings := checkWarningsWithin(t, sibling, "a yield through a sibling lambda"); len(warnings) != 0 {
		t.Fatalf("expected the sibling yield to leave f's summary unknown, got %v", warnings)
	}

	// Recursion that reaches no yield keeps the summary exact, so the bound
	// costs no diagnostic on bodies that never yield.
	yieldFree := compileScript(t, `
def f()
  x = false
  h = -> {
    if x
      1
    else
      x = true
      h.call
    end
  }
  h.call
  0
end

def main() -> string
  f()
end
`)
	warnings := checkWarningsWithin(t, yieldFree, "yield-free recursion")
	if len(warnings) != 1 || warnings[0].Message != "return value expected string, got int" {
		t.Fatalf("expected yield-free recursion to keep f's summary exact, got %v", warnings)
	}
}

// TestCheckWarningsNamespaceWriteAtRecursionDepthIsRecorded pins the effect the
// namespace scan must not lose. The lambda's false branch changes the captured
// state before calling itself, so the `JSON.stringify` assignment in its true
// branch runs only on the nested invocation. The ordinary scan walks the body
// under the first invocation's state and prunes that branch, so without the
// re-entrant pass the checker would keep treating JSON.stringify as the
// builtin and report its string result as a contradiction.
func TestCheckWarningsNamespaceWriteAtRecursionDepthIsRecorded(t *testing.T) {
	t.Parallel()

	written := compileScript(t, `
def main() -> int
  x = false
  h = -> {
    if x
      JSON.stringify = -> { 1 }
    else
      x = true
      h.call
    end
  }
  h.call
  JSON.stringify({})
end
`)
	if warnings := checkWarningsWithin(t, written, "a namespace write at recursion depth"); len(warnings) != 0 {
		t.Fatalf("expected the nested write to make JSON.stringify dynamic, got %v", warnings)
	}

	// The same recursive shape without a write keeps the member exact, so the
	// re-entrant pass records reachable writes instead of widening everything.
	unwritten := compileScript(t, `
def main() -> int
  x = false
  h = -> {
    if x
      1
    else
      x = true
      h.call
    end
  }
  h.call
  JSON.stringify({})
end
`)
	warnings := checkWarningsWithin(t, unwritten, "a recursive lambda with no namespace write")
	if len(warnings) != 1 || warnings[0].Message != "return value expected int, got string" {
		t.Fatalf("expected JSON.stringify to stay exact with no write, got %v", warnings)
	}
}

// TestCheckWarningsSelfInvokedLambdaScanTerminates covers the namespace effect
// scan. An array element resolves to an exact lambda value, so a lambda that
// calls its own array slot made the scan walk the same body again for every
// nested call and never return (#12).
func TestCheckWarningsSelfInvokedLambdaScanTerminates(t *testing.T) {
	t.Parallel()

	const source = `
def main()
  fns = [-> { fns[0].call }]
  h = -> { fns[0].call }
  h.call
  0
end
`
	script := compileScript(t, source)
	if warnings := checkWarningsWithin(t, script, "a self-invoking projected lambda"); len(warnings) != 0 {
		t.Fatalf("expected no warnings for a self-invoking projected lambda, got %v", warnings)
	}

	diagnosed := compileScript(t, `
def main()
  fns = [-> {
    fns[0].call
    missing
  }]
  h = -> { fns[0].call }
  h.call
  0
end
`)
	requireOnlyUndefinedMissingWarning(
		t,
		checkWarningsWithin(t, diagnosed, "a self-invoking projected lambda with a bad body"),
		"self-invoking projected lambda",
	)
}

// TestCheckWarningsRecursionKeepsInvariantGuards pins how much the re-entry
// walks forget. Both bodies reassign `x` to the value it already holds before
// recursing, so no invocation can enter the branch `x` guards. Forgetting every
// rebound local would make that branch look reachable and silently drop the
// diagnostic each script should still report.
func TestCheckWarningsRecursionKeepsInvariantGuards(t *testing.T) {
	t.Parallel()

	yieldGuard := compileScript(t, `
def f()
  x = false
  done = false
  h = -> {
    if x
      yield
    elsif done
      1
    else
      done = true
      x = false
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
	warnings := checkWarningsWithin(t, yieldGuard, "an invariant guard over a yield")
	if len(warnings) != 1 || warnings[0].Message != "return value expected string, got int" {
		t.Fatalf("expected the unreachable yield to leave f's summary exact, got %v", warnings)
	}

	writeGuard := compileScript(t, `
def main() -> int
  x = false
  done = false
  h = -> {
    if x
      JSON.stringify = -> { 1 }
    elsif done
      1
    else
      done = true
      x = false
      h.call
    end
  }
  h.call
  JSON.stringify({})
end
`)
	warnings = checkWarningsWithin(t, writeGuard, "an invariant guard over a namespace write")
	if len(warnings) != 1 || warnings[0].Message != "return value expected int, got string" {
		t.Fatalf("expected the unreachable write to leave JSON.stringify exact, got %v", warnings)
	}
}

// TestCheckWarningsRecursionKeepsIvarFactsItCannotWrite pins the ivar half.
// Recursion is not itself an ivar effect: a body that changes only a local
// keeps every unset-ivar fact, while a write the recursion enables is still
// collected and widens the ivar it targets.
func TestCheckWarningsRecursionKeepsIvarFactsItCannotWrite(t *testing.T) {
	t.Parallel()

	const prefix = `
class User
  property a: int
  property b: int

  def initialize()
    done = false
`
	const suffix = `
    @a = @b
  end
end

def run()
  User.new().a
end
`
	noWrite := compileScript(t, prefix+`    h = -> {
      if done
        1
      else
        done = true
        h.call
      end
    }
    h.call
`+suffix)
	warnings := checkWarningsWithin(t, noWrite, "recursion that writes no ivar")
	if len(warnings) != 1 || warnings[0].Message != "write to @a expected int, got nil" {
		t.Fatalf("expected the unset fact for @b to survive recursion, got %v", warnings)
	}

	// The same shape writing @b on the branch the recursion enables must still
	// widen it, so the narrowing did not trade the effect away.
	write := compileScript(t, prefix+`    h = -> {
      if done
        @b = 2
      else
        done = true
        h.call
      end
    }
    h.call
`+suffix)
	if warnings := checkWarningsWithin(t, write, "recursion that writes an ivar"); len(warnings) != 0 {
		t.Fatalf("expected the nested write to widen @b, got %v", warnings)
	}
}

// TestCheckWarningsRecursionKeepsIvarFactsAcrossManyCallSites pins the same
// two facts on a body holding several recursive calls rather than one. The walk
// count that bounds re-entry is now spent for the whole outermost walk instead
// of being restored at each site, so the later sites are cut off; cutting them
// off must not turn into declaring every ivar unknown, and must not stop the
// write the recursion enables from being collected.
func TestCheckWarningsRecursionKeepsIvarFactsAcrossManyCallSites(t *testing.T) {
	t.Parallel()

	const prefix = `
class User
  property a: int
  property b: int

  def initialize()
    done = false
`
	const suffix = `
    @a = @b
  end
end

def run()
  User.new().a
end
`
	noWrite := compileScript(t, prefix+`    h = -> {
      if done
        1
      else
        done = true
        h.call
        h.call
        h.call
        h.call
      end
    }
    h.call
`+suffix)
	warnings := checkWarningsWithin(t, noWrite, "many recursive calls writing no ivar")
	if len(warnings) != 1 || warnings[0].Message != "write to @a expected int, got nil" {
		t.Fatalf("expected the unset fact for @b to survive recursion at four call sites,"+
			" got %v", warnings)
	}

	write := compileScript(t, prefix+`    h = -> {
      if done
        @b = 2
      else
        done = true
        h.call
        h.call
        h.call
        h.call
      end
    }
    h.call
`+suffix)
	if warnings := checkWarningsWithin(t, write, "many recursive calls writing an ivar"); len(warnings) != 0 {
		t.Fatalf("expected the nested write to widen @b at four call sites, got %v", warnings)
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
