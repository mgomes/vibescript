package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestSemicolonStatementSeparators(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run; x = 1; y = 2; x + y; end`)

	if got := callFunc(t, script, "run", nil); !got.Equal(NewInt(3)) {
		t.Fatalf("run() = %s, want 3", got)
	}
}

func TestIntTimes(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def collect(n)
      out = []
      n.times do |i|
        out = out + [i]
      end
      out
    end

    def times_returns_receiver(n)
      n.times do |i|
        i
      end
    end

    def times_negative(n)
      count = 0
      n.times do |i|
        count = count + 1
      end
      count
    end

    def times_without_block(n)
      n.times
    end
    `)

	collected := callFunc(t, script, "collect", []Value{NewInt(4)})
	compareArrays(t, collected, []Value{NewInt(0), NewInt(1), NewInt(2), NewInt(3)})

	ret := callFunc(t, script, "times_returns_receiver", []Value{NewInt(3)})
	if !ret.Equal(NewInt(3)) {
		t.Fatalf("times return value mismatch: got %v want %v", ret, NewInt(3))
	}

	neg := callFunc(t, script, "times_negative", []Value{NewInt(-2)})
	if !neg.Equal(NewInt(0)) {
		t.Fatalf("negative times loop mismatch: got %v want 0", neg)
	}

	_, err := script.Call(context.Background(), "times_without_block", []Value{NewInt(1)}, CallOptions{})
	if err == nil {
		t.Fatalf("expected error for times without block")
	}
}

func TestWhileLoops(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def countdown(n)
      out = []
      while n > 0
        out = out + [n]
        n = n - 1
      end
      out
    end

    def first_positive(n)
      while n > 0
        return n
      end
      0
    end

    def skip_false()
      while false
        1
      end
    end
    `)

	countdown := callFunc(t, script, "countdown", []Value{NewInt(3)})
	compareArrays(t, countdown, []Value{NewInt(3), NewInt(2), NewInt(1)})

	if got := callFunc(t, script, "first_positive", []Value{NewInt(4)}); !got.Equal(NewInt(4)) {
		t.Fatalf("first_positive mismatch for positive input: %v", got)
	}
	if got := callFunc(t, script, "first_positive", []Value{NewInt(0)}); !got.Equal(NewInt(0)) {
		t.Fatalf("first_positive mismatch for zero input: %v", got)
	}
	if got := callFunc(t, script, "skip_false", nil); !got.Equal(NewNil()) {
		t.Fatalf("skip_false expected nil, got %v", got)
	}

	spinScript := compileScriptWithConfig(t, Config{StepQuota: 40}, `
    def spin()
      while true
      end
    end
    `)
	requireCallRuntimeErrorType(t, spinScript, "spin", nil, CallOptions{}, runtimeErrorTypeLimit)
}

func TestUntilLoops(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def count_up(target)
      out = []
      n = 0
      until n >= target
        out = out + [n]
        n = n + 1
      end
      out
    end

    def first_non_negative(n)
      until n >= 0
        return n
      end
      n
    end

    def skip_until_true()
      until true
        1
      end
    end
    `)

	countUp := callFunc(t, script, "count_up", []Value{NewInt(4)})
	compareArrays(t, countUp, []Value{NewInt(0), NewInt(1), NewInt(2), NewInt(3)})

	if got := callFunc(t, script, "first_non_negative", []Value{NewInt(-3)}); !got.Equal(NewInt(-3)) {
		t.Fatalf("first_non_negative mismatch for negative input: %v", got)
	}
	if got := callFunc(t, script, "first_non_negative", []Value{NewInt(2)}); !got.Equal(NewInt(2)) {
		t.Fatalf("first_non_negative mismatch for non-negative input: %v", got)
	}
	if got := callFunc(t, script, "skip_until_true", nil); !got.Equal(NewNil()) {
		t.Fatalf("skip_until_true expected nil, got %v", got)
	}

	spinScript := compileScriptWithConfig(t, Config{StepQuota: 40}, `
    def spin_until()
      until false
      end
    end
	`)
	requireCallRuntimeErrorType(t, spinScript, "spin_until", nil, CallOptions{}, runtimeErrorTypeLimit)
}

func TestForRangeLoops(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def inclusive_range_values
      out = []
      for n in 1..5
        out = out + [n]
      end
      out
    end

    def exclusive_range_values
      out = []
      for n in 1...5
        out = out + [n]
      end
      out
    end

    def descending_inclusive_range_values
      out = []
      for n in 5..1
        out = out + [n]
      end
      out
    end

    def descending_exclusive_range_values
      out = []
      for n in 5...1
        out = out + [n]
      end
      out
    end

    def empty_exclusive_range_values
      out = []
      for n in 3...3
        out = out + [n]
      end
      out
    end
    `)

	tests := []struct {
		name string
		fn   string
		want []Value
	}{
		{
			name: "inclusive",
			fn:   "inclusive_range_values",
			want: []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4), NewInt(5)},
		},
		{
			name: "exclusive",
			fn:   "exclusive_range_values",
			want: []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)},
		},
		{
			name: "descending_inclusive",
			fn:   "descending_inclusive_range_values",
			want: []Value{NewInt(5), NewInt(4), NewInt(3), NewInt(2), NewInt(1)},
		},
		{
			name: "descending_exclusive",
			fn:   "descending_exclusive_range_values",
			want: []Value{NewInt(5), NewInt(4), NewInt(3), NewInt(2)},
		},
		{
			name: "same_endpoint_exclusive",
			fn:   "empty_exclusive_range_values",
			want: nil,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, nil)
			compareArrays(t, got, tc.want)
		})
	}
}

func TestForHashLoops(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def pairs()
      out = []
      for pair in { b: 2, a: 1, c: 3 }
        out = out + [pair]
      end
      out
    end

    def keys()
      out = []
      for pair in { b: 2, a: 1 }
        out = out + [pair[0]]
      end
      out
    end

    def values()
      out = []
      for pair in { b: 2, a: 1 }
        out = out + [pair[1]]
      end
      out
    end

    def empty_hash()
      count = 0
      for pair in {}
        count = count + 1
      end
      count
    end

    def break_at_b()
      out = []
      for pair in { a: 1, b: 2, c: 3 }
        if pair[0] == :b
          break
        end
        out = out + [pair]
      end
      out
    end

    def next_skips_b()
      out = []
      for pair in { a: 1, b: 2, c: 3 }
        if pair[0] == :b
          next
        end
        out = out + [pair]
      end
      out
    end

    def last_pair()
      for pair in { a: 1, b: 2 }
        pair
      end
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{
			name: "yields_sorted_key_value_pairs",
			fn:   "pairs",
			want: NewArray([]Value{
				NewArray([]Value{NewSymbol("a"), NewInt(1)}),
				NewArray([]Value{NewSymbol("b"), NewInt(2)}),
				NewArray([]Value{NewSymbol("c"), NewInt(3)}),
			}),
		},
		{
			name: "first_element_is_symbol_key",
			fn:   "keys",
			want: NewArray([]Value{NewSymbol("a"), NewSymbol("b")}),
		},
		{
			name: "second_element_is_value",
			fn:   "values",
			want: NewArray([]Value{NewInt(1), NewInt(2)}),
		},
		{
			name: "empty_hash_runs_zero_iterations",
			fn:   "empty_hash",
			want: NewInt(0),
		},
		{
			name: "break_stops_iteration",
			fn:   "break_at_b",
			want: NewArray([]Value{NewArray([]Value{NewSymbol("a"), NewInt(1)})}),
		},
		{
			name: "next_skips_entry",
			fn:   "next_skips_b",
			want: NewArray([]Value{
				NewArray([]Value{NewSymbol("a"), NewInt(1)}),
				NewArray([]Value{NewSymbol("c"), NewInt(3)}),
			}),
		},
		{
			name: "loop_returns_last_body_value",
			fn:   "last_pair",
			want: NewArray([]Value{NewSymbol("b"), NewInt(2)}),
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, nil)
			if diff := valueDiff(tc.want, got); diff != "" {
				t.Fatalf("%s mismatch (-want +got):\n%s", tc.fn, diff)
			}
		})
	}
}

func TestForHashLoopConsumesStepQuota(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 2}, `def run()
  for pair in { a: 1, b: 2, c: 3, d: 4, e: 5 }
  end
end`)
	requireCallRuntimeErrorType(t, script, "run", nil, CallOptions{}, runtimeErrorTypeLimit)
}

func TestForLoopConsumesStepQuota(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{
			name: "range_empty_body",
			body: `def run()
  for i in 1..1000
  end
end`,
		},
		{
			name: "descending_range_empty_body",
			body: `def run()
  for i in 1000..1
  end
end`,
		},
		{
			name: "array_empty_body",
			body: `def run()
  for i in [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20]
  end
end`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: 10}, tc.body)
			requireCallRuntimeErrorType(t, script, "run", nil, CallOptions{}, runtimeErrorTypeLimit)
		})
	}
}

func TestForLoopChecksCancellationAfterHostCancel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{
			name: "range_empty_body",
			body: `def run()
  cancel_now()
  for i in 1..1000
  end
end`,
		},
		{
			name: "descending_range_empty_body",
			body: `def run()
  cancel_now()
  for i in 1000..1
  end
end`,
		},
		{
			name: "array_empty_body",
			body: `def run()
  cancel_now()
  for i in [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20]
  end
end`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var cancel context.CancelFunc
			engine := MustNewEngine(Config{StepQuota: 10_000_000})
			engine.builtins["cancel_now"] = NewBuiltin("cancel_now", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
				cancel()
				return NewNil(), nil
			})
			script := compileScriptWithEngine(t, engine, tc.body)

			ctx, cancelFunc := context.WithCancel(context.Background())
			cancel = cancelFunc
			_, err := script.Call(ctx, "run", nil, CallOptions{})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Script.Call(empty for body after cancel) error = %v, want context.Canceled", err)
			}
		})
	}
}

func TestScriptCallChecksCanceledContextBeforeFunctionSuggestion(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  1
end
`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := script.Call(ctx, "missing_run", nil, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Script.Call missing function under canceled context = %v, want context.Canceled", err)
	}

	lazyGlobals := newTaskLazyGlobals(map[string]Value{"payload": NewString("unused")}, false, false)
	_, err = script.callWithLazyTaskGlobals(ctx, "missing_run", nil, CallOptions{}, lazyGlobals)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("callWithLazyTaskGlobals missing function under canceled context = %v, want context.Canceled", err)
	}
}

func TestUnlessConditionals(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def block_form(flag)
      unless flag
        "open"
      else
        "blocked"
      end
    end

    def block_without_else(flag)
      unless flag
        "open"
      end
    end

    def modifier_form(flag)
      out = []
      out = out + ["open"] unless flag
      out
    end

    def modifier_expression(flag)
      "open" unless flag
    end
    `)

	cases := []struct {
		name string
		fn   string
		arg  Value
		want Value
	}{
		{name: "block_false_runs_body", fn: "block_form", arg: NewBool(false), want: NewString("open")},
		{name: "block_true_runs_else", fn: "block_form", arg: NewBool(true), want: NewString("blocked")},
		{name: "without_else_false_runs_body", fn: "block_without_else", arg: NewBool(false), want: NewString("open")},
		{name: "without_else_true_returns_nil", fn: "block_without_else", arg: NewBool(true), want: NewNil()},
		{name: "modifier_false_runs_statement", fn: "modifier_form", arg: NewBool(false), want: NewArray([]Value{NewString("open")})},
		{name: "modifier_true_skips_statement", fn: "modifier_form", arg: NewBool(true), want: NewArray(nil)},
		{name: "modifier_expression_false_returns_value", fn: "modifier_expression", arg: NewBool(false), want: NewString("open")},
		{name: "modifier_expression_true_returns_nil", fn: "modifier_expression", arg: NewBool(true), want: NewNil()},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, []Value{tc.arg})
			if !got.Equal(tc.want) {
				t.Fatalf("%s(%v) = %v, want %v", tc.fn, tc.arg, got, tc.want)
			}
		})
	}
}

func TestThenControlFlowSeparators(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def if_then(flag)
      if flag then "yes" else "no" end
    end

    def if_elsif_then(value)
      if value == 1 then "one" elsif value == 2 then "two" else "other" end
    end

    def unless_then(flag)
      unless flag then "open" else "closed" end
    end
    `)

	cases := []struct {
		name string
		fn   string
		args []Value
		want Value
	}{
		{name: "if_true", fn: "if_then", args: []Value{NewBool(true)}, want: NewString("yes")},
		{name: "if_false", fn: "if_then", args: []Value{NewBool(false)}, want: NewString("no")},
		{name: "elsif_first_branch", fn: "if_elsif_then", args: []Value{NewInt(1)}, want: NewString("one")},
		{name: "elsif_second_branch", fn: "if_elsif_then", args: []Value{NewInt(2)}, want: NewString("two")},
		{name: "elsif_else_branch", fn: "if_elsif_then", args: []Value{NewInt(3)}, want: NewString("other")},
		{name: "unless_false", fn: "unless_then", args: []Value{NewBool(false)}, want: NewString("open")},
		{name: "unless_true", fn: "unless_then", args: []Value{NewBool(true)}, want: NewString("closed")},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, tc.args)
			if !got.Equal(tc.want) {
				t.Fatalf("%s(%v) = %v, want %v", tc.fn, tc.args, got, tc.want)
			}
		})
	}
}

func TestLineTerminatedHeadersAndStatements(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def if_empty_array
      if true
        []
      else
        [2]
      end
    end

    def if_array
      if true
        [1]
      else
        [2]
      end
    end

    def if_hash
      if false
        [1]
      else
        { a: 1 }
      end
    end

    def if_chain(values)
      if values
        .reverse
        .include?(1)
        "hit"
      else
        "miss"
      end
    end

    def while_body
      seen = []
      i = 0
      while i < 1
        [i]
        seen = seen + [i]
        i = i + 1
      end
      seen
    end

    def until_body
      seen = []
      i = 0
      until i == 1
        { i: i }
        seen = seen + [i]
        i = i + 1
      end
      seen
    end

    def for_body
      seen = []
      for item in [1, 2]
        [item]
        seen = seen + [item]
      end
      seen
    end

    def return_value
      return true
      [1]
    end

    def bare_return
      return
      [1]
    end

    def raise_message
      raise "boom"
      [1]
    end
    `)

	if got := callFunc(t, script, "if_empty_array", nil); got.Kind() != KindArray || len(got.Array()) != 0 {
		t.Fatalf("if_empty_array mismatch: %v", got)
	}
	compareArrays(t, callFunc(t, script, "if_array", nil), []Value{NewInt(1)})

	hashResult := callFunc(t, script, "if_hash", nil)
	if hashResult.Kind() != KindHash {
		t.Fatalf("if_hash expected hash, got %v", hashResult.Kind())
	}
	compareHash(t, hashResult.Hash(), map[string]Value{"a": NewInt(1)})
	if got := callFunc(t, script, "if_chain", []Value{NewArray([]Value{NewInt(2), NewInt(1)})}); !got.Equal(NewString("hit")) {
		t.Fatalf("if_chain hit mismatch: %v", got)
	}
	if got := callFunc(t, script, "if_chain", []Value{NewArray([]Value{NewInt(2)})}); !got.Equal(NewString("miss")) {
		t.Fatalf("if_chain miss mismatch: %v", got)
	}

	compareArrays(t, callFunc(t, script, "while_body", nil), []Value{NewInt(0)})
	compareArrays(t, callFunc(t, script, "until_body", nil), []Value{NewInt(0)})
	compareArrays(t, callFunc(t, script, "for_body", nil), []Value{NewInt(1), NewInt(2)})

	if got := callFunc(t, script, "return_value", nil); !got.Equal(NewBool(true)) {
		t.Fatalf("return_value mismatch: %v", got)
	}
	if got := callFunc(t, script, "bare_return", nil); got.Kind() != KindNil {
		t.Fatalf("bare_return expected nil, got %v", got)
	}
	requireCallErrorContains(t, script, "raise_message", nil, CallOptions{}, "boom")
}

func TestCaseWhenExpressions(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def label(score)
      case score
      when 100
        "perfect"
      when 90, 95
        "great"
      else
        "ok"
      end
    end

    def compact_label(score)
      case score
      when 100 then "perfect"
      when 90, 95 then "great"
      else "ok"
      end
    end

    def classify(value)
      case value
      when nil
        "missing"
      when true
        "yes"
      else
        "other"
      end
    end

    def assign_case(v)
      result = case v
      when 1
        10
      else
        20
      end
      result
    end

    def unmatched(v)
      case v
      when 1
        "one"
      end
    end

    def range_label(value)
      case value
      when 1..5
        "low"
      when 10..6
        "high"
      else
        "other"
      end
    end

    def float_range_label(value)
      case value
      when 1..5
        "inside"
      else
        "outside"
      end
    end

    def range_target(value)
      case value
      when 1..5
        "same range"
      else
        "other"
      end
    end

    def exclusive_range_label(value)
      case value
      when 1...5
        "low"
      when 10...6
        "high"
      else
        "other"
      end
    end

    def exclusive_range_target(value)
      case value
      when 1...5
        "same exclusive range"
      else
        "other"
      end
    end

    def large_integer_range(value)
      case value
      when 9007199254740992..9007199254740992
        "exact"
      else
        "miss"
      end
    end

    def large_float_range(value)
      case value
      when 9007199254740993..9007199254740993
        "exact"
      else
        "miss"
      end
    end

    def predicate_label(value)
      case
      when value == 1
        "one"
      when value == 2
        "two"
      else
        "other"
      end
    end

    def predicate_multi(value)
      case
      when value < 0, value == 0
        "non-positive"
      else
        "positive"
      end
    end

    def predicate_unmatched(value)
      case
      when value < 0
        "negative"
      end
    end
    `)

	cases := []struct {
		name string
		fn   string
		arg  Value
		want Value
	}{
		{name: "label_100_perfect", fn: "label", arg: NewInt(100), want: NewString("perfect")},
		{name: "label_95_great", fn: "label", arg: NewInt(95), want: NewString("great")},
		{name: "label_70_default", fn: "label", arg: NewInt(70), want: NewString("ok")},
		{name: "compact_label_100_perfect", fn: "compact_label", arg: NewInt(100), want: NewString("perfect")},
		{name: "compact_label_95_great", fn: "compact_label", arg: NewInt(95), want: NewString("great")},
		{name: "compact_label_70_default", fn: "compact_label", arg: NewInt(70), want: NewString("ok")},
		{name: "classify_nil_missing", fn: "classify", arg: NewNil(), want: NewString("missing")},
		{name: "classify_true_yes", fn: "classify", arg: NewBool(true), want: NewString("yes")},
		{name: "classify_one_other", fn: "classify", arg: NewInt(1), want: NewString("other")},
		{name: "assign_case_match", fn: "assign_case", arg: NewInt(1), want: NewInt(10)},
		{name: "assign_case_default", fn: "assign_case", arg: NewInt(2), want: NewInt(20)},
		{name: "unmatched_returns_nil", fn: "unmatched", arg: NewInt(7), want: NewNil()},
		{name: "range_matches_integer", fn: "range_label", arg: NewInt(3), want: NewString("low")},
		{name: "descending_range_matches_integer", fn: "range_label", arg: NewInt(8), want: NewString("high")},
		{name: "range_miss_uses_else", fn: "range_label", arg: NewInt(11), want: NewString("other")},
		{name: "range_matches_float", fn: "float_range_label", arg: NewFloat(3.5), want: NewString("inside")},
		{name: "range_target_keeps_equality", fn: "range_target", arg: NewRange(Range{Start: 1, End: 5}), want: NewString("same range")},
		{name: "exclusive_range_matches_integer", fn: "exclusive_range_label", arg: NewInt(4), want: NewString("low")},
		{name: "exclusive_range_excludes_ascending_end", fn: "exclusive_range_label", arg: NewInt(5), want: NewString("other")},
		{name: "exclusive_descending_range_matches_integer", fn: "exclusive_range_label", arg: NewInt(7), want: NewString("high")},
		{name: "exclusive_range_excludes_descending_end", fn: "exclusive_range_label", arg: NewInt(6), want: NewString("other")},
		{name: "exclusive_range_matches_fractional_float", fn: "exclusive_range_label", arg: NewFloat(4.5), want: NewString("low")},
		{name: "exclusive_range_excludes_float_at_end", fn: "exclusive_range_label", arg: NewFloat(5), want: NewString("other")},
		{name: "exclusive_descending_range_matches_fractional_float", fn: "exclusive_range_label", arg: NewFloat(6.5), want: NewString("high")},
		{name: "exclusive_descending_range_excludes_float_at_end", fn: "exclusive_range_label", arg: NewFloat(6), want: NewString("other")},
		{name: "exclusive_range_target_keeps_equality", fn: "exclusive_range_target", arg: NewRange(Range{Start: 1, End: 5, Exclusive: true}), want: NewString("same exclusive range")},
		{name: "exclusive_range_target_rejects_inclusive_range", fn: "exclusive_range_target", arg: NewRange(Range{Start: 1, End: 5}), want: NewString("other")},
		{name: "large_integer_range_matches_exact", fn: "large_integer_range", arg: NewInt(9007199254740992), want: NewString("exact")},
		{name: "large_integer_range_does_not_round", fn: "large_integer_range", arg: NewInt(9007199254740993), want: NewString("miss")},
		{name: "large_float_range_does_not_round_bounds", fn: "large_float_range", arg: NewFloat(9007199254740992), want: NewString("miss")},
		{name: "predicate_case_first_match", fn: "predicate_label", arg: NewInt(1), want: NewString("one")},
		{name: "predicate_case_second_match", fn: "predicate_label", arg: NewInt(2), want: NewString("two")},
		{name: "predicate_case_else", fn: "predicate_label", arg: NewInt(3), want: NewString("other")},
		{name: "predicate_case_multi_first_match", fn: "predicate_multi", arg: NewInt(-1), want: NewString("non-positive")},
		{name: "predicate_case_multi_second_match", fn: "predicate_multi", arg: NewInt(0), want: NewString("non-positive")},
		{name: "predicate_case_multi_else", fn: "predicate_multi", arg: NewInt(1), want: NewString("positive")},
		{name: "predicate_case_unmatched_returns_nil", fn: "predicate_unmatched", arg: NewInt(1), want: NewNil()},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, []Value{tc.arg})
			if !got.Equal(tc.want) {
				t.Fatalf("%s(%v) = %v, want %v", tc.fn, tc.arg, got, tc.want)
			}
		})
	}
}

func TestBeginRescueEnsure(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def safe_div(a, b)
      begin
        a / b
      rescue
        "fallback"
      end
    end

    def ensure_trace(fail)
      trace = []
      begin
        trace = trace + ["body"]
        if fail
          1 / 0
        end
        trace = trace + ["body_done"]
      rescue
        trace = trace + ["rescue"]
      ensure
        trace = trace + ["ensure"]
      end
      trace
    end

    def rescue_assertion()
      begin
        assert false, "boom"
      rescue
        "caught"
      end
    end

    def ensure_return_override()
      begin
        10
      ensure
        return 42
      end
    end

    def ensure_without_rescue()
      begin
        1 / 0
      ensure
        123
      end
    end
    `)

	if got := callFunc(t, script, "safe_div", []Value{NewInt(10), NewInt(2)}); !got.Equal(NewInt(5)) {
		t.Fatalf("safe_div success mismatch: %v", got)
	}
	if got := callFunc(t, script, "safe_div", []Value{NewInt(10), NewInt(0)}); !got.Equal(NewString("fallback")) {
		t.Fatalf("safe_div rescue mismatch: %v", got)
	}

	traceOK := callFunc(t, script, "ensure_trace", []Value{NewBool(false)})
	compareArrays(t, traceOK, []Value{NewString("body"), NewString("body_done"), NewString("ensure")})

	traceFail := callFunc(t, script, "ensure_trace", []Value{NewBool(true)})
	compareArrays(t, traceFail, []Value{NewString("body"), NewString("rescue"), NewString("ensure")})

	if got := callFunc(t, script, "rescue_assertion", nil); !got.Equal(NewString("caught")) {
		t.Fatalf("rescue_assertion mismatch: %v", got)
	}

	if got := callFunc(t, script, "ensure_return_override", nil); !got.Equal(NewInt(42)) {
		t.Fatalf("ensure_return_override mismatch: %v", got)
	}

	requireCallErrorContains(t, script, "ensure_without_rescue", nil, CallOptions{}, "division by zero")
}

func TestFunctionLevelRescueEnsure(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def safe_div(a, b)
      a / b
    rescue RuntimeError => err
      [err.type, err.message]
    end

    def success_else()
      "body"
    rescue
      "rescue"
    else
      "else"
    ensure
      "ensure"
    end

    def ensure_return_override()
      10
    ensure
      return 42
    end

    def ensure_without_rescue()
      1 / 0
    ensure
      123
    end
    `)

	if got := callFunc(t, script, "safe_div", []Value{NewInt(10), NewInt(2)}); !got.Equal(NewInt(5)) {
		t.Fatalf("safe_div success mismatch: %v", got)
	}
	compareArrays(t, callFunc(t, script, "safe_div", []Value{NewInt(10), NewInt(0)}), []Value{
		NewString(runtimeErrorTypeZeroDiv),
		NewString("division by zero"),
	})
	if got := callFunc(t, script, "success_else", nil); !got.Equal(NewString("else")) {
		t.Fatalf("success_else mismatch: %v", got)
	}
	if got := callFunc(t, script, "ensure_return_override", nil); !got.Equal(NewInt(42)) {
		t.Fatalf("ensure_return_override mismatch: %v", got)
	}
	requireCallErrorContains(t, script, "ensure_without_rescue", nil, CallOptions{}, "division by zero")
}

func TestBeginRescueTypedMatching(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def typed_assertion()
      begin
        assert false, "boom"
      rescue(AssertionError)
        "assertion"
      end
    end

    def typed_runtime()
      begin
        assert false, "boom"
      rescue(RuntimeError)
        "runtime"
      end
    end

    def typed_union()
      begin
        assert false, "boom"
      rescue(AssertionError | RuntimeError)
        "union"
      end
    end

    def typed_limit()
      begin
        recurse()
      rescue(LimitError)
        "limit"
      end
    end

    def recurse()
      recurse()
    end

    def rescue_mismatch()
      begin
        1 / 0
      rescue(AssertionError)
        "nope"
      end
    end

    def assertion_passthrough()
      assert false, "raw"
    end
    `)

	if got := callFunc(t, script, "typed_assertion", nil); !got.Equal(NewString("assertion")) {
		t.Fatalf("typed_assertion mismatch: %v", got)
	}
	if got := callFunc(t, script, "typed_runtime", nil); !got.Equal(NewString("runtime")) {
		t.Fatalf("typed_runtime mismatch: %v", got)
	}
	if got := callFunc(t, script, "typed_union", nil); !got.Equal(NewString("union")) {
		t.Fatalf("typed_union mismatch: %v", got)
	}
	if got := callFunc(t, script, "typed_limit", nil); !got.Equal(NewString("limit")) {
		t.Fatalf("typed_limit mismatch: %v", got)
	}

	err := callScriptErr(t, context.Background(), script, "rescue_mismatch", nil, CallOptions{})
	requireErrorContains(t, err, "division by zero")
	var divideErr *RuntimeError
	if !errors.As(err, &divideErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if divideErr.Type != runtimeErrorTypeZeroDiv {
		t.Fatalf("expected runtime error type %s, got %s", runtimeErrorTypeZeroDiv, divideErr.Type)
	}

	err = callScriptErr(t, context.Background(), script, "assertion_passthrough", nil, CallOptions{})
	requireErrorContains(t, err, "raw")
	var assertionErr *RuntimeError
	if !errors.As(err, &assertionErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if assertionErr.Type != runtimeErrorTypeAssertion {
		t.Fatalf("expected runtime error type %s, got %s", runtimeErrorTypeAssertion, assertionErr.Type)
	}
}

func TestBeginRescueRubyStyleBinding(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def ruby_typed()
      begin
        raise("boom")
      rescue RuntimeError
        "rescued"
      end
    end

    def ruby_binding()
      begin
        raise("boom")
      rescue => err
        [err.type, err.message]
      end
    end

    def ruby_typed_binding()
      begin
        assert false, "bad"
      rescue AssertionError | RuntimeError => err
        [err.type, err.message]
      end
    end

    def ruby_parenthesized_binding()
      begin
        raise("boom")
      rescue(RuntimeError) => err
        err.type
      end
    end

    def ruby_binding_scope()
      begin
        raise("boom")
      rescue => err
        "rescued"
      end
      err
    end
    `)

	if got := callFunc(t, script, "ruby_typed", nil); !got.Equal(NewString("rescued")) {
		t.Fatalf("ruby_typed mismatch: %v", got)
	}
	compareArrays(t, callFunc(t, script, "ruby_binding", nil), []Value{
		NewString(runtimeErrorTypeBase),
		NewString("boom"),
	})
	compareArrays(t, callFunc(t, script, "ruby_typed_binding", nil), []Value{
		NewString(runtimeErrorTypeAssertion),
		NewString("bad"),
	})
	if got := callFunc(t, script, "ruby_parenthesized_binding", nil); !got.Equal(NewString(runtimeErrorTypeBase)) {
		t.Fatalf("ruby_parenthesized_binding mismatch: %v", got)
	}
	requireCallErrorContains(t, script, "ruby_binding_scope", nil, CallOptions{}, "undefined variable err")
}

func TestRubyStyleExceptionClassesAndBindingMembers(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def standard_error()
      begin
        raise("boom")
      rescue StandardError => err
        [err.class, err.type, err.message, err.to_s]
      end
    end

    def zero_division()
      begin
        1 / 0
      rescue ZeroDivisionError => err
        [err.class, err.type, err.message]
      end
    end

    def needs_block()
      begin
        yield
      rescue LocalJumpError => err
        [err.class, err.type, err.message]
      end
    end

    def required_arg(value)
      value
    end

    def wrong_arity()
      begin
        required_arg()
      rescue ArgumentError => err
        [err.class, err.type, err.message]
      end
    end

    def backtrace_shape()
      begin
        raise("boom")
      rescue => err
        [err.backtrace.length > 0, err.backtrace[0].include?("backtrace_shape")]
      end
    end
    `)

	compareArrays(t, callFunc(t, script, "standard_error", nil), []Value{
		NewString(runtimeErrorTypeBase),
		NewString(runtimeErrorTypeBase),
		NewString("boom"),
		NewString("boom"),
	})
	compareArrays(t, callFunc(t, script, "zero_division", nil), []Value{
		NewString(runtimeErrorTypeZeroDiv),
		NewString(runtimeErrorTypeZeroDiv),
		NewString("division by zero"),
	})
	compareArrays(t, callFunc(t, script, "needs_block", nil), []Value{
		NewString(runtimeErrorTypeLocalJump),
		NewString(runtimeErrorTypeLocalJump),
		NewString("no block given"),
	})
	compareArrays(t, callFunc(t, script, "wrong_arity", nil), []Value{
		NewString(runtimeErrorTypeArgument),
		NewString(runtimeErrorTypeArgument),
		NewString("missing argument value"),
	})
	compareArrays(t, callFunc(t, script, "backtrace_shape", nil), []Value{
		NewBool(true),
		NewBool(true),
	})
}

func TestBeginRescueDoesNotCatchLoopControlSignals(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def break_in_loop()
      out = []
      for n in [1, 2, 3]
        begin
          if n == 2
            break
          end
          out = out + [n]
        rescue
          out = out + ["rescued"]
        end
      end
      out
    end

    def next_in_loop()
      out = []
      for n in [1, 2, 3]
        begin
          if n == 2
            next
          end
          out = out + [n]
        rescue
          out = out + ["rescued"]
        end
      end
      out
    end
    `)

	breakOut := callFunc(t, script, "break_in_loop", nil)
	compareArrays(t, breakOut, []Value{NewInt(1)})

	nextOut := callFunc(t, script, "next_in_loop", nil)
	compareArrays(t, nextOut, []Value{NewInt(1), NewInt(3)})
}

func TestBeginRescueDoesNotRecoverStepQuota(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 60}, `
    def run()
      begin
        while true
        end
      rescue
        "rescued"
      end
    end
    `)

	requireCallRuntimeErrorType(t, script, "run", nil, CallOptions{}, runtimeErrorTypeLimit)
}

func TestExecutionStepChecksCanceledContextOnFirstStep(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exec := &Execution{
		ctx:   ctx,
		quota: 10_000,
	}

	err := exec.step()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execution.step() at first step = %v, want context.Canceled", err)
	}
}

func TestScriptCallChecksCanceledContextBeforeShortFunction(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `def run()
  1
end`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := script.Call(ctx, "run", nil, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Script.Call(canceled context) error = %v, want context.Canceled", err)
	}
}

func TestExecutionStepPollsCanceledContextOnSlowPath(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exec := &Execution{
		ctx:   ctx,
		quota: 10_000,
	}

	if err := exec.step(); err != nil {
		t.Fatalf("Execution.step() at first step = %v, want nil before cancellation", err)
	}
	cancel()
	for step := 2; step < stepSlowPathMask+1; step++ {
		if err := exec.step(); err != nil {
			t.Fatalf("Execution.step() at step %d = %v, want nil before slow path", step, err)
		}
	}

	err := exec.step()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execution.step() at step %d = %v, want context.Canceled", stepSlowPathMask+1, err)
	}
}

func TestBeginRescueDoesNotRecoverRecursionLimit(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{RecursionLimit: 4, StepQuota: 10_000}, `
    def run()
      begin
        recurse()
      rescue
        "rescued"
      end
    end

    def recurse()
      recurse()
    end
    `)

	requireCallRuntimeErrorType(t, script, "run", nil, CallOptions{}, runtimeErrorTypeLimit)
}

func TestStepQuotaLimitErrorPreservesFunctionFrames(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 5}, `
    def outer()
      inner()
    end

    def inner()
      a = 1
      b = 2
      c = 3
    end
    `)

	err := callScriptErr(t, context.Background(), script, "outer", nil, CallOptions{})
	requireRuntimeErrorType(t, err, runtimeErrorTypeLimit)

	var rtErr *RuntimeError
	if !errors.As(err, &rtErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if len(rtErr.Frames) < 2 {
		t.Fatalf("expected at least 2 frames, got %d", len(rtErr.Frames))
	}
	if rtErr.Frames[0].Function != "inner" {
		t.Fatalf("expected inner frame first, got %s", rtErr.Frames[0].Function)
	}
	if rtErr.Frames[0].Pos.Line <= 6 {
		t.Fatalf("expected failing statement position inside inner body, got %+v", rtErr.Frames[0].Pos)
	}
	foundOuter := false
	for _, frame := range rtErr.Frames {
		if frame.Function == "outer" {
			foundOuter = true
		}
	}
	if !foundOuter {
		t.Fatalf("expected outer call frame, got %+v", rtErr.Frames)
	}
}

func TestBeginRescueTypedUnknownTypeFailsCompile(t *testing.T) {
	t.Parallel()
	requireCompileErrorContainsDefault(t, `
    def bad()
      begin
        1 / 0
      rescue(NotARealError)
        "fallback"
      end
    end
    `, "unknown rescue error type NotARealError")
}

func TestBeginRescueReraisePreservesStack(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def inner()
      assert false, "boom"
    end

    def middle()
      begin
        inner()
      rescue(AssertionError)
        raise
      end
    end

    def outer()
      middle()
    end

    def catches_reraise()
      begin
        middle()
      rescue(AssertionError)
        "caught"
      end
    end

    def raise_outside()
      raise
    end

    def raise_new_message()
      raise "custom boom"
    end

    def raise_nil()
      raise nil
    end

    def raise_int()
      raise 1
    end

    def raise_hash()
      raise({ message: "boom" })
    end
    `)

	if got := callFunc(t, script, "catches_reraise", nil); !got.Equal(NewString("caught")) {
		t.Fatalf("catches_reraise mismatch: %v", got)
	}

	err := callScriptErr(t, context.Background(), script, "outer", nil, CallOptions{})
	requireErrorContains(t, err, "boom")
	var rtErr *RuntimeError
	if !errors.As(err, &rtErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if rtErr.Type != runtimeErrorTypeAssertion {
		t.Fatalf("expected assertion error type %s, got %s", runtimeErrorTypeAssertion, rtErr.Type)
	}
	if len(rtErr.Frames) < 4 {
		t.Fatalf("expected at least 4 frames, got %d", len(rtErr.Frames))
	}
	if rtErr.Frames[0].Function != "inner" {
		t.Fatalf("expected inner frame first, got %s", rtErr.Frames[0].Function)
	}
	if rtErr.Frames[1].Function != "inner" {
		t.Fatalf("expected inner call site second, got %s", rtErr.Frames[1].Function)
	}
	if rtErr.Frames[2].Function != "middle" {
		t.Fatalf("expected middle frame third, got %s", rtErr.Frames[2].Function)
	}
	if rtErr.Frames[3].Function != "outer" {
		t.Fatalf("expected outer frame fourth, got %s", rtErr.Frames[3].Function)
	}

	err = callScriptErr(t, context.Background(), script, "raise_outside", nil, CallOptions{})
	var outsideErr *RuntimeError
	if !errors.As(err, &outsideErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if outsideErr.Type != runtimeErrorTypeBase {
		t.Fatalf("expected runtime error type %s, got %s", runtimeErrorTypeBase, outsideErr.Type)
	}
	if outsideErr.Message != "" {
		t.Fatalf("bare raise outside rescue message = %q, want empty", outsideErr.Message)
	}

	err = callScriptErr(t, context.Background(), script, "raise_new_message", nil, CallOptions{})
	requireErrorContains(t, err, "custom boom")
	var raisedErr *RuntimeError
	if !errors.As(err, &raisedErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if raisedErr.Type != runtimeErrorTypeBase {
		t.Fatalf("expected runtime error type %s, got %s", runtimeErrorTypeBase, raisedErr.Type)
	}

	typeErrorCases := []struct {
		name    string
		message string
	}{
		{name: "raise_nil", message: "exception object expected"},
		{name: "raise_int", message: "exception class/object expected"},
		{name: "raise_hash", message: "exception class/object expected"},
	}
	for _, tc := range typeErrorCases {
		err := callScriptErr(t, context.Background(), script, tc.name, nil, CallOptions{})
		var typeErr *RuntimeError
		if !errors.As(err, &typeErr) {
			t.Fatalf("%s: expected RuntimeError, got %T", tc.name, err)
		}
		if typeErr.Type != runtimeErrorTypeType {
			t.Fatalf("%s: RuntimeError.Type = %s, want %s", tc.name, typeErr.Type, runtimeErrorTypeType)
		}
		if typeErr.Message != tc.message {
			t.Fatalf("%s: message = %q, want %q", tc.name, typeErr.Message, tc.message)
		}
	}
}

func TestLoopControlBreakAndNext(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def for_break()
      out = []
      for n in [1, 2, 3, 4]
        if n == 3
          break
        end
        out = out + [n]
      end
      out
    end

    def for_next()
      out = []
      for n in [1, 2, 3, 4]
        if n % 2 == 0
          next
        end
        out = out + [n]
      end
      out
    end

    def while_break_next()
      n = 0
      out = []
      while n < 5
        n = n + 1
        if n == 3
          next
        end
        if n == 5
          break
        end
        out = out + [n]
      end
      out
    end

    def break_outside()
      break
    end

    def next_outside()
      next
    end
    `)

	forBreak := callFunc(t, script, "for_break", nil)
	compareArrays(t, forBreak, []Value{NewInt(1), NewInt(2)})

	forNext := callFunc(t, script, "for_next", nil)
	compareArrays(t, forNext, []Value{NewInt(1), NewInt(3)})

	whileBreakNext := callFunc(t, script, "while_break_next", nil)
	compareArrays(t, whileBreakNext, []Value{NewInt(1), NewInt(2), NewInt(4)})

	requireCallErrorContains(t, script, "break_outside", nil, CallOptions{}, "break used outside of loop")
	requireCallErrorContains(t, script, "next_outside", nil, CallOptions{}, "next used outside of loop")
}

func TestLoopControlNestedAndBlockBoundaryBehavior(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    class SetterBoundary
      def break_set=(n)
        if n == 2
          break
        end
      end

      def next_set=(n)
        if n == 2
          next
        end
      end
    end

    def nested_break()
      out = []
      for i in [1, 2]
        for j in [1, 2, 3]
          if j == 2
            break
          end
          out = out + [i * 10 + j]
        end
      end
      out
    end

    def nested_next()
      out = []
      for i in [1, 2]
        for j in [1, 2, 3]
          if j == 2
            next
          end
          out = out + [i * 10 + j]
        end
      end
      out
    end

    def break_from_block_boundary()
      out = []
      for n in [1, 2, 3]
        items = [n]
        items.each do |v|
          if v == 2
            break
          end
        end
        out = out + [n]
      end
      out
    end

    def next_from_block_boundary()
      out = []
      for n in [1, 2, 3]
        items = [n]
        items.each do |v|
          if v == 2
            next
          end
        end
        out = out + [n]
      end
      out
    end

    def break_from_setter_boundary()
      target = SetterBoundary.new
      for n in [1, 2, 3]
        target.break_set = n
      end
      true
    end

    def next_from_setter_boundary()
      target = SetterBoundary.new
      for n in [1, 2, 3]
        target.next_set = n
      end
      true
    end
    `)

	nestedBreak := callFunc(t, script, "nested_break", nil)
	compareArrays(t, nestedBreak, []Value{NewInt(11), NewInt(21)})

	nestedNext := callFunc(t, script, "nested_next", nil)
	compareArrays(t, nestedNext, []Value{NewInt(11), NewInt(13), NewInt(21), NewInt(23)})

	boundaryCases := []struct {
		name string
		fn   string
		want string
	}{
		{name: "break_from_block_boundary", fn: "break_from_block_boundary", want: "break cannot cross call boundary"},
		{name: "next_from_block_boundary", fn: "next_from_block_boundary", want: "next cannot cross call boundary"},
		{name: "break_from_setter_boundary", fn: "break_from_setter_boundary", want: "break cannot cross call boundary"},
		{name: "next_from_setter_boundary", fn: "next_from_setter_boundary", want: "next cannot cross call boundary"},
	}
	for _, tc := range boundaryCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCallErrorContains(t, script, tc.fn, nil, CallOptions{}, tc.want)
		})
	}
}

func TestLoopControlInsideClassMethods(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    class Counter
      def self.collect(limit)
        out = []
        n = 0
        while n < limit
          n = n + 1
          if n % 2 == 0
            next
          end
          if n > 5
            break
          end
          out = out + [n]
        end
        out
      end
    end

    def run(limit)
      Counter.collect(limit)
    end
    `)

	result := callFunc(t, script, "run", []Value{NewInt(10)})
	compareArrays(t, result, []Value{NewInt(1), NewInt(3), NewInt(5)})
}

func TestFunctionDefinitionWithoutParens(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def greeting
      "hi"
    end
    `)

	result := callFunc(t, script, "greeting", nil)
	if result.Kind() != KindString || result.String() != "hi" {
		t.Fatalf("unexpected result: %v", result)
	}
}

func TestFunctionDefinitionWithoutParensBindsParameters(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def inc value
      value + 1
    end

    def add left, right
      left + right
    end

    def scale value: int, factor = 2 -> int
      value * factor
    end

    def run
      [inc(4), add(2, 3), scale(5), scale(5, 3)]
    end
    `)

	result := callFunc(t, script, "run", nil)
	compareArrays(t, result, []Value{NewInt(5), NewInt(5), NewInt(10), NewInt(15)})
}
