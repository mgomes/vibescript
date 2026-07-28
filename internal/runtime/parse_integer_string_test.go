package runtime

import (
	"context"
	"strings"
	"testing"
)

// to_i rejected any value outside int64, so an integer the language can
// represent, print, and compute with could not be parsed back from its own
// string form. Arbitrary-precision integers are documented, and the surfaces
// that stay within 64 bits are indexes, counts, sizes, and precisions -- a
// value conversion is none of those.
func TestStringToIntegerAcceptsValuesBeyondInt64(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "max int64 still parses", expr: `"9223372036854775807".to_i.to_s`, want: "9223372036854775807"},
		{name: "one past max int64", expr: `"9223372036854775808".to_i.to_s`, want: "9223372036854775808"},
		{name: "one past min int64", expr: `"-9223372036854775809".to_i.to_s`, want: "-9223372036854775809"},
		{name: "far beyond int64", expr: `"123456789012345678901234567890".to_i.to_s`, want: "123456789012345678901234567890"},
		{name: "small values unaffected", expr: `"42".to_i.to_s`, want: "42"},
		{name: "negative small values", expr: `"-42".to_i.to_s`, want: "-42"},
		{name: "surrounding whitespace still trims", expr: `"  123  ".to_i.to_s`, want: "123"},
		// The round trip through a value's own string form is the point.
		{name: "round trips a computed bignum", expr: `(2 ** 100).to_s.to_i.to_s`, want: "1267650600228229401496703205376"},
		{name: "round trip preserves identity", expr: `((2 ** 100).to_s.to_i == 2 ** 100).to_s`, want: "true"},
		// to_int is the same conversion under another name, and its float
		// branch already promoted beyond int64 while its string branch did not.
		{name: "to_int builtin on a string", expr: `to_int("9223372036854775808").to_s`, want: "9223372036854775808"},
		{name: "to_int builtin agrees with to_i", expr: `(to_int("123456789012345678901234567890") == "123456789012345678901234567890".to_i).to_s`, want: "true"},
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
				t.Fatalf("%s = %s, want %s", tc.expr, got.String(), tc.want)
			}
		})
	}
}

// Only the range limit is lifted. A malformed string is still rejected, so
// this does not quietly become Ruby's lenient to_i, which returns 0 rather
// than reporting the mistake.
func TestStringToIntegerStillRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{`"12abc".to_i`, `"".to_i`, `"1.5".to_i`, `"abc".to_i`, `"0x10".to_i`, `"1e5".to_i`, `"9223372036854775808abc".to_i`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+expr+"\nend")
			if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
				t.Fatalf("%s was accepted, want it rejected", expr)
			}
		})
	}
}

// big.Int accepts an underscore-separated syntax that ParseInt does not. The
// range fallback must not widen the accepted grammar as a side effect.
func TestStringToIntegerDoesNotWidenAcceptedSyntax(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      "9_223_372_036_854_775_808".to_i
    end
    `)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("underscore-separated digits were accepted, want the base-10 grammar unchanged")
	}
	if !strings.Contains(err.Error(), "base-10 integer string") {
		t.Fatalf("error = %v, want the base-10 rejection", err)
	}
}

// Converting a long digit string is work proportional to its length, so it is
// charged against the step quota rather than running unbounded.
func TestStringToIntegerChargesStepQuota(t *testing.T) {
	t.Parallel()
	digits := strings.Repeat("9", 20000)
	script := compileScriptWithConfig(t, Config{StepQuota: 60, MemoryQuotaBytes: 8 << 20},
		"def run(s)\n  s.to_i\nend")
	if _, err := script.Call(context.Background(), "run", []Value{NewString(digits)}, CallOptions{}); err == nil {
		t.Fatalf("expected the step quota to stop a 20000-digit conversion")
	}
}

// The materialized big integer is charged against the memory quota.
func TestStringToIntegerChargesMemoryQuota(t *testing.T) {
	t.Parallel()
	digits := strings.Repeat("9", 200000)
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000_000, MemoryQuotaBytes: 96 * 1024},
		"def run(s)\n  s.to_i\nend")
	if _, err := script.Call(context.Background(), "run", []Value{NewString(digits)}, CallOptions{}); err == nil {
		t.Fatalf("expected the memory quota to stop a 200000-digit conversion")
	}
}

// big.Int.SetString is superlinear, so a linear step charge can pass well
// before the conversion finishes: a multi-million-digit argument fits the
// default memory quota and spends only part of the step quota yet occupies a
// worker for seconds. The parser caps integer literals at the same limit for
// the same reason.
func TestStringToIntegerCapsConversionDigits(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: 64 << 20},
		"def run(s)\n  s.to_i\nend")
	oversized := strings.Repeat("9", maxParsedIntegerDigits+1)
	_, err := script.Call(context.Background(), "run", []Value{NewString(oversized)}, CallOptions{})
	if err == nil {
		t.Fatalf("a %d-digit conversion was accepted, want it capped", len(oversized))
	}
	if !strings.Contains(err.Error(), "digit conversion limit") {
		t.Fatalf("error = %v, want the digit cap", err)
	}
	// A conversion at the limit still succeeds, so the cap is not narrower
	// than advertised -- including with a sign, which is not a digit.
	atLimit := strings.Repeat("9", maxParsedIntegerDigits)
	for _, input := range []string{atLimit, "-" + atLimit, "+" + atLimit} {
		if _, err := script.Call(context.Background(), "run", []Value{NewString(input)}, CallOptions{}); err != nil {
			t.Fatalf("a %d-digit conversion was rejected: %v", maxParsedIntegerDigits, err)
		}
	}
}
