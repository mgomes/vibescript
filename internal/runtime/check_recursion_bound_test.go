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
