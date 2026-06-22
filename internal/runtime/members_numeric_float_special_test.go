package runtime

import (
	"context"
	"math"
	"testing"
)

// TestFloatDivisionByZeroProducesSpecialValues verifies that the `/` operator
// follows IEEE 754 and Ruby for a zero divisor: a finite nonzero numerator
// yields +/-Infinity and a zero numerator yields NaN, instead of raising.
// Integer division by zero is covered separately and still errors.
func TestFloatDivisionByZeroProducesSpecialValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		expr      string
		wantInf   int // +1, -1, or 0 when not infinite
		wantNaN   bool
		wantValue float64 // checked only when finite (wantInf == 0 && !wantNaN)
	}{
		{name: "positive over zero", expr: "1.0 / 0", wantInf: 1},
		{name: "negative over zero", expr: "(-1.0) / 0", wantInf: -1},
		{name: "zero over zero", expr: "0.0 / 0.0", wantNaN: true},
		{name: "positive over float zero", expr: "1.0 / 0.0", wantInf: 1},
		{name: "negative over float zero", expr: "(-1.0) / 0.0", wantInf: -1},
		{name: "int over float zero", expr: "1 / 0.0", wantInf: 1},
		{name: "negative int over float zero", expr: "(-1) / 0.0", wantInf: -1},
		{name: "finite division still works", expr: "3.0 / 2", wantValue: 1.5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, tc.expr)
			if got.Kind() != KindFloat {
				t.Fatalf("%s kind = %v, want float", tc.expr, got.Kind())
			}
			f := got.Float()
			switch {
			case tc.wantNaN:
				if !math.IsNaN(f) {
					t.Fatalf("%s = %v, want NaN", tc.expr, f)
				}
			case tc.wantInf != 0:
				if !math.IsInf(f, tc.wantInf) {
					t.Fatalf("%s = %v, want infinity with sign %d", tc.expr, f, tc.wantInf)
				}
			default:
				if f != tc.wantValue {
					t.Fatalf("%s = %v, want %v", tc.expr, f, tc.wantValue)
				}
			}
		})
	}
}

// TestIntegerDivisionByZeroStillErrors confirms the integer division-by-zero
// contract is preserved: only float operands opt into IEEE special values.
func TestIntegerDivisionByZeroStillErrors(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{"1 / 0", "(-1) / 0", "0 / 0"} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "division by zero")
		})
	}
}

// TestFloatFdivByZeroProducesSpecialValues verifies that Numeric#fdiv mirrors
// the `/` operator for a zero divisor, yielding Infinity/NaN rather than raising.
func TestFloatFdivByZeroProducesSpecialValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr    string
		wantInf int
		wantNaN bool
	}{
		{expr: "1.0.fdiv(0)", wantInf: 1},
		{expr: "(-1.0).fdiv(0)", wantInf: -1},
		{expr: "0.0.fdiv(0)", wantNaN: true},
		{expr: "1.fdiv(0)", wantInf: 1},
		{expr: "(-1).fdiv(0)", wantInf: -1},
		{expr: "1.0.fdiv(0.0)", wantInf: 1},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, tc.expr)
			if got.Kind() != KindFloat {
				t.Fatalf("%s kind = %v, want float", tc.expr, got.Kind())
			}
			f := got.Float()
			if tc.wantNaN {
				if !math.IsNaN(f) {
					t.Fatalf("%s = %v, want NaN", tc.expr, f)
				}
				return
			}
			if !math.IsInf(f, tc.wantInf) {
				t.Fatalf("%s = %v, want infinity with sign %d", tc.expr, f, tc.wantInf)
			}
		})
	}
}

// TestFloatNanPredicate covers Float#nan?, which is true only for NaN.
func TestFloatNanPredicate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want bool
	}{
		{"(0.0 / 0.0).nan?", true},
		{"(1.0 / 0).nan?", false},
		{"(-1.0 / 0).nan?", false},
		{"1.5.nan?", false},
		{"0.0.nan?", false},
		{"(-3.5).nan?", false},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, tc.expr)
			if got.Kind() != KindBool {
				t.Fatalf("%s kind = %v, want bool", tc.expr, got.Kind())
			}
			if got.Bool() != tc.want {
				t.Fatalf("%s = %v, want %v", tc.expr, got.Bool(), tc.want)
			}
		})
	}
}

// TestFloatFinitePredicate covers Float#finite?, which is true for any value
// that is neither infinite nor NaN.
func TestFloatFinitePredicate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want bool
	}{
		{"1.0.finite?", true},
		{"0.0.finite?", true},
		{"(-2.5).finite?", true},
		{"(1.0 / 0).finite?", false},
		{"(-1.0 / 0).finite?", false},
		{"(0.0 / 0.0).finite?", false},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, tc.expr)
			if got.Kind() != KindBool {
				t.Fatalf("%s kind = %v, want bool", tc.expr, got.Kind())
			}
			if got.Bool() != tc.want {
				t.Fatalf("%s = %v, want %v", tc.expr, got.Bool(), tc.want)
			}
		})
	}
}

// TestFloatInfinitePredicate covers Float#infinite?, which returns 1 for
// +Infinity, -1 for -Infinity, and nil for every finite value and NaN.
func TestFloatInfinitePredicate(t *testing.T) {
	t.Parallel()

	intTests := []struct {
		expr string
		want int64
	}{
		{"(1.0 / 0).infinite?", 1},
		{"(-1.0 / 0).infinite?", -1},
	}
	for _, tc := range intTests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, tc.expr)
			if got.Kind() != KindInt {
				t.Fatalf("%s kind = %v, want int", tc.expr, got.Kind())
			}
			if got.Int() != tc.want {
				t.Fatalf("%s = %d, want %d", tc.expr, got.Int(), tc.want)
			}
		})
	}

	nilExprs := []string{"1.5.infinite?", "0.0.infinite?", "(-2.5).infinite?", "(0.0 / 0.0).infinite?"}
	for _, expr := range nilExprs {
		t.Run(expr+" nil", func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, expr)
			if got.Kind() != KindNil {
				t.Fatalf("%s = %v, want nil", expr, got)
			}
		})
	}
}

// TestFloatSpecialPredicatesRejectArguments confirms the predicates take no
// arguments, mirroring the neighboring numeric predicates.
func TestFloatSpecialPredicatesRejectArguments(t *testing.T) {
	t.Parallel()

	exprs := []string{"1.0.nan?(1)", "1.0.infinite?(1)", "1.0.finite?(1)"}
	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "does not take arguments")
		})
	}
}

// TestFloatSpecialPredicatesOnHostFloats covers floats supplied by the host
// (rather than computed inside the script), ensuring the predicates inspect the
// receiver's IEEE class regardless of provenance.
func TestFloatSpecialPredicatesOnHostFloats(t *testing.T) {
	t.Parallel()

	script := compileScript(t, "def classify(x)\n  [x.nan?, x.infinite?, x.finite?]\nend")

	tests := []struct {
		name    string
		arg     Value
		wantNaN bool
		wantInf Value // nil, 1, or -1
		wantFin bool
	}{
		{name: "positive infinity", arg: NewFloat(math.Inf(1)), wantInf: NewInt(1)},
		{name: "negative infinity", arg: NewFloat(math.Inf(-1)), wantInf: NewInt(-1)},
		{name: "nan", arg: NewFloat(math.NaN()), wantNaN: true, wantInf: NewNil()},
		{name: "finite", arg: NewFloat(2.5), wantInf: NewNil(), wantFin: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, "classify", []Value{tc.arg})
			if got.Kind() != KindArray {
				t.Fatalf("classify(%v) kind = %v, want array", tc.arg, got.Kind())
			}
			arr := got.Array()
			if len(arr) != 3 {
				t.Fatalf("classify(%v) len = %d, want 3", tc.arg, len(arr))
			}
			if arr[0].Bool() != tc.wantNaN {
				t.Fatalf("classify(%v) nan? = %v, want %v", tc.arg, arr[0].Bool(), tc.wantNaN)
			}
			if !arr[1].Equal(tc.wantInf) {
				t.Fatalf("classify(%v) infinite? = %v, want %v", tc.arg, arr[1], tc.wantInf)
			}
			if arr[2].Bool() != tc.wantFin {
				t.Fatalf("classify(%v) finite? = %v, want %v", tc.arg, arr[2].Bool(), tc.wantFin)
			}
		})
	}
}

// TestFloatSpecialValueComparisons documents the IEEE comparison semantics that
// Vibescript inherits from Go and shares with Ruby: ordered comparisons treat
// infinities as the extreme values and every comparison with NaN is false,
// including NaN == NaN.
func TestFloatSpecialValueComparisons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want bool
	}{
		{"(1.0 / 0) > 1000000.0", true},
		{"(1.0 / 0) > (1.0 / 0)", false},
		{"(-1.0 / 0) < (-1000000.0)", true},
		{"(1.0 / 0) <= (1.0 / 0)", true}, // same infinity compares <=
		{"(1.0 / 0) >= 1000000.0", true}, // +Infinity is the greatest value
		{"(-1.0 / 0) <= (-1000000.0)", true},
		{"(1.0 / 0) == (1.0 / 0)", true}, // same infinity compares equal
		{"(0.0 / 0.0) == (0.0 / 0.0)", false},
		{"(0.0 / 0.0) < 1.0", false},
		{"(0.0 / 0.0) > 1.0", false},
		// Every ordered comparison with NaN is false (IEEE 754 / Ruby), including
		// the inclusive operators that route through the shared ordering helper.
		{"(0.0 / 0.0) <= 1.0", false},
		{"(0.0 / 0.0) >= 1.0", false},
		{"1.0 <= (0.0 / 0.0)", false},
		{"1.0 >= (0.0 / 0.0)", false},
		{"(0.0 / 0.0) <= (0.0 / 0.0)", false},
		{"(0.0 / 0.0) >= (0.0 / 0.0)", false},
		{"(0.0.fdiv(0)) <= 1.0", false},
		{"(0.0.fdiv(0)) >= 1.0", false},
		{"(0.0 / 0.0) != (0.0 / 0.0)", true},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, tc.expr)
			if got.Kind() != KindBool {
				t.Fatalf("%s kind = %v, want bool", tc.expr, got.Kind())
			}
			if got.Bool() != tc.want {
				t.Fatalf("%s = %v, want %v", tc.expr, got.Bool(), tc.want)
			}
		})
	}
}

// TestFloatSpaceshipUnorderedReturnsNil verifies that `<=>` returns nil whenever
// a NaN appears on either side, matching Ruby's IEEE-faithful spaceship, while
// finite and infinite operands still produce -1/0/1.
func TestFloatSpaceshipUnorderedReturnsNil(t *testing.T) {
	t.Parallel()

	t.Run("nil for nan operands", func(t *testing.T) {
		t.Parallel()
		exprs := []string{
			"(0.0 / 0.0) <=> 1.0",
			"1.0 <=> (0.0 / 0.0)",
			"(0.0 / 0.0) <=> (0.0 / 0.0)",
			"(0.0.fdiv(0)) <=> 1.0",
		}
		for _, expr := range exprs {
			got := evalNumericExpr(t, expr)
			if got.Kind() != KindNil {
				t.Fatalf("%s = %v (kind %v), want nil", expr, got, got.Kind())
			}
		}
	})

	t.Run("ordered operands still yield integers", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			expr string
			want int64
		}{
			{"1.0 <=> 2.0", -1},
			{"2.0 <=> 2.0", 0},
			{"3.0 <=> 2.0", 1},
			{"(1.0 / 0) <=> 1000000.0", 1},
			{"(-1.0 / 0) <=> 1000000.0", -1},
			{"(1.0 / 0) <=> (1.0 / 0)", 0},
		}
		for _, tc := range tests {
			got := evalNumericExpr(t, tc.expr)
			if got.Kind() != KindInt {
				t.Fatalf("%s = %v (kind %v), want int %d", tc.expr, got, got.Kind(), tc.want)
			}
			if got.Int() != tc.want {
				t.Fatalf("%s = %d, want %d", tc.expr, got.Int(), tc.want)
			}
		}
	})
}

// TestFloatSpecialValueStaysFloat verifies that special values keep the float
// type, so downstream type checks and member dispatch behave consistently.
func TestFloatSpecialValueStaysFloat(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{"1.0 / 0", "(-1.0) / 0", "0.0 / 0.0", "1.0.fdiv(0)"} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, expr)
			if got.Kind() != KindFloat {
				t.Fatalf("%s kind = %v, want float", expr, got.Kind())
			}
		})
	}
}

// TestFloatSpecialValueRendering checks the Ruby-style string forms for the
// special values, both as the script's return value and inside interpolation.
func TestFloatSpecialValueRendering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{"1.0 / 0", "Infinity"},
		{"(-1.0) / 0", "-Infinity"},
		{"0.0 / 0.0", "NaN"},
		{`"v=#{1.0 / 0}"`, "v=Infinity"},
		{`"v=#{(-1.0) / 0}"`, "v=-Infinity"},
		{`"v=#{0.0 / 0.0}"`, "v=NaN"},
		{"1.5", "1.5"}, // finite values are unaffected
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, tc.expr)
			if got.String() != tc.want {
				t.Fatalf("%s rendered %q, want %q", tc.expr, got.String(), tc.want)
			}
		})
	}
}

// TestFloatSpecialValueRenderingIsBounded guards the sandbox concern from the
// issue: special-value formatting must stay a short, fixed-length string rather
// than an unbounded edge case. The longest special form is "-Infinity" (9
// bytes), well under any finite float's shortest round-trippable form.
func TestFloatSpecialValueRenderingIsBounded(t *testing.T) {
	t.Parallel()

	const maxSpecialLen = len("-Infinity")
	for _, expr := range []string{"1.0 / 0", "(-1.0) / 0", "0.0 / 0.0"} {
		got := evalNumericExpr(t, expr)
		if rendered := got.String(); len(rendered) > maxSpecialLen {
			t.Fatalf("%s rendered %q (%d bytes), want <= %d bytes", expr, rendered, len(rendered), maxSpecialLen)
		}
	}
}

// TestFloatSpecialValueJSONRejected confirms the JSON boundary stays Ruby-
// compatible: JSON has no representation for Infinity or NaN, so stringify
// reports an error instead of emitting an out-of-spec token.
func TestFloatSpecialValueJSONRejected(t *testing.T) {
	t.Parallel()

	exprs := []string{
		`JSON.stringify(1.0 / 0)`,
		`JSON.stringify(-1.0 / 0)`,
		`JSON.stringify(0.0 / 0.0)`,
		`JSON.stringify({"a" => 1.0 / 0})`,
		`JSON.stringify([0.0 / 0.0])`,
	}
	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "unsupported value")
		})
	}
}

// TestFloatSpecialValueJSONErrorMessageUsesRubySpelling verifies the JSON error
// message renders the offending value with Vibescript's float formatting
// ("Infinity"/"NaN") rather than Go's "+Inf"/"NaN", keeping diagnostics
// consistent with how the value prints everywhere else.
func TestFloatSpecialValueJSONErrorMessageUsesRubySpelling(t *testing.T) {
	t.Parallel()

	script := compileScript(t, "def run()\n  JSON.stringify(1.0 / 0)\nend")
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatal("JSON.stringify(Infinity) succeeded, want error")
	}
	requireErrorContains(t, err, "Infinity")
}

// TestNonFiniteFloatRejectedAtIntegerCoercion guards every site that coerces a
// float to int64: a NaN or Infinity created by the new IEEE division must error
// rather than silently flowing in as a garbage int64. This covers range
// endpoints, money_cents, and duration arithmetic.
func TestNonFiniteFloatRejectedAtIntegerCoercion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "range start nan", expr: "(0.0 / 0.0)..1", want: "cannot convert NaN to integer"},
		{name: "range end nan", expr: "1..(0.0 / 0.0)", want: "cannot convert NaN to integer"},
		{name: "range start infinity", expr: "(1.0 / 0)..1", want: "cannot convert Infinity to integer"},
		{name: "range end neg infinity", expr: "1..(-1.0 / 0)", want: "cannot convert -Infinity to integer"},
		{name: "money_cents nan", expr: `money_cents(0.0 / 0.0, "USD")`, want: "money_cents expects integer cents"},
		{name: "money_cents infinity", expr: `money_cents(1.0 / 0, "USD")`, want: "money_cents expects integer cents"},
		{name: "duration plus nan", expr: "5.seconds + (0.0 / 0.0)", want: "cannot convert NaN to integer"},
		{name: "duration plus infinity", expr: "5.seconds + (1.0 / 0)", want: "cannot convert Infinity to integer"},
		{name: "nan plus duration", expr: "(0.0 / 0.0) + 5.seconds", want: "cannot convert NaN to integer"},
		{name: "duration times infinity", expr: "5.seconds * (1.0 / 0)", want: "cannot convert Infinity to integer"},
		{name: "duration over nan", expr: "5.seconds / (0.0 / 0.0)", want: "cannot convert NaN to integer"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

// TestFiniteFloatStillCoercesAtIntegerSites confirms the hardened coercion path
// keeps truncating ordinary finite floats toward zero rather than rejecting
// them, so existing programs that pass float endpoints keep working.
func TestFiniteFloatStillCoercesAtIntegerSites(t *testing.T) {
	t.Parallel()

	t.Run("range truncates float endpoints", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, "def run()\n  out = []\n  for i in 1.9..4.2\n    out = out.push(i)\n  end\n  out\nend")
		got := callFunc(t, script, "run", nil)
		if got.Kind() != KindArray {
			t.Fatalf("range iteration kind = %v, want array", got.Kind())
		}
		arr := got.Array()
		want := []int64{1, 2, 3, 4}
		if len(arr) != len(want) {
			t.Fatalf("range iteration = %v, want %v", arr, want)
		}
		for i, w := range want {
			if arr[i].Kind() != KindInt || arr[i].Int() != w {
				t.Fatalf("range iteration[%d] = %v, want %d", i, arr[i], w)
			}
		}
	})

	t.Run("money_cents truncates float cents", func(t *testing.T) {
		t.Parallel()
		got := evalNumericExpr(t, `money_cents(2550.9, "USD")`)
		if got.Kind() != KindMoney {
			t.Fatalf("money_cents kind = %v, want money", got.Kind())
		}
		if cents := got.Money().Cents(); cents != 2550 {
			t.Fatalf("money_cents cents = %d, want 2550", cents)
		}
	})
}

// TestFloatDivByZeroFamilyStillRaises confirms that the floored/integer-valued
// division helpers (div, divmod, modulo, remainder) keep raising on a zero
// divisor like Ruby, since only fdiv and `/` opt into IEEE special values.
func TestFloatDivByZeroFamilyStillRaises(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{"1.0.div(0)", "float.div by zero"},
		{"1.0.divmod(0)", "float.divmod by zero"},
		{"1.0.modulo(0)", "float.modulo by zero"},
		{"1.0.remainder(0)", "float.remainder by zero"},
		{"1.0.div(0.0)", "float.div by zero"},
		{"1.0.modulo(0.0)", "float.modulo by zero"},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

// TestNumericDivmodInfiniteDivisor verifies that Float#divmod accepts the
// +/-Infinity divisors now produced by IEEE float division. The quotient is
// Ruby's floored sign-aware ratio (0 when the operands share a sign or the
// receiver is zero, -1 otherwise) and the modulo follows the divisor's sign,
// matching MRI exactly. Before the fix the quotient recovery degenerated to
// Inf/Inf = NaN and raised "result out of int64 range".
func TestNumericDivmodInfiniteDivisor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		expr     string
		quotient int64
		modulo   float64 // checked only when modInf == 0
		modInf   int     // +1, -1, or 0 when the modulo is finite
	}{
		{name: "pos receiver pos inf", expr: "1.0.divmod(1.0 / 0)", quotient: 0, modulo: 1.0},
		{name: "pos receiver neg inf", expr: "1.0.divmod(-1.0 / 0)", quotient: -1, modInf: -1},
		{name: "neg receiver pos inf", expr: "(-1.0).divmod(1.0 / 0)", quotient: -1, modInf: 1},
		{name: "neg receiver neg inf", expr: "(-1.0).divmod(-1.0 / 0)", quotient: 0, modulo: -1.0},
		{name: "zero receiver pos inf", expr: "0.0.divmod(1.0 / 0)", quotient: 0, modulo: 0.0},
		{name: "zero receiver neg inf", expr: "0.0.divmod(-1.0 / 0)", quotient: 0, modulo: 0.0},
		{name: "int receiver pos inf", expr: "5.divmod(1.0 / 0)", quotient: 0, modulo: 5.0},
		{name: "fdiv infinite divisor", expr: "1.0.divmod((-1).fdiv(0))", quotient: -1, modInf: -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, tc.expr)
			if got.Kind() != KindArray {
				t.Fatalf("%s kind = %v, want array", tc.expr, got.Kind())
			}
			pair := got.Array()
			if len(pair) != 2 {
				t.Fatalf("%s length = %d, want 2", tc.expr, len(pair))
			}
			if pair[0].Kind() != KindInt || pair[0].Int() != tc.quotient {
				t.Fatalf("%s quotient = %v, want int %d", tc.expr, pair[0], tc.quotient)
			}
			if pair[1].Kind() != KindFloat {
				t.Fatalf("%s modulo kind = %v, want float", tc.expr, pair[1].Kind())
			}
			mod := pair[1].Float()
			if tc.modInf != 0 {
				if !math.IsInf(mod, tc.modInf) {
					t.Fatalf("%s modulo = %v, want infinity with sign %d", tc.expr, mod, tc.modInf)
				}
				return
			}
			if mod != tc.modulo {
				t.Fatalf("%s modulo = %v, want %v", tc.expr, mod, tc.modulo)
			}
		})
	}
}

// TestNumericDivInfiniteDivisor verifies that Float#div returns Ruby's floored
// integer quotient for an infinite divisor (always 0 for a finite receiver,
// since the ratio is +/-0.0) rather than raising.
func TestNumericDivInfiniteDivisor(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		"1.0.div(1.0 / 0)",
		"1.0.div(-1.0 / 0)",
		"(-1.0).div(1.0 / 0)",
		"5.div(1.0 / 0)",
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, expr)
			if got.Kind() != KindInt || got.Int() != 0 {
				t.Fatalf("%s = %v, want int 0", expr, got)
			}
		})
	}
}

// TestNumericModuloInfiniteDivisor verifies that Float#modulo (and %) follow
// Ruby for an infinite divisor: the result is the receiver when the operands
// share a sign and +/-Infinity (the divisor's sign) otherwise.
func TestNumericModuloInfiniteDivisor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		expr    string
		value   float64 // checked only when wantInf == 0
		wantInf int
	}{
		{name: "pos receiver pos inf", expr: "1.0.modulo(1.0 / 0)", value: 1.0},
		{name: "pos receiver neg inf", expr: "1.0.modulo(-1.0 / 0)", wantInf: -1},
		{name: "neg receiver pos inf", expr: "(-1.0).modulo(1.0 / 0)", wantInf: 1},
		{name: "neg receiver neg inf", expr: "(-1.0).modulo(-1.0 / 0)", value: -1.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, tc.expr)
			if got.Kind() != KindFloat {
				t.Fatalf("%s kind = %v, want float", tc.expr, got.Kind())
			}
			f := got.Float()
			if tc.wantInf != 0 {
				if !math.IsInf(f, tc.wantInf) {
					t.Fatalf("%s = %v, want infinity with sign %d", tc.expr, f, tc.wantInf)
				}
				return
			}
			if f != tc.value {
				t.Fatalf("%s = %v, want %v", tc.expr, f, tc.value)
			}
		})
	}
}

// Non-finite floats produced by zero-division must not slip into index
// coercion, and NaN must make ordering helpers fail rather than compare equal.
func TestNonFiniteFloatRejectedInIndexAndSort(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want string
	}{
		{"infinity index via fetch", `[1].fetch(1.0 / 0, "fallback")`, "array.fetch index must be integer"},
		{"nan in sort", `[2.0, 0.0 / 0.0, 1.0].sort`, "array.sort values are not comparable"},
		{"nan in min", `[2.0, 0.0 / 0.0].min`, "array.min values are not comparable"},
		{"nan in max", `[2.0, 0.0 / 0.0].max`, "array.max values are not comparable"},
		{"infinity epoch in Time.at", `Time.at(1.0 / 0)`, "Time.at expects a finite numeric epoch"},
		{"nan epoch in Time.at", `Time.at(0.0 / 0.0)`, "Time.at expects a finite numeric epoch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

// Numeric#remainder follows the dividend's sign, so an infinite divisor leaves
// a finite dividend unchanged when signs agree and yields NaN otherwise
// (matching Ruby, and unlike modulo which yields the signed infinity).
func TestNumericRemainderInfiniteDivisor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		expr    string
		value   float64 // checked when !wantNaN
		wantNaN bool
	}{
		{name: "pos receiver pos inf returns receiver", expr: "1.0.remainder(1.0 / 0)", value: 1.0},
		{name: "pos receiver neg inf is NaN", expr: "1.0.remainder(-1.0 / 0)", wantNaN: true},
		{name: "neg receiver pos inf is NaN", expr: "(-1.0).remainder(1.0 / 0)", wantNaN: true},
		{name: "neg receiver neg inf returns receiver", expr: "(-1.0).remainder(-1.0 / 0)", value: -1.0},
		{name: "fractional receiver pos inf returns receiver", expr: "2.5.remainder(1.0 / 0)", value: 2.5},
		{name: "zero receiver pos inf is zero", expr: "0.0.remainder(1.0 / 0)", value: 0.0},
		{name: "infinite receiver finite divisor is NaN", expr: "(1.0 / 0).remainder(2.0)", wantNaN: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, tc.expr)
			if got.Kind() != KindFloat {
				t.Fatalf("%s kind = %v, want float", tc.expr, got.Kind())
			}
			f := got.Float()
			if tc.wantNaN {
				if !math.IsNaN(f) {
					t.Fatalf("%s = %v, want NaN", tc.expr, f)
				}
				return
			}
			if f != tc.value {
				t.Fatalf("%s = %v, want %v", tc.expr, f, tc.value)
			}
		})
	}
}
