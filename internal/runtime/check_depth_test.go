package runtime

import (
	"context"
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

func TestCheckWarningsNestingDepthAtCapPassesAndExecutes(t *testing.T) {
	t.Parallel()

	script := compileScript(t, nestedIfSource(maxCheckNestingDepth))
	if warnings := script.CheckWarnings(); len(warnings) != 0 {
		t.Fatalf("expected no warnings at the nesting cap, got %v", warnings)
	}
	result, err := script.Call(context.Background(), "main", []Value{NewInt(5)}, CallOptions{})
	if err != nil {
		t.Fatalf("call main: %v", err)
	}
	if result.Kind() != KindInt {
		t.Fatalf("expected int result, got %v", result.Kind())
	}
}

func TestCheckWarningsNestingDepthOverCapReportsDiagnostic(t *testing.T) {
	t.Parallel()

	source := nestedIfSource(maxCheckNestingDepth + 1)
	script := compileScript(t, source)
	wantMessage := fmt.Sprintf("check exceeded maximum nesting depth of %d", maxCheckNestingDepth)

	assertDepthWarning := func(label string, warnings []CheckWarning) {
		t.Helper()
		if len(warnings) != 1 {
			t.Fatalf("%s: expected exactly the depth warning, got %v", label, warnings)
		}
		if warnings[0].Message != wantMessage {
			t.Fatalf("%s: expected %q, got %q", label, wantMessage, warnings[0].Message)
		}
		if warnings[0].Function != "main" {
			t.Fatalf("%s: expected warning attributed to main, got %q", label, warnings[0].Function)
		}
	}

	assertDepthWarning("CheckWarnings", script.CheckWarnings())
	assertDepthWarning("CheckWarningsForFunction", script.CheckWarningsForFunction("main"))
	assertDepthWarning("CheckOrderIndependentWarnings", script.CheckOrderIndependentWarnings())

	// The cap is a checker-only backstop: the runtime executes the same
	// script without any depth limit.
	if _, err := script.Call(context.Background(), "main", []Value{NewInt(5)}, CallOptions{}); err != nil {
		t.Fatalf("call main over checker cap: %v", err)
	}
}

func TestCheckWarningsNestingDepthCapCoversBlocksAndMethods(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	b.WriteString("class Deep\n  def dig(n)\n    acc = 0\n")
	for i := 0; i <= maxCheckNestingDepth; i++ {
		fmt.Fprintf(&b, "    if n > %d\n", i)
	}
	b.WriteString("    acc = acc + 1\n")
	for i := 0; i <= maxCheckNestingDepth; i++ {
		b.WriteString("    end\n")
	}
	b.WriteString("    acc\n  end\nend\n")

	script := compileScript(t, b.String())
	warnings := script.CheckWarnings()
	if len(warnings) != 1 {
		t.Fatalf("expected exactly the depth warning, got %v", warnings)
	}
	if warnings[0].Function != "Deep#dig" {
		t.Fatalf("expected warning attributed to Deep#dig, got %q", warnings[0].Function)
	}
	wantMessage := fmt.Sprintf("check exceeded maximum nesting depth of %d", maxCheckNestingDepth)
	if warnings[0].Message != wantMessage {
		t.Fatalf("expected %q, got %q", wantMessage, warnings[0].Message)
	}

	// Block literals nest into their own scope and count against the cap too.
	b.Reset()
	b.WriteString("def run()\n  [1].each do |v|\n")
	for i := 0; i <= maxCheckNestingDepth; i++ {
		fmt.Fprintf(&b, "    if v > %d\n", i)
	}
	b.WriteString("    v\n")
	for i := 0; i <= maxCheckNestingDepth; i++ {
		b.WriteString("    end\n")
	}
	b.WriteString("  end\nend\n")

	blockScript := compileScript(t, b.String())
	blockWarnings := blockScript.CheckWarnings()
	if len(blockWarnings) != 1 {
		t.Fatalf("expected exactly the depth warning for block nesting, got %v", blockWarnings)
	}
	if blockWarnings[0].Message != wantMessage {
		t.Fatalf("expected %q for block nesting, got %q", wantMessage, blockWarnings[0].Message)
	}
}
