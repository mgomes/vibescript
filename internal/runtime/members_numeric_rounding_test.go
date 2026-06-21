package runtime

import (
	"math"
	"testing"
)

// TestFloatRoundingWithPrecision checks Float#round/#floor/#ceil against the
// Ruby reference values. Positive precision keeps the value a Float; zero or
// negative precision returns an Integer.
func TestFloatRoundingWithPrecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want Value
	}{
		// Positive precision returns a Float.
		{"1.234.round(2)", NewFloat(1.23)},
		{"1.234.floor(2)", NewFloat(1.23)},
		{"1.234.ceil(2)", NewFloat(1.24)},
		{"1.236.round(2)", NewFloat(1.24)},
		{"1.236.floor(2)", NewFloat(1.23)},
		{"1.236.ceil(2)", NewFloat(1.24)},
		{"123.456.round(1)", NewFloat(123.5)},
		{"1234.5678.round(2)", NewFloat(1234.57)},
		// Decimal halves rounded as a person expects despite float error.
		{"2.675.round(2)", NewFloat(2.68)},
		{"1.005.round(2)", NewFloat(1.01)},
		{"0.285.round(2)", NewFloat(0.29)},
		{"8.005.round(2)", NewFloat(8.01)},
		{"0.125.round(2)", NewFloat(0.13)},
		// Negative numbers round away from zero.
		{"(-1.234).round(2)", NewFloat(-1.23)},
		{"(-1.234).floor(2)", NewFloat(-1.24)},
		{"(-1.234).ceil(2)", NewFloat(-1.23)},
		{"(-2.675).round(2)", NewFloat(-2.68)},
		// Zero precision returns an Integer rounded half away from zero.
		{"1.234.round", NewInt(1)},
		{"1.5.round", NewInt(2)},
		{"2.5.round", NewInt(3)},
		{"0.5.round", NewInt(1)},
		{"(-2.5).round", NewInt(-3)},
		{"1.9.floor", NewInt(1)},
		{"1.1.ceil", NewInt(2)},
		{"(-1.9).floor", NewInt(-2)},
		{"(-1.1).ceil", NewInt(-1)},
		{"1.234.round(0)", NewInt(1)},
		// Negative precision buckets to powers of ten and returns an Integer.
		{"1234.0.round(-2)", NewInt(1200)},
		{"1234.0.floor(-2)", NewInt(1200)},
		{"1234.0.ceil(-2)", NewInt(1300)},
		{"1250.0.round(-2)", NewInt(1300)},
		{"1249.9.round(-2)", NewInt(1200)},
		{"(-1234.0).round(-2)", NewInt(-1200)},
		{"(-1234.0).floor(-2)", NewInt(-1300)},
		{"(-1234.0).ceil(-2)", NewInt(-1200)},
		// Extreme precision: overflow guard keeps the value, underflow collapses
		// to an Integer zero.
		{"1.5.round(20)", NewFloat(1.5)},
		{"1.5.round(100)", NewFloat(1.5)},
		{"1.5.round(-20)", NewInt(0)},
		{"1234.5.floor(-10)", NewInt(0)},
		{"1234.5.ceil(-10)", NewInt(10000000000)},
		// Zero is unchanged.
		{"0.0.round(2)", NewFloat(0)},
		{"0.0.round", NewInt(0)},
		{"0.0.floor(-2)", NewInt(0)},
		{"0.0.ceil(-2)", NewInt(0)},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, tc.expr)
			if got.Kind() != tc.want.Kind() {
				t.Fatalf("%s kind = %v, want %v", tc.expr, got.Kind(), tc.want.Kind())
			}
			if !got.Equal(tc.want) {
				t.Fatalf("%s = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

// TestIntegerRoundingWithPrecision checks Integer#round/#floor/#ceil. Any
// non-negative precision returns the receiver unchanged; negative precision
// buckets to the matching power of ten.
func TestIntegerRoundingWithPrecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want int64
	}{
		// Non-negative precision leaves integers untouched.
		{"1234.round", 1234},
		{"1234.floor", 1234},
		{"1234.ceil", 1234},
		{"1234.round(0)", 1234},
		{"1234.round(2)", 1234},
		{"1234.floor(5)", 1234},
		{"1234.ceil(3)", 1234},
		// Negative precision rounds half away from zero.
		{"1234.round(-2)", 1200},
		{"1234.floor(-2)", 1200},
		{"1234.ceil(-2)", 1300},
		{"5.round(-1)", 10},
		{"15.round(-1)", 20},
		{"25.round(-1)", 30},
		{"50.round(-2)", 100},
		{"449.round(-2)", 400},
		{"450.round(-2)", 500},
		{"1234567.round(-3)", 1235000},
		// floor toward negative infinity, ceil toward positive infinity.
		{"14.floor(-1)", 10},
		{"14.ceil(-1)", 20},
		{"11.floor(-2)", 0},
		{"11.ceil(-2)", 100},
		{"99.floor(-2)", 0},
		{"99.ceil(-2)", 100},
		// Negative receivers.
		{"(-5).round(-1)", -10},
		{"(-15).round(-1)", -20},
		{"(-50).round(-2)", -100},
		{"(-14).floor(-1)", -20},
		{"(-14).ceil(-1)", -10},
		{"(-11).floor(-2)", -100},
		{"(-11).ceil(-2)", 0},
		{"(-1).floor(-2)", -100},
		{"(-1).ceil(-2)", 0},
		// Buckets larger than the value collapse toward zero.
		{"5.round(-2)", 0},
		{"0.round(-5)", 0},
		{"1.round(-100)", 0},
		// Buckets past int64 still collapse toward zero when the value is well
		// below half the bucket.
		{"1.round(-20)", 0},
		{"1.floor(-20)", 0},
		{"(-1).ceil(-20)", 0},
		// Boundary against int64: floor/ceil that stay in range succeed.
		{"9223372036854775807.floor(-1)", 9223372036854775800},
		{"(-9223372036854775807).ceil(-1)", -9223372036854775800},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, tc.expr)
			if !got.Equal(NewInt(tc.want)) {
				t.Fatalf("%s = %v, want %d", tc.expr, got, tc.want)
			}
		})
	}
}

// TestNumericRoundingArgumentRejection verifies precision arguments are
// validated like Ruby: only a single Integer is accepted.
func TestNumericRoundingArgumentRejection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{"1.5.round(2.0)", "float.round precision must be an Integer"},
		{"1.5.floor(2.0)", "float.floor precision must be an Integer"},
		{"1.5.ceil(2.0)", "float.ceil precision must be an Integer"},
		{"1234.round(1.5)", "int.round precision must be an Integer"},
		{"1234.floor(1.5)", "int.floor precision must be an Integer"},
		{"1234.ceil(1.5)", "int.ceil precision must be an Integer"},
		{"1.5.round(2, 3)", "float.round expects at most one precision argument"},
		{"1234.floor(1, 2)", "int.floor expects at most one precision argument"},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

// TestNumericRoundingOverflow verifies that results which leave the int64 range
// report an overflow instead of widening like Ruby's bignums.
func TestNumericRoundingOverflow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		args []Value
		want string
	}{
		{
			name: "float round to int overflow",
			expr: "n.round",
			args: []Value{NewFloat(1e30)},
			want: "float.round result out of int64 range",
		},
		{
			name: "float floor to int overflow",
			expr: "n.floor",
			args: []Value{NewFloat(1e30)},
			want: "float.floor result out of int64 range",
		},
		{
			name: "float round negative precision overflow",
			expr: "n.round(-2)",
			args: []Value{NewFloat(1e30)},
			want: "float.round result out of int64 range",
		},
		{
			name: "int round negative precision overflow",
			expr: "n.round(-1)",
			args: []Value{NewInt(math.MaxInt64)},
			want: "int.round result out of int64 range",
		},
		{
			name: "int ceil negative precision overflow",
			expr: "n.ceil(-1)",
			args: []Value{NewInt(math.MaxInt64)},
			want: "int.ceil result out of int64 range",
		},
		{
			name: "int floor negative precision overflow",
			expr: "n.floor(-1)",
			args: []Value{NewInt(math.MinInt64)},
			want: "int.floor result out of int64 range",
		},
		{
			// 10^19 exceeds int64, and |MinInt64| reaches half the bucket, so
			// rounding away from zero lands on -10^19 and reports overflow
			// instead of widening like Ruby's bignums.
			name: "int round bucket beyond int64",
			expr: "n.round(-19)",
			args: []Value{NewInt(math.MinInt64)},
			want: "int.round result out of int64 range",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run(n)\n  "+tc.expr+"\nend")
			requireCallErrorContains(t, script, "run", tc.args, CallOptions{}, tc.want)
		})
	}
}

// TestFloatRoundPositivePrecisionKeepsExtremeValue confirms the overflow guard
// returns the receiver unchanged when ndigits exceeds available precision, so a
// huge float never overflows the scaling.
func TestFloatRoundPositivePrecisionKeepsExtremeValue(t *testing.T) {
	t.Parallel()

	script := compileScript(t, "def run(n)\n  n.round(50)\nend")
	got := callFunc(t, script, "run", []Value{NewFloat(1e300)})
	if got.Kind() != KindFloat {
		t.Fatalf("kind = %v, want float", got.Kind())
	}
	if got.Float() != 1e300 {
		t.Fatalf("round(50) = %v, want 1e300", got.Float())
	}
}
