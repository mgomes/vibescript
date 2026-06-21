package runtime

import (
	"math"
	"testing"
)

func evalNumericExpr(t *testing.T, expr string) Value {
	t.Helper()
	script := compileScript(t, "def run()\n  "+expr+"\nend")
	return callFunc(t, script, "run", nil)
}

func TestNumericPredicateHelpers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want bool
	}{
		{"int zero true", "0.zero?", true},
		{"int zero false", "5.zero?", false},
		{"int positive true", "5.positive?", true},
		{"int positive false zero", "0.positive?", false},
		{"int positive false neg", "(-5).positive?", false},
		{"int negative true", "(-1).negative?", true},
		{"int negative false zero", "0.negative?", false},
		{"int negative false pos", "1.negative?", false},
		{"float zero true", "0.0.zero?", true},
		{"float zero false", "1.5.zero?", false},
		{"float positive true", "1.5.positive?", true},
		{"float positive false", "(-1.5).positive?", false},
		{"float negative true", "(-1.5).negative?", true},
		{"float negative false", "1.5.negative?", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
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

func TestNumericNonzeroHelper(t *testing.T) {
	t.Parallel()

	// Ruby's nonzero? returns the receiver when nonzero and nil when zero.
	if got := evalNumericExpr(t, "0.nonzero?"); got.Kind() != KindNil {
		t.Fatalf("0.nonzero? = %v, want nil", got)
	}
	if got := evalNumericExpr(t, "7.nonzero?"); !got.Equal(NewInt(7)) {
		t.Fatalf("7.nonzero? = %v, want 7", got)
	}
	if got := evalNumericExpr(t, "(-3).nonzero?"); !got.Equal(NewInt(-3)) {
		t.Fatalf("(-3).nonzero? = %v, want -3", got)
	}
	if got := evalNumericExpr(t, "0.0.nonzero?"); got.Kind() != KindNil {
		t.Fatalf("0.0.nonzero? = %v, want nil", got)
	}
	if got := evalNumericExpr(t, "2.5.nonzero?"); !got.Equal(NewFloat(2.5)) {
		t.Fatalf("2.5.nonzero? = %v, want 2.5", got)
	}
}

func TestIntegerSuccessorHelpers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want int64
	}{
		{"1.next", 2},
		{"1.succ", 2},
		{"1.pred", 0},
		{"(-1).next", 0},
		{"(-1).succ", 0},
		{"(-1).pred", -2},
		{"0.pred", -1},
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

func TestNumericHelperArgumentRejection(t *testing.T) {
	t.Parallel()

	exprs := []string{
		"0.zero?(1)", "1.positive?(1)", "1.negative?(1)", "1.nonzero?(1)",
		"1.next(1)", "1.succ(1)", "1.pred(1)",
		"0.0.zero?(1)", "1.5.positive?(1)", "1.5.negative?(1)", "1.5.nonzero?(1)",
	}
	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "does not take arguments")
		})
	}
}

func TestIntegerSuccessorOverflow(t *testing.T) {
	t.Parallel()

	// succ/pred cannot grow beyond int64, unlike Ruby's arbitrary-precision
	// integers, so the boundary cases report an overflow rather than wrapping.
	succScript := compileScript(t, "def run(n)\n  n.succ\nend")
	requireCallErrorContains(t, succScript, "run", []Value{NewInt(math.MaxInt64)}, CallOptions{}, "int.succ overflow")

	nextScript := compileScript(t, "def run(n)\n  n.next\nend")
	requireCallErrorContains(t, nextScript, "run", []Value{NewInt(math.MaxInt64)}, CallOptions{}, "int.next overflow")

	predScript := compileScript(t, "def run(n)\n  n.pred\nend")
	requireCallErrorContains(t, predScript, "run", []Value{NewInt(math.MinInt64)}, CallOptions{}, "int.pred overflow")
}
