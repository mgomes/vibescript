package runtime

import "testing"

// evalIntExpr compiles and runs a single expression in a run() body, returning
// its value. It mirrors evalRangeExpr for the integer iteration helpers.
func evalIntExpr(t *testing.T, expr string) Value {
	t.Helper()
	script := compileScript(t, "def run()\n  "+expr+"\nend")
	return callFunc(t, script, "run", nil)
}

func TestIntUptoDownto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want int64
	}{
		// Blocks reassign the closed-over accumulator, the value-semantics-safe way
		// to observe each yielded value.
		{"upto ascending", "total = 0\n  1.upto(4) { |i| total = total + i }\n  total", 10},
		{"upto single", "total = 0\n  3.upto(3) { |i| total = total + i }\n  total", 3},
		{"upto empty when start exceeds limit", "total = 99\n  5.upto(1) { |i| total = i }\n  total", 99},
		{"upto returns receiver", "5.upto(7) { |i| i }", 5},
		{"downto descending", "total = 0\n  4.downto(1) { |i| total = total + i }\n  total", 10},
		{"downto single", "total = 0\n  3.downto(3) { |i| total = total + i }\n  total", 3},
		{"downto empty when start below limit", "total = 99\n  1.downto(5) { |i| total = i }\n  total", 99},
		{"downto returns receiver", "7.downto(5) { |i| i }", 7},
		{"upto negative span", "total = 0\n  (-3).upto(-1) { |i| total = total + i }\n  total", -6},
		{"downto negative span", "total = 0\n  (-1).downto(-3) { |i| total = total + i }\n  total", -6},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalIntExpr(t, tc.expr)
			if !got.Equal(NewInt(tc.want)) {
				t.Fatalf("%s = %v, want %d", tc.expr, got, tc.want)
			}
		})
	}
}

func TestIntStep(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want int64
	}{
		{"step ascending lands on limit", "total = 0\n  1.step(10, 3) { |i| total = total + i }\n  total", 22},
		{"step ascending stops before limit", "total = 0\n  1.step(9, 3) { |i| total = total + i }\n  total", 12},
		{"step default of one", "total = 0\n  1.step(5) { |i| total = total + i }\n  total", 15},
		{"step descending", "total = 0\n  10.step(1, -3) { |i| total = total + i }\n  total", 22},
		{"step single when limit equals start", "total = 0\n  4.step(4, 2) { |i| total = total + i }\n  total", 4},
		{"step ascending past limit yields nothing", "total = 99\n  5.step(1, 2) { |i| total = i }\n  total", 99},
		{"step descending past limit yields nothing", "total = 99\n  1.step(5, -2) { |i| total = i }\n  total", 99},
		{"step returns receiver", "3.step(9, 2) { |i| i }", 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalIntExpr(t, tc.expr)
			if !got.Equal(NewInt(tc.want)) {
				t.Fatalf("%s = %v, want %d", tc.expr, got, tc.want)
			}
		})
	}
}

func TestIntIterationArgumentRejection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{"upto no block", "1.upto(3)", "requires a block"},
		{"downto no block", "3.downto(1)", "requires a block"},
		{"step no block", "1.step(5)", "requires a block"},
		{"upto no arg", "1.upto { |i| i }", "expects a limit"},
		{"upto extra arg", "1.upto(3, 5) { |i| i }", "expects a limit"},
		{"upto non-int", "1.upto(\"3\") { |i| i }", "integer limit"},
		{"downto non-int", "1.downto(2.5) { |i| i }", "integer limit"},
		{"step no arg", "1.step { |i| i }", "expects a limit"},
		{"step extra arg", "1.step(5, 1, 2) { |i| i }", "expects a limit"},
		{"step non-int limit", "1.step(\"5\") { |i| i }", "integer limit"},
		{"step non-int step", "1.step(5, 2.5) { |i| i }", "integer step"},
		{"step zero", "1.step(5, 0) { |i| i }", "must not be zero"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

func TestIntIterationStepQuota(t *testing.T) {
	t.Parallel()

	// Each yielded value consumes a step, so iterating over a span far larger
	// than the step quota stops on the step limit rather than running unbounded.
	tests := []struct {
		name string
		expr string
	}{
		{"upto", "1.upto(1000000) { |i| i }"},
		{"downto", "1000000.downto(1) { |i| i }"},
		{"step", "1.step(1000000, 1) { |i| i }"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := "def run()\n  " + tc.expr + "\nend"
			script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: 64 << 20}, source)
			requireCallRuntimeErrorType(t, script, "run", nil, CallOptions{}, runtimeErrorTypeLimit)
		})
	}
}

func TestIntStepOverflowTerminates(t *testing.T) {
	t.Parallel()

	// A stride whose advance would overflow int64 must terminate after the final
	// in-range yield rather than wrapping and restarting iteration. Stepping from
	// a near-MaxInt64 start lands on the limit and stops; a regression that let
	// current + stride wrap would loop until the step quota tripped.
	source := "def run()\n  count = 0\n  9223372036854775804.step(9223372036854775806, 2) { |i| count = count + 1 }\n  count\nend"
	script := compileScriptWithConfig(t, Config{StepQuota: 1000, MemoryQuotaBytes: 64 << 20}, source)
	got := callFunc(t, script, "run", nil)
	if !got.Equal(NewInt(2)) {
		t.Fatalf("step near MaxInt64 yielded %v times, want 2", got)
	}
}
