package runtime

import (
	"context"
	"strings"
	"testing"
	"time"
)

// There was no way to build a repeated string without a loop, so a separator,
// indent, or table rule at a computed width could not be written at all -- a
// literal "------" was the only option. Producing text is what most scripts in
// this language do, which is what made this worth having.
func TestStringRepetition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "repeats the receiver", expr: `"ab" * 3`, want: "ababab"},
		{name: "builds a separator", expr: `"-" * 20`, want: strings.Repeat("-", 20)},
		{name: "zero count yields empty", expr: `"ab" * 0`, want: ""},
		{name: "empty receiver yields empty", expr: `"" * 5`, want: ""},
		{name: "count one returns the receiver", expr: `"ab" * 1`, want: "ab"},
		// Ruby truncates a float count toward zero rather than rejecting it.
		{name: "whole float count", expr: `"ab" * 2.0`, want: "abab"},
		{name: "fractional float count truncates", expr: `"ab" * 1.5`, want: "ab"},
		// Repetition is by bytes of the encoded string, so multibyte runes
		// survive intact.
		{name: "multibyte runes", expr: `"é" * 3`, want: "ééé"},
		{name: "multi-rune string", expr: `"日本" * 2`, want: "日本日本"},
		{name: "computed width", expr: `w = 4 + 1` + "\n  " + `"=" * w`, want: "====="},
		{name: "composes with padding", expr: `("ab" * 2).length.to_s`, want: "4"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.expr, got.String(), tc.want)
			}
		})
	}
}

// Ruby raises on a negative count rather than treating it as zero, so a sign
// mistake is reported instead of silently producing an empty string.
func TestStringRepetitionRejectsNegativeCount(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{`"ab" * -1`, `"ab" * -3`, `"ab" * -2.0`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+expr+"\nend")
			_, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err == nil {
				t.Fatalf("%s was accepted, want a negative count reported", expr)
			}
			if !strings.Contains(err.Error(), "negative") {
				t.Fatalf("%s error = %v, want it to name the negative count", expr, err)
			}
		})
	}
}

// Repetition is not commutative in Ruby, and a non-numeric count is not a
// count at all.
func TestStringRepetitionRejectsUnsupportedOperands(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{`3 * "ab"`, `"ab" * "cd"`, `"ab" * nil`, `"ab" * [1]`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+expr+"\nend")
			if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
				t.Fatalf("%s was accepted, want it rejected", expr)
			}
		})
	}
}

// The count is script-controlled and the result is the product of it and the
// receiver, so the memory quota has to stop an oversized repetition before it
// is allocated.
func TestStringRepetitionRespectsMemoryQuota(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 1_000_000, MemoryQuotaBytes: 256 * 1024}, `
    def run()
      "abcdefghij" * 100000000
    end
    `)
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatalf("expected the memory quota to stop a 1GB repetition")
	}
}

// An empty receiver repeated a script-chosen number of times allocates
// nothing, so the memory quota could never stop the loop. It must not run at
// all.
func TestEmptyStringRepetitionDoesNotSpin(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 1_000_000, MemoryQuotaBytes: 8 << 20}, `
    def run()
      ("" * 1000000000).length
    end
    `)
	done := make(chan struct{})
	var got Value
	var err error
	go func() {
		defer close(done)
		got, err = script.Call(context.Background(), "run", nil, CallOptions{})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("repeating an empty string one billion times did not return promptly")
	}
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "0" {
		t.Fatalf("length = %s, want 0", got.String())
	}
}
