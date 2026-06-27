package runtime

import "testing"

func TestIntUptoDownto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want int64
	}{
		// upto yields ascending and folds digits left to right.
		{"upto ascending", "1.upto(3) { |i| acc = acc * 10 + i }", 123},
		{"upto single", "5.upto(5) { |i| acc = acc * 10 + i }", 5},
		{"upto empty", "5.upto(3) { |i| acc = acc * 10 + i }", 0},
		{"upto negative span", "(-2).upto(1) { |i| acc = acc + i }", -2},
		// downto yields descending.
		{"downto descending", "3.downto(1) { |i| acc = acc * 10 + i }", 321},
		{"downto single", "5.downto(5) { |i| acc = acc * 10 + i }", 5},
		{"downto empty", "3.downto(5) { |i| acc = acc * 10 + i }", 0},
		{"downto into negatives", "1.downto(-2) { |i| acc = acc + i }", -2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := "def run()\n  acc = 0\n  " + tc.expr + "\n  acc\nend"
			got := callFunc(t, compileScript(t, source), "run", nil)
			if !got.Equal(NewInt(tc.want)) {
				t.Fatalf("%s = %v, want %d", tc.expr, got, tc.want)
			}
		})
	}
}

func TestIntUptoDowntoReturnsReceiver(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{"7.upto(9) { |i| i }", "7.downto(5) { |i| i }"} {
		source := "def run()\n  " + expr + "\nend"
		got := callFunc(t, compileScript(t, source), "run", nil)
		if !got.Equal(NewInt(7)) {
			t.Fatalf("%s returned %v, want receiver 7", expr, got)
		}
	}
}

func TestIntStep(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want int64
	}{
		// step defaults to 1 and yields each value up to the limit.
		{"default step", "1.step(3) { |i| acc = acc * 10 + i }", 123},
		{"positive step lands on limit", "1.step(10, 3) { |i| acc = acc * 100 + i }", 1040710},
		{"positive step overshoots limit", "1.step(8, 3) { |i| acc = acc * 100 + i }", 10407},
		{"negative step", "10.step(1, -3) { |i| acc = acc * 100 + i }", 10070401},
		{"single value", "5.step(5) { |i| acc = acc * 100 + i }", 5},
		{"empty positive", "5.step(1) { |i| acc = acc + i }", 0},
		{"empty negative", "1.step(5, -1) { |i| acc = acc + i }", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := "def run()\n  acc = 0\n  " + tc.expr + "\n  acc\nend"
			got := callFunc(t, compileScript(t, source), "run", nil)
			if !got.Equal(NewInt(tc.want)) {
				t.Fatalf("%s = %v, want %d", tc.expr, got, tc.want)
			}
		})
	}
}

func TestIntStepReturnsReceiver(t *testing.T) {
	t.Parallel()

	source := "def run()\n  4.step(10, 2) { |i| i }\nend"
	got := callFunc(t, compileScript(t, source), "run", nil)
	if !got.Equal(NewInt(4)) {
		t.Fatalf("step returned %v, want receiver 4", got)
	}
}

func TestIntEnumerationArgumentRejection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{"upto no block", "1.upto(3)", "requires a block"},
		{"upto no arg", "1.upto { |i| i }", "expects one integer argument"},
		{"upto float limit", "1.upto(3.5) { |i| i }", "expects an integer limit"},
		{"upto kwarg", "1.upto(3, by: 2) { |i| i }", "does not take keyword arguments"},
		{"downto no block", "3.downto(1)", "requires a block"},
		{"downto float limit", "3.downto(1.0) { |i| i }", "expects an integer limit"},
		{"step no block", "1.step(3)", "requires a block"},
		{"step zero", "1.step(3, 0) { |i| i }", "must not be zero"},
		{"step float step", "1.step(3, 1.5) { |i| i }", "expects an integer step"},
		{"step float limit", "1.step(3.0) { |i| i }", "expects an integer limit"},
		{"step too many args", "1.step(3, 1, 1) { |i| i }", "limit and an optional step"},
		{"step kwarg", "1.step(3, by: 1) { |i| i }", "does not take keyword arguments"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := "def run()\n  " + tc.expr + "\nend"
			requireCallErrorContains(t, compileScript(t, source), "run", nil, CallOptions{}, tc.want)
		})
	}
}

func TestIntEnumerationStepQuota(t *testing.T) {
	t.Parallel()

	// Each yield consumes a step, so a wide span trips the step limit rather than
	// running unbounded.
	tests := []string{
		"1.upto(1000000) { |i| i }",
		"1000000.downto(1) { |i| i }",
		"1.step(1000000) { |i| i }",
	}
	for _, expr := range tests {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			source := "def run()\n  " + expr + "\nend"
			script := compileScriptWithConfig(t, Config{StepQuota: 100, MemoryQuotaBytes: 64 << 20}, source)
			requireCallRuntimeErrorType(t, script, "run", nil, CallOptions{}, runtimeErrorTypeLimit)
		})
	}
}
