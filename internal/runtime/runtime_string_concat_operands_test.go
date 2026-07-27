package runtime

import (
	"context"
	"strings"
	"testing"
)

// String concatenation rendered whatever it was given, so "a" + nil produced
// "a" -- a missing value disappeared into the message instead of being
// reported -- and [1] + "a" produced "[1]a" from two values that cannot
// sensibly concatenate. Both were silent.
func TestStringConcatRejectsNilAndContainers(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def add(left, right)
      left + right
    end
    `)

	cases := []struct {
		name  string
		left  Value
		right Value
	}{
		{name: "string_plus_nil", left: NewString("a"), right: NewNil()},
		{name: "nil_plus_string", left: NewNil(), right: NewString("a")},
		{name: "array_plus_string", left: NewArray([]Value{NewInt(1)}), right: NewString("a")},
		{name: "string_plus_array", left: NewString("a"), right: NewArray([]Value{NewInt(1)})},
		{name: "hash_plus_string", left: NewHash(map[string]Value{"a": NewInt(1)}), right: NewString("x")},
		{name: "string_plus_hash", left: NewString("x"), right: NewHash(map[string]Value{"a": NewInt(1)})},
		// An object reaches a script from a host capability or context
		// provider and rendered as the placeholder "<object>".
		{name: "object_plus_string", left: NewObject(map[string]Value{"name": NewString("ada")}), right: NewString("x")},
		{name: "string_plus_object", left: NewString("user: "), right: NewObject(map[string]Value{"name": NewString("ada")})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := script.Call(context.Background(), "add", []Value{tc.left, tc.right}, CallOptions{})
			if err == nil {
				t.Fatalf("%s: expected an error, got none", tc.name)
			}
			if !strings.Contains(err.Error(), "unsupported addition operands") {
				t.Fatalf("%s: unexpected error %v", tc.name, err)
			}
		})
	}
}

// Concatenating a scalar into a string stays supported. It is the idiom the
// docs and shipped examples use ("total: " + count), and restricting it would
// be a migration rather than a bug fix.
func TestStringConcatKeepsScalarOperands(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def add(left, right)
      left + right
    end
    `)

	cases := []struct {
		name  string
		left  Value
		right Value
		want  string
	}{
		{name: "string_plus_string", left: NewString("a"), right: NewString("b"), want: "ab"},
		{name: "string_plus_int", left: NewString("total: "), right: NewInt(5), want: "total: 5"},
		{name: "int_plus_string", left: NewInt(5), right: NewString("x"), want: "5x"},
		{name: "string_plus_float", left: NewString("a"), right: NewFloat(1.5), want: "a1.5"},
		{name: "string_plus_symbol", left: NewString("a"), right: NewSymbol("sym"), want: "asym"},
		{name: "string_plus_bool", left: NewString("a"), right: NewBool(true), want: "atrue"},
		{name: "string_plus_money", left: NewString("a"), right: mustMoney("1.00 USD"), want: "a1.00 USD"},
		{name: "string_plus_duration", left: NewString("a"), right: NewDuration(durationFromSeconds(300)), want: "a300s"},
		// A regex renders as its round-trippable literal form rather than a
		// placeholder, so it stays concatenable.
		{name: "string_plus_regex", left: NewString("pattern: "), right: mustRegexValue(t, "a+"), want: "pattern: /a+/"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := script.Call(context.Background(), "add", []Value{tc.left, tc.right}, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.name, got.String(), tc.want)
			}
		})
	}
}

// Array concatenation is a real operation and must not be caught by the
// string-operand restriction.
func TestArrayConcatUnaffected(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def add(left, right)
      left + right
    end
    `)

	got, err := script.Call(context.Background(), "add", []Value{
		NewArray([]Value{NewInt(1)}),
		NewArray([]Value{NewInt(2)}),
	}, CallOptions{})
	if err != nil {
		t.Fatalf("array concat: %v", err)
	}
	if got.Kind() != KindArray || len(got.Array()) != 2 {
		t.Fatalf("array concat produced %v", got.String())
	}
}

// A class instance renders as the placeholder "<Name instance>", so
// concatenating one produced text that reads like output. Defining to_s does
// not currently change that, since neither concatenation nor interpolation
// consults it (#1055), which is why rejecting is the right answer for now.
func TestStringConcatRejectsClassInstances(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{`"user: " + U.new`, `U.new + " :user"`} {
		t.Run(expr, func(t *testing.T) {
			script := compileScript(t, `
    class U
      def initialize
        @n = 1
      end

      def to_s
        "U!"
      end
    end

    def run()
      `+expr+`
    end
    `)
			_, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err == nil {
				t.Fatalf("%s: expected an error, got none", expr)
			}
			if !strings.Contains(err.Error(), "unsupported addition operands") {
				t.Fatalf("%s: unexpected error %v", expr, err)
			}
		})
	}
}

// A callable renders as the placeholder "<block>", so it belongs with the other
// non-renderable kinds. The allowlist means a kind has to opt in deliberately
// rather than inherit concatenation, which is what let objects, instances, and
// callables each slip through in turn.
func TestStringConcatRejectsCallables(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      "fn: " + ->(x) { x }
    end
    `)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected a callable operand to be rejected")
	}
	if !strings.Contains(err.Error(), "unsupported addition operands") {
		t.Fatalf("unexpected error %v", err)
	}
}

func mustRegexValue(t *testing.T, pattern string) Value {
	t.Helper()
	script := compileScript(t, "def run()\n  /"+pattern+"/\nend")
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("build regex %q: %v", pattern, err)
	}
	return got
}
