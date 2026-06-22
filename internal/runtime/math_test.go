package runtime

import (
	"fmt"
	"math"
	"testing"
)

// callMathExpr compiles and runs a single expression inside a `def run` so the
// Math namespace resolves through the normal call root.
func callMathExpr(t *testing.T, expr string) Value {
	t.Helper()
	script := compileScript(t, fmt.Sprintf("def run\n  %s\nend\n", expr))
	return callFunc(t, script, "run", nil)
}

func requireFloat(t *testing.T, value Value, want float64) {
	t.Helper()
	if value.Kind() != KindFloat {
		t.Fatalf("expected float, got %s", value.Kind())
	}
	if got := value.Float(); got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMathConstants(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		expr string
		want float64
	}{
		{name: "pi_scope", expr: "Math::PI", want: math.Pi},
		{name: "e_scope", expr: "Math::E", want: math.E},
		// Vibescript namespace objects expose the same members through `.`
		// and `::`, so the dot accessor reaches the constant too.
		{name: "pi_dot", expr: "Math.PI", want: math.Pi},
		{name: "e_dot", expr: "Math.E", want: math.E},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireFloat(t, callMathExpr(t, tc.expr), tc.want)
		})
	}
}

func TestMathFunctionsHappyPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		expr string
		want float64
	}{
		{name: "sqrt", expr: "Math.sqrt(9)", want: 3},
		{name: "sqrt_float", expr: "Math.sqrt(2.0)", want: math.Sqrt2},
		{name: "cbrt", expr: "Math.cbrt(27)", want: 3},
		{name: "cbrt_negative", expr: "Math.cbrt(-27)", want: -3},
		{name: "sin", expr: "Math.sin(0)", want: 0},
		{name: "cos", expr: "Math.cos(0)", want: 1},
		{name: "tan", expr: "Math.tan(0)", want: 0},
		{name: "asin", expr: "Math.asin(0)", want: 0},
		{name: "acos", expr: "Math.acos(1)", want: 0},
		{name: "atan", expr: "Math.atan(0)", want: 0},
		{name: "atan2", expr: "Math.atan2(0, 1)", want: 0},
		{name: "exp", expr: "Math.exp(0)", want: 1},
		{name: "exp_one", expr: "Math.exp(1)", want: math.E},
		{name: "log_natural", expr: "Math.log(1)", want: 0},
		{name: "log_e", expr: "Math.log(Math::E)", want: 1},
		{name: "log_base", expr: "Math.log(8, 2)", want: 3},
		{name: "log2", expr: "Math.log2(8)", want: 3},
		{name: "log10", expr: "Math.log10(100)", want: 2},
		{name: "hypot", expr: "Math.hypot(3, 4)", want: 5},
		// `::` reaches module functions too, mirroring Ruby's Math::sqrt.
		{name: "sqrt_scope", expr: "Math::sqrt(9)", want: 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireFloat(t, callMathExpr(t, tc.expr), tc.want)
		})
	}
}

func TestMathReturnsFloat(t *testing.T) {
	t.Parallel()
	// Ruby's Math always yields a Float, even for integer arguments with an
	// integral result; Vibescript matches that so downstream float methods work.
	value := callMathExpr(t, "Math.sqrt(4)")
	if value.Kind() != KindFloat {
		t.Fatalf("Math.sqrt(4) should be a float, got %s", value.Kind())
	}
}

func TestMathSpecialValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		expr   string
		assert func(*testing.T, float64)
	}{
		{
			name: "log_zero_is_negative_infinity",
			expr: "Math.log(0)",
			assert: func(t *testing.T, got float64) {
				if !math.IsInf(got, -1) {
					t.Fatalf("got %v, want -Inf", got)
				}
			},
		},
		{
			name: "log10_zero_is_negative_infinity",
			expr: "Math.log10(0)",
			assert: func(t *testing.T, got float64) {
				if !math.IsInf(got, -1) {
					t.Fatalf("got %v, want -Inf", got)
				}
			},
		},
		{
			name: "log_base_zero_is_negative_infinity",
			expr: "Math.log(0, 2)",
			assert: func(t *testing.T, got float64) {
				if !math.IsInf(got, -1) {
					t.Fatalf("got %v, want -Inf", got)
				}
			},
		},
		{
			name: "sqrt_infinity_is_infinity",
			expr: "Math.sqrt(1.0 / 0)",
			assert: func(t *testing.T, got float64) {
				if !math.IsInf(got, 1) {
					t.Fatalf("got %v, want +Inf", got)
				}
			},
		},
		{
			name: "nan_propagates",
			expr: "Math.sqrt(0.0 / 0)",
			assert: func(t *testing.T, got float64) {
				if !math.IsNaN(got) {
					t.Fatalf("got %v, want NaN", got)
				}
			},
		},
		{
			name: "log_nan_propagates",
			expr: "Math.log(0.0 / 0)",
			assert: func(t *testing.T, got float64) {
				if !math.IsNaN(got) {
					t.Fatalf("got %v, want NaN", got)
				}
			},
		},
		{
			// Ruby's Math.sin/cos/tan have no domain restriction, so an
			// infinite argument yields NaN (per IEEE 754) instead of raising.
			name: "sin_infinity_is_nan",
			expr: "Math.sin(1.0 / 0)",
			assert: func(t *testing.T, got float64) {
				if !math.IsNaN(got) {
					t.Fatalf("got %v, want NaN", got)
				}
			},
		},
		{
			name: "cos_infinity_is_nan",
			expr: "Math.cos(1.0 / 0)",
			assert: func(t *testing.T, got float64) {
				if !math.IsNaN(got) {
					t.Fatalf("got %v, want NaN", got)
				}
			},
		},
		{
			name: "tan_negative_infinity_is_nan",
			expr: "Math.tan(-1.0 / 0)",
			assert: func(t *testing.T, got float64) {
				if !math.IsNaN(got) {
					t.Fatalf("got %v, want NaN", got)
				}
			},
		},
		{
			// Ruby's atan accepts the full real line including infinities,
			// returning the finite limit PI/2 rather than NaN.
			name: "atan_infinity_is_half_pi",
			expr: "Math.atan(1.0 / 0)",
			assert: func(t *testing.T, got float64) {
				if got != math.Pi/2 {
					t.Fatalf("got %v, want %v", got, math.Pi/2)
				}
			},
		},
		{
			// Ruby computes Math.log(x, base) as log(x)/log(base); a base of
			// exactly 1 makes log(base) zero, so the result is +Infinity rather
			// than a spurious domain error or corrupted finite value.
			name: "log_base_one_is_infinity",
			expr: "Math.log(8, 1)",
			assert: func(t *testing.T, got float64) {
				if !math.IsInf(got, 1) {
					t.Fatalf("got %v, want +Inf", got)
				}
			},
		},
		{
			// log(1)/log(1) is 0/0, which is NaN under IEEE 754, matching Ruby.
			name: "log_one_base_one_is_nan",
			expr: "Math.log(1, 1)",
			assert: func(t *testing.T, got float64) {
				if !math.IsNaN(got) {
					t.Fatalf("got %v, want NaN", got)
				}
			},
		},
		{
			// log(x)/log(0) is finite/-Inf, which is -0.0 in Ruby and IEEE 754.
			name: "log_base_zero_is_negative_zero",
			expr: "Math.log(8, 0)",
			assert: func(t *testing.T, got float64) {
				if got != 0 || !math.Signbit(got) {
					t.Fatalf("got %v, want -0.0", got)
				}
			},
		},
		{
			// log(x)/log(Infinity) is finite/+Inf, which is 0.0.
			name: "log_base_infinity_is_zero",
			expr: "Math.log(8, 1.0 / 0)",
			assert: func(t *testing.T, got float64) {
				if got != 0 || math.Signbit(got) {
					t.Fatalf("got %v, want 0.0", got)
				}
			},
		},
		{
			// A NaN argument is never below or above the [-1, 1] domain bounds
			// (every comparison with NaN is false), so it propagates through
			// asin/acos instead of raising, matching Ruby.
			name: "asin_nan_propagates",
			expr: "Math.asin(0.0 / 0)",
			assert: func(t *testing.T, got float64) {
				if !math.IsNaN(got) {
					t.Fatalf("got %v, want NaN", got)
				}
			},
		},
		{
			name: "acos_nan_propagates",
			expr: "Math.acos(0.0 / 0)",
			assert: func(t *testing.T, got float64) {
				if !math.IsNaN(got) {
					t.Fatalf("got %v, want NaN", got)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			value := callMathExpr(t, tc.expr)
			if value.Kind() != KindFloat {
				t.Fatalf("expected float, got %s", value.Kind())
			}
			tc.assert(t, value.Float())
		})
	}
}

func TestMathDomainErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "sqrt_negative", expr: "Math.sqrt(-1)", want: "Math.sqrt out of domain"},
		{name: "log_negative", expr: "Math.log(-1)", want: "Math.log out of domain"},
		{name: "log_base_negative", expr: "Math.log(8, -2)", want: "Math.log out of domain"},
		{name: "log2_negative", expr: "Math.log2(-1)", want: "Math.log2 out of domain"},
		{name: "log10_negative", expr: "Math.log10(-1)", want: "Math.log10 out of domain"},
		{name: "asin_above_one", expr: "Math.asin(2)", want: "Math.asin out of domain"},
		{name: "asin_below_minus_one", expr: "Math.asin(-2)", want: "Math.asin out of domain"},
		{name: "acos_above_one", expr: "Math.acos(2)", want: "Math.acos out of domain"},
		// Infinity lies outside the [-1, 1] domain of asin/acos, so Ruby raises
		// rather than returning NaN.
		{name: "asin_infinity", expr: "Math.asin(1.0 / 0)", want: "Math.asin out of domain"},
		{name: "acos_infinity", expr: "Math.acos(1.0 / 0)", want: "Math.acos out of domain"},
		// Negative infinity is below the non-negative domain of sqrt and the
		// logarithms, so it raises instead of propagating as a float.
		{name: "sqrt_negative_infinity", expr: "Math.sqrt(-1.0 / 0)", want: "Math.sqrt out of domain"},
		{name: "log_negative_infinity", expr: "Math.log(-1.0 / 0)", want: "Math.log out of domain"},
		{name: "log2_negative_infinity", expr: "Math.log2(-1.0 / 0)", want: "Math.log2 out of domain"},
		{name: "log10_negative_infinity", expr: "Math.log10(-1.0 / 0)", want: "Math.log10 out of domain"},
		{name: "log_base_negative_infinity", expr: "Math.log(8, -1.0 / 0)", want: "Math.log out of domain"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, fmt.Sprintf("def run\n  %s\nend\n", tc.expr))
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

func TestMathArgumentErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "sqrt_non_numeric", expr: `Math.sqrt("x")`, want: "Math.sqrt expects a numeric argument, got string"},
		{name: "sqrt_too_many", expr: "Math.sqrt(1, 2)", want: "Math.sqrt expects 1 argument, got 2"},
		{name: "hypot_too_few", expr: "Math.hypot(1)", want: "Math.hypot expects 2 arguments, got 1"},
		{name: "atan2_non_numeric", expr: `Math.atan2(1, "x")`, want: "Math.atan2 expects a numeric argument, got string"},
		{name: "log_too_many", expr: "Math.log(1, 2, 3)", want: "Math.log expects 1 or 2 arguments, got 3"},
		{name: "sqrt_keyword", expr: "Math.sqrt(x: 1)", want: "Math.sqrt does not accept keyword arguments"},
		{name: "unknown_scope_member", expr: "Math::nope", want: "unknown member nope"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, fmt.Sprintf("def run\n  %s\nend\n", tc.expr))
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

func TestMathBlockRejected(t *testing.T) {
	t.Parallel()
	script := compileScript(t, "def run\n  Math.sqrt(4) do\n    1\n  end\nend\n")
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "Math.sqrt does not accept a block")
}

func TestMathNamespaceIsolatedPerCall(t *testing.T) {
	t.Parallel()
	// Mutating the namespace object in one call must not leak into the next,
	// the same isolation JSON and Time namespaces enjoy.
	script := compileScript(t, `
def poison
  Math[:PI] = 0
  Math[:PI]
end

def read
  Math::PI
end
`)
	if got := callFunc(t, script, "poison", nil); got.Kind() != KindInt || got.Int() != 0 {
		t.Fatalf("poison should observe its own mutation, got %#v", got)
	}
	requireFloat(t, callFunc(t, script, "read", nil), math.Pi)
}
