package runtime

import (
	"context"
	"errors"
	"testing"
)

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
	requireCallErrorIs(t, spinScript, "spin", nil, CallOptions{}, errStepQuotaExceeded)
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
	requireCallErrorIs(t, spinScript, "spin_until", nil, CallOptions{}, errStepQuotaExceeded)
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
		{name: "large_integer_range_matches_exact", fn: "large_integer_range", arg: NewInt(9007199254740992), want: NewString("exact")},
		{name: "large_integer_range_does_not_round", fn: "large_integer_range", arg: NewInt(9007199254740993), want: NewString("miss")},
		{name: "large_float_range_does_not_round_bounds", fn: "large_float_range", arg: NewFloat(9007199254740992), want: NewString("miss")},
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

	err := callScriptErr(t, context.Background(), script, "rescue_mismatch", nil, CallOptions{})
	requireErrorContains(t, err, "division by zero")
	var divideErr *RuntimeError
	if !errors.As(err, &divideErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if divideErr.Type != runtimeErrorTypeBase {
		t.Fatalf("expected runtime error type %s, got %s", runtimeErrorTypeBase, divideErr.Type)
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

func TestBeginRescueDoesNotCatchHostControlSignals(t *testing.T) {
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

	requireCallErrorIs(t, script, "run", nil, CallOptions{}, errStepQuotaExceeded)
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

	requireCallErrorContains(t, script, "raise_outside", nil, CallOptions{}, "raise used outside of rescue")
	err = callScriptErr(t, context.Background(), script, "raise_new_message", nil, CallOptions{})
	requireErrorContains(t, err, "custom boom")
	var raisedErr *RuntimeError
	if !errors.As(err, &raisedErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if raisedErr.Type != runtimeErrorTypeBase {
		t.Fatalf("expected runtime error type %s, got %s", runtimeErrorTypeBase, raisedErr.Type)
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
