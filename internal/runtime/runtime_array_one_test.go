package runtime

import "testing"

// TestArrayOnePredicate exercises Array#one? for both the plain truthiness form
// and the block form, mirroring Ruby's Enumerable#one? semantics: true only when
// exactly one element (or block result) is truthy.
func TestArrayOnePredicate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   bool
	}{
		{name: "empty plain", script: `def run; [].one?; end`, want: false},
		{name: "single truthy plain", script: `def run; [1].one?; end`, want: true},
		{name: "two truthy plain", script: `def run; [1, 2].one?; end`, want: false},
		{name: "single truthy among falsey", script: `def run; [nil, false, 7, nil].one?; end`, want: true},
		{name: "all falsey plain", script: `def run; [nil, false].one?; end`, want: false},
		{name: "block exactly one match", script: `def run; [1, 2, 3].one? { |n| n.even? }; end`, want: true},
		{name: "block no match", script: `def run; [1, 3, 5].one? { |n| n.even? }; end`, want: false},
		{name: "block multiple match", script: `def run; [2, 4, 6].one? { |n| n.even? }; end`, want: false},
		{name: "block empty receiver", script: `def run; [].one? { |n| n.even? }; end`, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			got := callFunc(t, script, "run", nil)
			if got.Kind() != KindBool {
				t.Fatalf("one? returned %v, want bool", got.Kind())
			}
			if got.Bool() != tc.want {
				t.Fatalf("one? = %v, want %v", got.Bool(), tc.want)
			}
		})
	}
}

// TestArrayOneStopsEarly proves the plain form returns once a second truthy
// element is seen without scanning the rest of the receiver. The trailing element
// would raise if visited, so reaching false without an error confirms the early
// stop.
func TestArrayOneStopsEarly(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def run(values)
      values.one? do |v|
        if v == :boom
          raise "should not reach boom"
        end
        v
      end
    end
  `)
	receiver := NewArray([]Value{NewInt(1), NewInt(2), NewSymbol("boom")})
	got := callFunc(t, script, "run", []Value{receiver})
	if got.Kind() != KindBool || got.Bool() {
		t.Fatalf("one? = %v, want false without reaching boom", got)
	}
}

// TestArrayOneRejectsArguments mirrors the neighboring predicates: one? takes no
// positional arguments.
func TestArrayOneRejectsArguments(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run(values); values.one?(1); end`)
	receiver := NewArray([]Value{NewInt(1)})
	requireCallErrorContains(t, script, "run", []Value{receiver}, CallOptions{}, "array.one? does not take arguments")
}

// TestArrayOneHonorsStepQuota confirms block iteration charges steps, so one?
// over a large receiver trips the step limit even with ample memory.
func TestArrayOneHonorsStepQuota(t *testing.T) {
	t.Parallel()

	// The block never matches, so one? must walk the whole receiver. Each block
	// call charges a step, so the tight quota trips before the scan completes.
	source := `def run(values)
  values.one? { |v| v < 0 }
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 40, MemoryQuotaBytes: 64 << 20}, source)

	items := make([]Value, 2000)
	for i := range items {
		items[i] = NewInt(int64(i + 1))
	}
	requireCallRuntimeErrorType(t, script, "run", []Value{NewArray(items)}, CallOptions{}, runtimeErrorTypeLimit)
}

// TestArrayOneEmptyBlockHonorsStepQuota guards the no-op block case. An empty
// block evaluates no statements, so runner.call charges no steps; without a
// per-element step charge one? could scan the whole receiver and ignore the step
// quota (and, by the same path, context cancellation). The block never yields a
// truthy value, forcing a full scan, so the tight quota must still trip.
func TestArrayOneEmptyBlockHonorsStepQuota(t *testing.T) {
	t.Parallel()

	source := `def run(values)
  values.one? do |v|
  end
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 40, MemoryQuotaBytes: 64 << 20}, source)

	items := make([]Value, 2000)
	for i := range items {
		items[i] = NewInt(int64(i + 1))
	}
	requireCallRuntimeErrorType(t, script, "run", []Value{NewArray(items)}, CallOptions{}, runtimeErrorTypeLimit)
}
