package runtime

import (
	"context"
	"errors"
	"testing"
)

// TestArrayReduceSymbolShorthand exercises Ruby's Array#reduce operation form,
// where a symbol or string names the operation sent to the accumulator. The
// expected values mirror Ruby 2.6 behavior captured in issue #634.
func TestArrayReduceSymbolShorthand(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   Value
	}{
		{
			name:   "operator symbol concat",
			source: `def run(); ["a", "b", "c"].reduce(:concat); end`,
			want:   NewString("abc"),
		},
		{
			name:   "string operator plus",
			source: `def run(); [1, 2, 3].reduce("+"); end`,
			want:   NewInt(6),
		},
		{
			name:   "string operator times",
			source: `def run(); [2, 3, 4].reduce("*"); end`,
			want:   NewInt(24),
		},
		{
			name:   "string operator minus",
			source: `def run(); [10, 1, 2].reduce("-"); end`,
			want:   NewInt(7),
		},
		{
			name:   "initial plus operator",
			source: `def run(); [1, 2, 3].reduce(10, "+"); end`,
			want:   NewInt(16),
		},
		{
			name:   "initial concat symbol",
			source: `def run(); ["b", "c"].reduce("a", :concat); end`,
			want:   NewString("abc"),
		},
		{
			name:   "single element returns element",
			source: `def run(); [42].reduce("+"); end`,
			want:   NewInt(42),
		},
		{
			name:   "empty array no initial yields nil",
			source: `def run(); [].reduce("+"); end`,
			want:   NewNil(),
		},
		{
			name:   "empty array with initial yields initial",
			source: `def run(); [].reduce(99, "+"); end`,
			want:   NewInt(99),
		},
		{
			name:   "method name symbol dispatches to array member",
			source: `def run(); [[1], [2], [3]].reduce(:union); end`,
			want:   NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			got := callFunc(t, script, "run", nil)
			if diff := valueDiff(tc.want, got); diff != "" {
				t.Fatalf("reduce result mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestArrayReduceBlockForm confirms the explicit block form still works and
// that a block always takes precedence over a lone symbol argument: the symbol
// becomes the initial accumulator value, matching Ruby's
// `[1].reduce(:seed) { |a, b| "#{a}-#{b}" }`.
func TestArrayReduceBlockForm(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   Value
	}{
		{
			name:   "block without initial",
			source: `def run(); [1, 2, 3].reduce do |acc, n| acc + n end; end`,
			want:   NewInt(6),
		},
		{
			name:   "block with initial",
			source: `def run(); [1, 2, 3].reduce(100) do |acc, n| acc + n end; end`,
			want:   NewInt(106),
		},
		{
			name:   "block makes symbol argument the seed",
			source: `def run(); [1].reduce(:seed) do |acc, n| "#{acc}-#{n}" end; end`,
			want:   NewString("seed-1"),
		},
		{
			name:   "empty array with block and no initial yields nil",
			source: `def run(); [].reduce do |acc, n| acc + n end; end`,
			want:   NewNil(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			got := callFunc(t, script, "run", nil)
			if diff := valueDiff(tc.want, got); diff != "" {
				t.Fatalf("reduce result mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestArrayReduceOperationSendsViaRuntime verifies the operator form routes
// through the runtime so symbols constructed directly (which operator-symbol
// literals will produce once they lex) fold through the same path as string
// operation names.
func TestArrayReduceOperationSendsViaRuntime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		receiver  Value
		operation Value
		want      Value
	}{
		{
			name:      "plus operator symbol",
			receiver:  NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
			operation: NewSymbol("+"),
			want:      NewInt(6),
		},
		{
			name:      "times operator symbol",
			receiver:  NewArray([]Value{NewInt(2), NewInt(3), NewInt(4)}),
			operation: NewSymbol("*"),
			want:      NewInt(24),
		},
		{
			name:      "power operator symbol",
			receiver:  NewArray([]Value{NewInt(2), NewInt(3), NewInt(2)}),
			operation: NewSymbol("**"),
			want:      NewInt(64),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `def run(values, op); values.reduce(op); end`)
			got := callFunc(t, script, "run", []Value{tc.receiver, tc.operation})
			if diff := valueDiff(tc.want, got); diff != "" {
				t.Fatalf("reduce result mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestArrayReduceErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "no block no operation",
			source: `def run(); [1, 2, 3].reduce; end`,
			want:   "array.reduce requires a block or an operation",
		},
		{
			name:   "lone non-operation argument without block",
			source: `def run(); [1, 2, 3].reduce(10); end`,
			want:   "array.reduce operation must be a symbol or string",
		},
		{
			name:   "two-argument operation must be symbol or string",
			source: `def run(); [1, 2, 3].reduce(1, 2); end`,
			want:   "array.reduce operation must be a symbol or string",
		},
		{
			name:   "too many arguments",
			source: `def run(); [1, 2, 3].reduce(1, 2, 3); end`,
			want:   "array.reduce accepts at most an initial value and an operation",
		},
		{
			name:   "keyword arguments rejected",
			source: `def run(); [1, 2, 3].reduce(seed: 1); end`,
			want:   "array.reduce does not take keyword arguments",
		},
		{
			name:   "unknown operation surfaces dispatch error",
			source: `def run(); [1, 2, 3].reduce(:nope); end`,
			want:   `array.reduce cannot apply "nope"`,
		},
		{
			name:   "incompatible operands surface arithmetic error",
			source: `def run(); [1, "a"].reduce("-"); end`,
			want:   "subtraction",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

// TestArrayReduceOperationParticipatesInStepQuota confirms the operation form
// charges a step per element so a tight step quota trips on a large receiver.
func TestArrayReduceOperationParticipatesInStepQuota(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 40}, `def run(values); values.reduce("+"); end`)
	requireCallRuntimeErrorType(t, script, "run", []Value{largeIntArray(1000)}, CallOptions{}, runtimeErrorTypeLimit)
}

// TestArrayReduceOperationHonorsCancellation confirms the operation form polls
// context cancellation through the per-element step.
func TestArrayReduceOperationHonorsCancellation(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `def run(); [3, 1, 2].reduce("+"); end`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := script.Call(ctx, "run", nil, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("reduce under canceled context = %v, want context.Canceled", err)
	}
}
