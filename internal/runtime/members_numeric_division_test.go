package runtime

import (
	"math"
	"testing"
)

func TestNumericDivHelper(t *testing.T) {
	t.Parallel()

	// div performs floored division and always returns an integer, matching
	// Ruby's Numeric#div including the sign behavior for negative operands.
	tests := []struct {
		expr string
		want int64
	}{
		{"5.div(2)", 2},
		{"(-5).div(2)", -3},
		{"5.div(-2)", -3},
		{"(-5).div(-2)", 2},
		{"6.div(3)", 2},
		{"5.5.div(2)", 2},
		{"(-5.5).div(2)", -3},
		{"5.5.div(2.0)", 2},
		{"5.div(2.0)", 2},
		{"7.5.div(2.5)", 3},
	}

	for _, tc := range tests {
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
}

func TestNumericDivmodHelper(t *testing.T) {
	t.Parallel()

	// Integer operands yield an [int, int] pair; any float operand makes the
	// modulo a float, mirroring Ruby's Integer#divmod and Float#divmod.
	intTests := []struct {
		expr     string
		quotient int64
		modulo   int64
	}{
		{"5.divmod(2)", 2, 1},
		{"(-5).divmod(2)", -3, 1},
		{"5.divmod(-2)", -3, -1},
		{"(-5).divmod(-2)", 2, -1},
	}
	for _, tc := range intTests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, tc.expr)
			if got.Kind() != KindArray {
				t.Fatalf("%s kind = %v, want array", tc.expr, got.Kind())
			}
			pair := got.Array()
			if len(pair) != 2 {
				t.Fatalf("%s length = %d, want 2", tc.expr, len(pair))
			}
			if pair[0].Kind() != KindInt || pair[1].Kind() != KindInt {
				t.Fatalf("%s kinds = [%v, %v], want [int, int]", tc.expr, pair[0].Kind(), pair[1].Kind())
			}
			if pair[0].Int() != tc.quotient || pair[1].Int() != tc.modulo {
				t.Fatalf("%s = [%d, %d], want [%d, %d]", tc.expr, pair[0].Int(), pair[1].Int(), tc.quotient, tc.modulo)
			}
		})
	}

	floatTests := []struct {
		expr     string
		quotient int64
		modulo   float64
	}{
		{"5.5.divmod(2)", 2, 1.5},
		{"(-5.5).divmod(2)", -3, 0.5},
		{"5.divmod(2.0)", 2, 1.0},
		{"5.divmod(2.5)", 2, 0.0},
		{"7.5.divmod(2.5)", 3, 0.0},
	}
	for _, tc := range floatTests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, tc.expr)
			if got.Kind() != KindArray {
				t.Fatalf("%s kind = %v, want array", tc.expr, got.Kind())
			}
			pair := got.Array()
			if len(pair) != 2 {
				t.Fatalf("%s length = %d, want 2", tc.expr, len(pair))
			}
			if pair[0].Kind() != KindInt {
				t.Fatalf("%s quotient kind = %v, want int", tc.expr, pair[0].Kind())
			}
			if pair[1].Kind() != KindFloat {
				t.Fatalf("%s modulo kind = %v, want float", tc.expr, pair[1].Kind())
			}
			if pair[0].Int() != tc.quotient || pair[1].Float() != tc.modulo {
				t.Fatalf("%s = [%d, %g], want [%d, %g]", tc.expr, pair[0].Int(), pair[1].Float(), tc.quotient, tc.modulo)
			}
		})
	}
}

func TestNumericFdivHelper(t *testing.T) {
	t.Parallel()

	// fdiv always returns a float, regardless of operand kinds.
	tests := []struct {
		expr string
		want float64
	}{
		{"5.fdiv(2)", 2.5},
		{"5.fdiv(2.0)", 2.5},
		{"5.0.fdiv(2)", 2.5},
		{"5.5.fdiv(2)", 2.75},
		{"5.fdiv(2.5)", 2.0},
		{"(-5).fdiv(2)", -2.5},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, tc.expr)
			if got.Kind() != KindFloat {
				t.Fatalf("%s kind = %v, want float", tc.expr, got.Kind())
			}
			if got.Float() != tc.want {
				t.Fatalf("%s = %g, want %g", tc.expr, got.Float(), tc.want)
			}
		})
	}
}

func TestNumericRemainderHelper(t *testing.T) {
	t.Parallel()

	// remainder takes the dividend's sign (truncated division), which differs
	// from `%` for operands of opposite sign. Integer operands return an int;
	// any float operand returns a float.
	intTests := []struct {
		expr string
		want int64
	}{
		{"5.remainder(2)", 1},
		{"(-5).remainder(2)", -1},
		{"5.remainder(-2)", 1},
		{"(-5).remainder(-2)", -1},
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

	floatTests := []struct {
		expr string
		want float64
	}{
		{"5.5.remainder(2)", 1.5},
		{"(-5.5).remainder(2)", -1.5},
		{"10.5.remainder(3)", 1.5},
		{"(-10.5).remainder(3)", -1.5},
		{"5.remainder(2.5)", 0.0},
	}
	for _, tc := range floatTests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			got := evalNumericExpr(t, tc.expr)
			if got.Kind() != KindFloat {
				t.Fatalf("%s kind = %v, want float", tc.expr, got.Kind())
			}
			if got.Float() != tc.want {
				t.Fatalf("%s = %g, want %g", tc.expr, got.Float(), tc.want)
			}
		})
	}
}

func TestNumericRemainderDivergesFromModulo(t *testing.T) {
	t.Parallel()

	// For operands of opposite sign Ruby's remainder and `%` differ: remainder
	// follows the dividend while `%` follows the divisor.
	if got := evalNumericExpr(t, "(-5).remainder(2)"); !got.Equal(NewInt(-1)) {
		t.Fatalf("(-5).remainder(2) = %v, want -1", got)
	}
	if got := evalNumericExpr(t, "(-5) % 2"); !got.Equal(NewInt(1)) {
		t.Fatalf("(-5) %% 2 = %v, want 1", got)
	}
}

func TestNumericDivmodReconstructsDividend(t *testing.T) {
	t.Parallel()

	// The divmod identity self == quotient*divisor + modulo must hold for both
	// integer and mixed operands.
	cases := []struct {
		expr     string
		dividend float64
		divisor  float64
	}{
		{"7.divmod(3)", 7, 3},
		{"(-7).divmod(3)", -7, 3},
		{"7.divmod(-3)", 7, -3},
		{"(-7).divmod(-3)", -7, -3},
		{"7.5.divmod(2)", 7.5, 2},
		{"(-7.5).divmod(2)", -7.5, 2},
		{"7.divmod(2.5)", 7, 2.5},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			pair := evalNumericExpr(t, tc.expr).Array()
			quotient := pair[0].Float()
			modulo := pair[1].Float()
			reconstructed := quotient*tc.divisor + modulo
			if math.Abs(reconstructed-tc.dividend) > 1e-9 {
				t.Fatalf("%s: quotient*divisor+modulo = %g, want dividend %g", tc.expr, reconstructed, tc.dividend)
			}
		})
	}
}

func TestNumericDivisionZeroErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{"5.div(0)", "int.div by zero"},
		{"5.divmod(0)", "int.divmod by zero"},
		{"5.remainder(0)", "int.remainder by zero"},
		{"5.fdiv(0)", "int.fdiv by zero"},
		{"5.div(0.0)", "int.div by zero"},
		{"5.divmod(0.0)", "int.divmod by zero"},
		{"5.remainder(0.0)", "int.remainder by zero"},
		{"5.fdiv(0.0)", "int.fdiv by zero"},
		{"5.0.div(0.0)", "float.div by zero"},
		{"5.0.divmod(0.0)", "float.divmod by zero"},
		{"5.0.remainder(0.0)", "float.remainder by zero"},
		{"5.0.fdiv(0.0)", "float.fdiv by zero"},
		{"5.0.fdiv(0)", "float.fdiv by zero"},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

func TestNumericDivisionArgumentDiagnostics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{"5.div()", "int.div expects one numeric argument"},
		{"5.div(1, 2)", "int.div expects one numeric argument"},
		{"5.divmod()", "int.divmod expects one numeric argument"},
		{"5.fdiv()", "int.fdiv expects one numeric argument"},
		{"5.remainder()", "int.remainder expects one numeric argument"},
		{"5.div(\"x\")", "int.div expects a numeric argument"},
		{"5.divmod(\"x\")", "int.divmod expects a numeric argument"},
		{"5.fdiv(true)", "int.fdiv expects a numeric argument"},
		{"5.remainder(nil)", "int.remainder expects a numeric argument"},
		{"5.0.div(\"x\")", "float.div expects a numeric argument"},
		{"5.0.divmod()", "float.divmod expects one numeric argument"},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

func TestNumericDivisionOverflow(t *testing.T) {
	t.Parallel()

	// Vibescript integers are 64-bit, so the single quotient that escapes that
	// range (MinInt64 / -1) errors instead of wrapping, like abs and succ.
	divScript := compileScript(t, "def run(n)\n  n.div(-1)\nend")
	requireCallErrorContains(t, divScript, "run", []Value{NewInt(math.MinInt64)}, CallOptions{}, "int.div result out of int64 range")

	divmodScript := compileScript(t, "def run(n)\n  n.divmod(-1)\nend")
	requireCallErrorContains(t, divmodScript, "run", []Value{NewInt(math.MinInt64)}, CallOptions{}, "int.divmod result out of int64 range")

	// A float quotient outside the int64 range also errors rather than
	// truncating to an arbitrary value.
	floatDivScript := compileScript(t, "def run(n)\n  n.div(1)\nend")
	requireCallErrorContains(t, floatDivScript, "run", []Value{NewFloat(1e300)}, CallOptions{}, "float.div result out of int64 range")
}
