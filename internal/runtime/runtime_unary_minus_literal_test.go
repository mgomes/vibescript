package runtime

import (
	"context"
	"testing"
)

// -5.abs returned -5 because the minus bound looser than the member call, so
// the sign landed on the method's result. That was silent: an expression whose
// whole purpose is to produce a non-negative number returned a negative one.
// These pin the values, since the parse shape alone does not prove precedence.
func TestUnaryMinusBindsToNumericLiteral(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		// The regression.
		{name: "abs_of_negative_literal", expr: "-5.abs", want: "5"},
		{name: "abs_of_negative_float", expr: "-1.5.abs", want: "1.5"},
		{name: "to_s_of_negative_literal", expr: "-5.to_s", want: "-5"},
		{name: "predicate_on_negative_literal", expr: "-7.negative?", want: "true"},

		// Exponentiation still binds tighter than a literal's sign.
		{name: "exponent_outranks_sign", expr: "-2 ** 2", want: "-4"},
		{name: "negative_exponent", expr: "2 ** -2", want: "0.25"},

		// Only literals fold; a variable keeps -(x.abs).
		{name: "identifier_keeps_outer_sign", expr: "-x.abs", want: "-5"},

		// Binary minus is unaffected, with and without surrounding space.
		{name: "binary_minus", expr: "1 - 2", want: "-1"},
		{name: "binary_minus_tight", expr: "x -2", want: "3"},

		// Sign folding survives the int64 boundary in both directions.
		{name: "min_int64_literal", expr: "-9223372036854775808.abs", want: "9223372036854775808"},
		{name: "big_negative_literal", expr: "-99999999999999999999.abs", want: "99999999999999999999"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run(x)\n  ("+tc.expr+").to_s\nend")
			got, err := script.Call(context.Background(), "run", []Value{NewInt(5)}, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %s, want %s", tc.expr, got.String(), tc.want)
			}
		})
	}
}
