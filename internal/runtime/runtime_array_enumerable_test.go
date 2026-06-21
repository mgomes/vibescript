package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestArrayRejectTakeDropGrep(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def helpers()
      values = [1, 2, 3, 4]
      words = ["apple", "bee", "cat"]
      {
        reject: values.reject do |n|
          n % 2 == 0
        end,
        reject_empty: [].reject do |n|
          true
        end,
        take_while: values.take_while do |n|
          n < 3
        end,
        take_while_all: values.take_while do |n|
          n < 9
        end,
        take_while_none: values.take_while do |n|
          n < 0
        end,
        drop_while: values.drop_while do |n|
          n < 3
        end,
        drop_while_all: values.drop_while do |n|
          n < 9
        end,
        drop_while_none: values.drop_while do |n|
          n < 0
        end,
        grep_range: values.grep(2..3),
        grep_v_range: values.grep_v(2..3),
        grep_equal: words.grep("bee"),
        grep_v_equal: words.grep_v("bee"),
        grep_block: values.grep(2..3) do |n|
          n * 10
        end,
        grep_v_block: values.grep_v(2..3) do |n|
          n * 10
        end,
        original: values
      }
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()

	compareArrays(t, got["reject"], []Value{NewInt(1), NewInt(3)})
	compareArrays(t, got["reject_empty"], []Value{})
	compareArrays(t, got["take_while"], []Value{NewInt(1), NewInt(2)})
	compareArrays(t, got["take_while_all"], []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)})
	compareArrays(t, got["take_while_none"], []Value{})
	compareArrays(t, got["drop_while"], []Value{NewInt(3), NewInt(4)})
	compareArrays(t, got["drop_while_all"], []Value{})
	compareArrays(t, got["drop_while_none"], []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)})
	compareArrays(t, got["grep_range"], []Value{NewInt(2), NewInt(3)})
	compareArrays(t, got["grep_v_range"], []Value{NewInt(1), NewInt(4)})
	compareArrays(t, got["grep_equal"], []Value{NewString("bee")})
	compareArrays(t, got["grep_v_equal"], []Value{NewString("apple"), NewString("cat")})
	compareArrays(t, got["grep_block"], []Value{NewInt(20), NewInt(30)})
	compareArrays(t, got["grep_v_block"], []Value{NewInt(10), NewInt(40)})
	compareArrays(t, got["original"], []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)})
}

func TestArrayEnumerableHelperErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "reject without block",
			source: `def run(); [1, 2].reject; end`,
			want:   "array.reject requires a block",
		},
		{
			name:   "reject with arguments",
			source: `def run(); [1, 2].reject(1) do |n| n end; end`,
			want:   "array.reject does not take arguments",
		},
		{
			name:   "take_while without block",
			source: `def run(); [1, 2].take_while; end`,
			want:   "array.take_while requires a block",
		},
		{
			name:   "take_while with arguments",
			source: `def run(); [1, 2].take_while(1) do |n| n end; end`,
			want:   "array.take_while does not take arguments",
		},
		{
			name:   "drop_while without block",
			source: `def run(); [1, 2].drop_while; end`,
			want:   "array.drop_while requires a block",
		},
		{
			name:   "drop_while with arguments",
			source: `def run(); [1, 2].drop_while(1) do |n| n end; end`,
			want:   "array.drop_while does not take arguments",
		},
		{
			name:   "grep without pattern",
			source: `def run(); [1, 2].grep; end`,
			want:   "array.grep expects exactly one pattern argument",
		},
		{
			name:   "grep with extra arguments",
			source: `def run(); [1, 2].grep(1, 2); end`,
			want:   "array.grep expects exactly one pattern argument",
		},
		{
			name:   "grep_v without pattern",
			source: `def run(); [1, 2].grep_v; end`,
			want:   "array.grep_v expects exactly one pattern argument",
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

func TestArrayEnumerableHelperBlockErrorsPropagate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
	}{
		{
			name:   "reject block error",
			source: `def run(); [1, 2].reject do |n| n.frobnicate end; end`,
		},
		{
			name:   "take_while block error",
			source: `def run(); [1, 2].take_while do |n| n.frobnicate end; end`,
		},
		{
			name:   "drop_while block error",
			source: `def run(); [1, 2].drop_while do |n| n.frobnicate end; end`,
		},
		{
			name:   "grep transform block error",
			source: `def run(); [1, 2].grep(1..2) do |n| n.frobnicate end; end`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "frobnicate")
		})
	}
}

func TestArrayEnumerableHelpersParticipateInStepQuota(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
	}{
		{
			name:   "reject",
			source: `def run(values); values.reject do |v| v < 0 end; end`,
		},
		{
			name:   "take_while",
			source: `def run(values); values.take_while do |v| v >= 0 end; end`,
		},
		{
			name:   "drop_while",
			source: `def run(values); values.drop_while do |v| v < 0 end; end`,
		},
		{
			name:   "grep transform block",
			source: `def run(values); values.grep(0..100000) do |v| v end; end`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: 40}, tc.source)
			requireCallRuntimeErrorType(t, script, "run", []Value{largeIntArray(1000)}, CallOptions{}, runtimeErrorTypeLimit)
		})
	}
}

func TestArrayEnumerableHelpersHonorCancellation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
	}{
		{
			name:   "reject",
			source: `def run(); [3, 1, 2].reject do |v| v < 0 end; end`,
		},
		{
			name:   "take_while",
			source: `def run(); [3, 1, 2].take_while do |v| v >= 0 end; end`,
		},
		{
			name:   "drop_while",
			source: `def run(); [3, 1, 2].drop_while do |v| v < 0 end; end`,
		},
		{
			name:   "grep transform block",
			source: `def run(); [3, 1, 2].grep(0..9) do |v| v end; end`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := script.Call(ctx, "run", nil, CallOptions{})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s under canceled context = %v, want context.Canceled", tc.name, err)
			}
		})
	}
}
