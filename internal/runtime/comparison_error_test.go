package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// An incomparable relational comparison raised an error that rescue could not
// catch and that pointed at line 1. Both halves are the failure mode #1038 and
// #1056 described: the error never became a positioned *RuntimeError, so it
// was mislocated and outside rescue's reach. Sandbox limits are deliberately
// unrescuable, but an ordinary type error is a program-level mistake a script
// should be able to handle.
func TestIncomparableRelationalComparisonIsRescuable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
	}{
		{name: "int against string", expr: `1 < "a"`},
		{name: "string against int", expr: `"a" > 1`},
		{name: "arrays are not Comparable", expr: `[1, 2] < [1, 3]`},
		{name: "nil against nil", expr: `nil <= nil`},
		{name: "hash against int", expr: `({a: 1}) >= 1`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `
            def run()
              begin
                `+tc.expr+`
                "not reached"
              rescue => e
                "rescued: #{e.message}"
              end
            end
            `)
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: the comparison escaped rescue: %v", tc.name, err)
			}
			if !strings.HasPrefix(got.String(), "rescued: ") {
				t.Fatalf("%s = %q, want the rescue to have caught it", tc.name, got.String())
			}
		})
	}
}

// The error must carry the position of the comparison, not the start of the
// script.
func TestIncomparableComparisonReportsItsOwnPosition(t *testing.T) {
	t.Parallel()
	script := compileScript(t, "def run()\n  x = 1\n  y = \"a\"\n  x < y\nend")
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected the comparison to be reported")
	}
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("error is not a *RuntimeError, so rescue cannot catch it: %v", err)
	}
	// The code frame carries the reported location; before this it pointed at
	// the start of the script rather than the comparison.
	if !strings.Contains(runtimeErr.CodeFrame, "line 4") {
		t.Fatalf("code frame %q does not point at line 4 (the comparison)", runtimeErr.CodeFrame)
	}
}

// A comparison that succeeds is unaffected, including the kinds that order.
func TestOrderedComparisonsStillWork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{`(1 < 2).to_s`, "true"},
		{`(2 <= 2).to_s`, "true"},
		{`(3 > 2).to_s`, "true"},
		{`(2 >= 3).to_s`, "false"},
		{`("a" < "b").to_s`, "true"},
		{`(1.5 < 2).to_s`, "true"},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %s, want %s", tc.expr, got.String(), tc.want)
			}
		})
	}
}

// A sandbox limit surfaced through a comparison must stay uncatchable, so
// positioning the ordinary errors must not widen what rescue reaches.
func TestComparisonLimitErrorsStayUncatchable(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 250, MemoryQuotaBytes: 8 << 20}, `
    def run()
      begin
        total = 0
        (1..100000).each { |i| total = total + i }
        "not reached"
      rescue => e
        "rescued"
      end
    end
    `)
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatalf("a step-quota exhaustion was caught by rescue, want it uncatchable")
	}
}

// docs/language_reference.md specifies that the relational operators raise
// Ruby's ArgumentError on incomparable operands, but the sentinel carried no
// type so the shared wrap classified it as the base RuntimeError -- and
// `rescue ArgumentError` could not catch it.
func TestIncomparableComparisonIsAnArgumentError(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{`1 < "a"`, `"a" > 1`, `[1] <= [2]`, `nil >= nil`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `
            def run()
              begin
                `+expr+`
                "not reached"
              rescue ArgumentError => e
                "argument"
              rescue => e
                "base"
              end
            end
            `)
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", expr, err)
			}
			if got.String() != "argument" {
				t.Fatalf("%s was caught as %s, want ArgumentError", expr, got.String())
			}
		})
	}
}

// <=> answers nil rather than raising, so it is unaffected.
func TestSpaceshipStillAnswersNil(t *testing.T) {
	t.Parallel()
	script := compileScript(t, "def run()\n  (1 <=> \"a\").inspect\nend")
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "nil" {
		t.Fatalf("1 <=> \"a\" = %s, want nil", got.String())
	}
}

// Other error types keep their classification, so the comparison branch does
// not widen what ArgumentError catches.
func TestOtherErrorTypesKeepTheirClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "zero division", body: "1 / 0", want: "zerodiv"},
		{name: "explicit raise", body: `raise "x"`, want: "base"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `
            def run()
              begin
                `+tc.body+`
                "not reached"
              rescue ZeroDivisionError => e
                "zerodiv"
              rescue ArgumentError => e
                "argument"
              rescue => e
                "base"
              end
            end
            `)
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s caught as %s, want %s", tc.name, got.String(), tc.want)
			}
		})
	}
}
