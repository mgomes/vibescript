package runtime

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// nestedIfSource builds a function whose body nests depth if-statements, the
// shape whose exit analysis used to double at every level and made deep
// scripts take exponential time to check.
func nestedIfSource(depth int) string {
	var b strings.Builder
	b.WriteString("def main(n)\n  acc = 0\n")
	for i := 0; i < depth; i++ {
		fmt.Fprintf(&b, "  if n > %d\n", i)
	}
	b.WriteString("  acc = acc + 1\n")
	for i := 0; i < depth; i++ {
		b.WriteString("  end\n")
	}
	b.WriteString("  acc\nend\n")
	return b.String()
}

// TestCheckWarningsDeeplyNestedIfCompletesQuickly pins the checker's
// complexity on deeply nested conditionals: before blockAlwaysExits was made
// single-pass, every nesting level doubled the exit-analysis work and depth
// 300 hung outright. The 30-second deadline is far above the expected
// milliseconds so CI noise cannot trip it, while a complexity regression
// fails loudly instead of stalling the suite.
func TestCheckWarningsDeeplyNestedIfCompletesQuickly(t *testing.T) {
	t.Parallel()

	script := compileScript(t, nestedIfSource(300))

	done := make(chan []CheckWarning, 1)
	go func() {
		// Run both check passes the vibes run -check -e path performs.
		warnings := script.CheckWarnings()
		warnings = append(warnings, script.CheckOrderIndependentWarnings()...)
		done <- warnings
	}()

	select {
	case warnings := <-done:
		if len(warnings) != 0 {
			t.Fatalf("expected no warnings for depth-300 nested if, got %v", warnings)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("checking a depth-300 nested if did not finish within 30s")
	}
}
