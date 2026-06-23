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

func TestArrayFilterMap(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def helpers()
      values = [1, 2, 3, 4]
      {
        even_times_ten: values.filter_map do |n|
          if n % 2 == 0 then n * 10 end
        end,
        all_kept: values.filter_map do |n|
          n * 2
        end,
        none_kept: values.filter_map do |n|
          nil
        end,
        empty: [].filter_map do |n|
          n
        end,
        drops_false: [1, 2, 3].filter_map do |n|
          n == 2
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

	// Ruby's canonical filter_map example: keep even values, multiplied.
	compareArrays(t, got["even_times_ten"], []Value{NewInt(20), NewInt(40)})
	compareArrays(t, got["all_kept"], []Value{NewInt(2), NewInt(4), NewInt(6), NewInt(8)})
	// A block that always returns nil drops every element.
	compareArrays(t, got["none_kept"], []Value{})
	compareArrays(t, got["empty"], []Value{})
	// false predicates are dropped; only the truthy (true) result remains, and
	// filter_map keeps the block's return value, not the original element.
	compareArrays(t, got["drops_false"], []Value{NewBool(true)})
	// filter_map does not mutate the receiver.
	compareArrays(t, got["original"], []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)})
}

// TestArrayFilterMapDropsVibescriptFalsy documents that filter_map uses
// Vibescript's truthiness model (matching select/reject), so 0, "", and empty
// collections are dropped alongside nil and false. This diverges from Ruby,
// where only nil and false are falsy, but stays internally consistent with the
// other predicate-driven enumerable helpers.
func TestArrayFilterMapDropsVibescriptFalsy(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      [0, 1, "", "x", [], [9], 2].filter_map do |v|
        v
      end
    end
    `)
	result := callFunc(t, script, "run", nil)
	compareArrays(t, result, []Value{NewInt(1), NewString("x"), NewArray([]Value{NewInt(9)}), NewInt(2)})
}

// TestArrayEnumerableSparseResultsAreRightSized guards against the filtering
// helpers retaining a backing array sized to the whole receiver when the result
// is sparse. reject/take_while/grep all preallocate capacity equal to the
// receiver, so a result that drops most elements must be trimmed to avoid
// charging the caller's memory quota for storage it cannot reach.
func TestArrayEnumerableSparseResultsAreRightSized(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
	}{
		{
			name:   "reject",
			source: `def run(values); values.reject do |v| true end; end`,
		},
		{
			name:   "take_while",
			source: `def run(values); values.take_while do |v| false end; end`,
		},
		{
			name:   "filter_map",
			source: `def run(values); values.filter_map do |v| nil end; end`,
		},
		{
			name:   "grep",
			source: `def run(values); values.grep(-1); end`,
		},
		{
			name:   "grep_v",
			source: `def run(values); values.grep_v(0..100000); end`,
		},
	}

	const receiverSize = 1000
	// The receiver is large enough that retaining its backing array would be a
	// real cost, so the quota is raised well above the default to ensure the
	// test exercises trimming rather than the quota tripping on the input.
	cfg := Config{MemoryQuotaBytes: 8 * 1024 * 1024}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, cfg, tc.source)
			result := callFunc(t, script, "run", []Value{largeIntArray(receiverSize)})
			if result.Kind() != KindArray {
				t.Fatalf("expected array, got %v", result.Kind())
			}
			arr := result.Array()
			if len(arr) != 0 {
				t.Fatalf("expected empty result, got %d elements", len(arr))
			}
			if cap(arr) >= receiverSize {
				t.Fatalf("sparse result retained oversized backing array: cap=%d, want trimmed below %d", cap(arr), receiverSize)
			}
		})
	}
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
			name:   "filter_map without block",
			source: `def run(); [1, 2].filter_map; end`,
			want:   "array.filter_map requires a block",
		},
		{
			name:   "filter_map with arguments",
			source: `def run(); [1, 2].filter_map(1) do |n| n end; end`,
			want:   "array.filter_map does not take arguments",
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
			name:   "filter_map block error",
			source: `def run(); [1, 2].filter_map do |n| n.frobnicate end; end`,
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
			// Predicate stays true across the whole array so the block runs
			// for every element and actually trips the step quota; a predicate
			// that is immediately false would stop after one iteration.
			name:   "drop_while",
			source: `def run(values); values.drop_while do |v| v >= 0 end; end`,
		},
		{
			name:   "filter_map",
			source: `def run(values); values.filter_map do |v| v end; end`,
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
			name:   "filter_map",
			source: `def run(); [3, 1, 2].filter_map do |v| v end; end`,
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
