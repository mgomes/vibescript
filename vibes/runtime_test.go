package vibes

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func compileScript(t *testing.T, source string) *Script {
	t.Helper()
	engine := NewEngine(Config{})
	script, err := engine.Compile(source)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	return script
}

func callFunc(t *testing.T, script *Script, name string, args []Value) Value {
	t.Helper()
	result, err := script.Call(context.Background(), name, args, CallOptions{})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return result
}

func TestHashMergeAndKeys(t *testing.T) {
	script := compileScript(t, `
    def merged()
      base = { name: "Alex", raised: money("10.00 USD") }
      override = { raised: money("25.00 USD") }
      base.merge(override)
    end

    def sorted_keys()
      record = { b: 2, a: 1 }
      record.keys
    end
    `)

	merged := callFunc(t, script, "merged", nil)
	if merged.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", merged.Kind())
	}
	result := merged.Hash()
	if got, want := result["name"], NewString("Alex"); !got.Equal(want) {
		t.Fatalf("name mismatch: got %v want %v", got, want)
	}
	if got, want := result["raised"], mustMoneyValue(t, "25.00 USD"); !got.Equal(want) {
		t.Fatalf("raised mismatch: got %v want %v", got, want)
	}

	keys := callFunc(t, script, "sorted_keys", nil)
	wantKeys := []Value{NewSymbol("a"), NewSymbol("b")}
	compareArrays(t, keys, wantKeys)
}

func mustMoneyValue(t *testing.T, literal string) Value {
	t.Helper()
	money, err := parseMoneyLiteral(literal)
	if err != nil {
		t.Fatalf("parse money: %v", err)
	}
	return NewMoney(money)
}

func compareArrays(t *testing.T, value Value, want []Value) {
	t.Helper()
	if value.Kind() != KindArray {
		t.Fatalf("expected array, got %v", value.Kind())
	}
	arr := value.Array()
	if len(arr) != len(want) {
		t.Fatalf("length mismatch: got %d want %d", len(arr), len(want))
	}
	for i := range arr {
		if !arr[i].Equal(want[i]) {
			t.Fatalf("element %d mismatch: got %v want %v", i, arr[i], want[i])
		}
	}
}

func TestArrayPushPopAndSum(t *testing.T) {
	script := compileScript(t, `
    def push_and_pop(values, extra)
      pushed = values.push(extra)
      result = pushed.pop()
      result
    end

    def uniq_sum(values)
      values.uniq().sum()
    end
    `)

	base := NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})
	result := callFunc(t, script, "push_and_pop", []Value{base, NewInt(4)})
	if result.Kind() != KindHash {
		t.Fatalf("expected hash result, got %v", result.Kind())
	}
	resHash := result.Hash()
	compareArrays(t, resHash["array"], []Value{NewInt(1), NewInt(2), NewInt(3)})
	if popped := resHash["popped"]; !popped.Equal(NewInt(4)) {
		t.Fatalf("popped mismatch: %v", popped)
	}

	uniq := callFunc(t, script, "uniq_sum", []Value{NewArray([]Value{NewInt(1), NewInt(1), NewInt(3)})})
	if !uniq.Equal(NewInt(4)) {
		t.Fatalf("uniq sum mismatch: got %v", uniq)
	}
}

func TestArrayConcatAndSubtract(t *testing.T) {
	script := compileScript(t, `
    def concat(first, second)
      first + second
    end

    def subtract(first, second)
      first - second
    end
    `)

	first := NewArray([]Value{NewInt(1), NewInt(2)})
	second := NewArray([]Value{NewInt(3), NewInt(2)})

	concatenated := callFunc(t, script, "concat", []Value{first, second})
	compareArrays(t, concatenated, []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(2)})

	subtracted := callFunc(t, script, "subtract", []Value{first, second})
	compareArrays(t, subtracted, []Value{NewInt(1)})
}

func TestHashLiteralSyntaxRestriction(t *testing.T) {
	engine := NewEngine(Config{})
	_, err := engine.Compile(`
    def broken()
      { "name" => "alex" }
    end
    `)
	if err == nil {
		t.Fatalf("expected compile error for legacy hash syntax")
	}
}

func TestArraySumRejectsNonNumeric(t *testing.T) {
	engine := NewEngine(Config{})
	script, err := engine.Compile(`
    def bad()
      ["a"].sum()
    end
    `)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	_, err = script.Call(context.Background(), "bad", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected runtime error for non-numeric sum")
	}
}

func TestRuntimeErrorStackTrace(t *testing.T) {
	script := compileScript(t, `
    def inner()
      assert false, "boom"
    end

    def middle()
      inner()
    end

    def outer()
      middle()
    end
    `)

	_, err := script.Call(context.Background(), "outer", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected runtime error")
	}

	var rtErr *RuntimeError
	if !errors.As(err, &rtErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if !strings.Contains(rtErr.Message, "boom") {
		t.Fatalf("message mismatch: %v", rtErr.Message)
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
}

func TestIntTimes(t *testing.T) {
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

func TestDurationMethods(t *testing.T) {
	script := compileScript(t, `
    def duration_helpers()
      d = Duration.build(3600)
      {
        iso: d.iso8601,
        parts: d.parts,
        in_hours: d.in_hours,
        seconds: d.seconds,
        to_i: d.to_i,
        eql: d.eql?(Duration.parse("PT1H")),
        months: Duration.build(2592000).in_months
      }
    end

    def duration_after(base)
      60.seconds.after(base)
    end

    def duration_ago(base)
      60.seconds.ago(base)
    end

    def duration_parse_iso()
      Duration.parse("P1DT1H1M1S").to_i
    end

    def duration_parse_week()
      Duration.parse("P2W").to_i
    end

    def duration_parse_invalid()
      Duration.parse("P1DT1HXYZ")
    end
    `)

	result := callFunc(t, script, "duration_helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	parts := result.Hash()
	if got, want := parts["iso"].String(), "PT1H"; got != want {
		t.Fatalf("iso8601 mismatch: got %s want %s", got, want)
	}
	if got, want := parts["to_i"], NewInt(3600); !got.Equal(want) {
		t.Fatalf("to_i mismatch: got %v want %v", got, want)
	}
	if got, want := parts["seconds"], NewInt(3600); !got.Equal(want) {
		t.Fatalf("seconds mismatch: got %v want %v", got, want)
	}
	if got := parts["in_hours"]; got.Kind() != KindFloat || got.Float() != 1 {
		t.Fatalf("in_hours mismatch: %v", got)
	}
	if got := parts["months"]; got.Kind() != KindFloat || got.Float() != 1 {
		t.Fatalf("in_months mismatch: %v", got)
	}
	if got := parts["eql"]; got.Kind() != KindBool || !got.Bool() {
		t.Fatalf("expected eql? to be true, got %v", got)
	}

	partsVal := parts["parts"]
	if partsVal.Kind() != KindHash {
		t.Fatalf("parts should be hash, got %v", partsVal.Kind())
	}
	partsMap := partsVal.Hash()
	if partsMap["hours"] != NewInt(1) || partsMap["minutes"] != NewInt(0) || partsMap["seconds"] != NewInt(0) {
		t.Fatalf("parts unexpected: %#v", partsMap)
	}

	base := NewString("2024-01-01T00:00:00Z")
	after := callFunc(t, script, "duration_after", []Value{base})
	if got := after.String(); got != "2024-01-01T00:01:00Z" {
		t.Fatalf("after mismatch: %s", got)
	}

	before := callFunc(t, script, "duration_ago", []Value{NewString("2024-01-01T00:01:00Z")})
	if got := before.String(); got != "2024-01-01T00:00:00Z" {
		t.Fatalf("ago mismatch: %s", got)
	}

	parsed := callFunc(t, script, "duration_parse_iso", nil)
	if !parsed.Equal(NewInt(90061)) {
		t.Fatalf("parse iso mismatch: got %v want 90061", parsed)
	}

	weeks := callFunc(t, script, "duration_parse_week", nil)
	if !weeks.Equal(NewInt(1209600)) {
		t.Fatalf("parse weeks mismatch: got %v", weeks)
	}

	_, err := script.Call(context.Background(), "duration_parse_invalid", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected parse error for invalid duration")
	}

	badOrder := compileScript(t, `
    def run()
      Duration.parse("PT1S30M")
    end
    `)
	_, err = badOrder.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected parse error for out-of-order duration")
	}
}

func TestArrayAndHashHelpers(t *testing.T) {
	script := compileScript(t, `
    def array_helpers()
      [1, nil, 2, nil].compact()
    end

    def array_flatten()
      [[1], [2, [3]]].flatten()
    end

    def array_flatten_depth()
      [[1], [2, [3, [4]]]].flatten(1)
    end

    def array_join()
      ["a", "b", "c"].join("-")
    end

    def hash_compact()
      { a: 1, b: nil, c: 3 }.compact()
    end
    `)

	compact := callFunc(t, script, "array_helpers", nil)
	compareArrays(t, compact, []Value{NewInt(1), NewInt(2)})

	flatten := callFunc(t, script, "array_flatten", nil)
	compareArrays(t, flatten, []Value{NewInt(1), NewInt(2), NewInt(3)})

	flattenDepth := callFunc(t, script, "array_flatten_depth", nil)
	// flatten(1) flattens one level: [[1], [2, [3, [4]]]] -> [1, 2, [3, [4]]]
	if flattenDepth.Kind() != KindArray {
		t.Fatalf("expected array, got %v", flattenDepth.Kind())
	}
	arr := flattenDepth.Array()
	if len(arr) != 3 {
		t.Fatalf("expected 3 elements after flatten(1), got %d", len(arr))
	}
	if arr[0].Int() != 1 || arr[1].Int() != 2 {
		t.Fatalf("unexpected first two elements: %v, %v", arr[0], arr[1])
	}
	// Third element should still be nested: [3, [4]]
	if arr[2].Kind() != KindArray {
		t.Fatalf("expected third element to be array, got %v", arr[2].Kind())
	}

	joined := callFunc(t, script, "array_join", nil)
	if joined.Kind() != KindString || joined.String() != "a-b-c" {
		t.Fatalf("unexpected join result: %#v", joined)
	}

	hashResult := callFunc(t, script, "hash_compact", nil)
	if hashResult.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", hashResult.Kind())
	}
	h := hashResult.Hash()
	if len(h) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(h))
	}
	if _, ok := h["b"]; ok {
		t.Fatalf("expected key b to be removed")
	}
}

func TestStringHelpers(t *testing.T) {
	script := compileScript(t, `
    def helpers()
      ["  hello  ".strip(), "hi".upcase(), "BYE".downcase(), "a b c".split()]
    end

    def split_custom()
      "a,b,c".split(",")
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindArray {
		t.Fatalf("expected array, got %v", result.Kind())
	}
	arr := result.Array()
	if len(arr) != 4 {
		t.Fatalf("unexpected length: %d", len(arr))
	}
	if arr[0].String() != "hello" {
		t.Fatalf("strip mismatch: %s", arr[0].String())
	}
	if arr[1].String() != "HI" {
		t.Fatalf("upcase mismatch: %s", arr[1].String())
	}
	if arr[2].String() != "bye" {
		t.Fatalf("downcase mismatch: %s", arr[2].String())
	}
	compareArrays(t, arr[3], []Value{NewString("a"), NewString("b"), NewString("c")})

	customSplit := callFunc(t, script, "split_custom", nil)
	compareArrays(t, customSplit, []Value{NewString("a"), NewString("b"), NewString("c")})
}

func TestDurationHelpers(t *testing.T) {
	script := compileScript(t, `
    def minutes()
      (90.seconds).minutes
    end

    def hours()
      (7200.seconds).hours
    end

    def format()
      (2.hours).format
    end
    `)

	minutes := callFunc(t, script, "minutes", nil)
	if !minutes.Equal(NewInt(1)) {
		t.Fatalf("minutes mismatch: %#v", minutes)
	}
	hours := callFunc(t, script, "hours", nil)
	if !hours.Equal(NewInt(2)) {
		t.Fatalf("hours mismatch: %#v", hours)
	}
	formatted := callFunc(t, script, "format", nil)
	if formatted.Kind() != KindString || formatted.String() != "7200s" {
		t.Fatalf("format mismatch: %#v", formatted)
	}
}

func TestNowBuiltin(t *testing.T) {
	script := compileScript(t, `
    def current()
      now()
    end
    `)

	result := callFunc(t, script, "current", nil)
	if result.Kind() != KindString {
		t.Fatalf("expected string, got %v", result.Kind())
	}
	if _, err := time.Parse(time.RFC3339, result.String()); err != nil {
		t.Fatalf("now() output not RFC3339: %v", err)
	}
}

func TestMethodErrorHandling(t *testing.T) {
	tests := []struct {
		name   string
		script string
		errMsg string
	}{
		{
			name:   "string.split with non-string separator",
			script: `def run() "hello".split(123) end`,
			errMsg: "separator must be string",
		},
		{
			name:   "array.flatten with negative depth",
			script: `def run() [[1, 2]].flatten(-1) end`,
			errMsg: "must be non-negative",
		},
		{
			name:   "array.join with non-string separator",
			script: `def run() [1, 2, 3].join(123) end`,
			errMsg: "separator must be string",
		},
		{
			name:   "string unknown method",
			script: `def run() "hello".unknown_method() end`,
			errMsg: "unknown string method",
		},
		{
			name:   "hash unknown method",
			script: `def run() {a: 1}.unknown_method() end`,
			errMsg: "unknown hash method",
		},
		{
			name:   "array unknown method",
			script: `def run() [1, 2].unknown_method() end`,
			errMsg: "unknown array method",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := compileScript(t, tt.script)
			_, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err == nil {
				t.Fatalf("expected error containing %q", tt.errMsg)
			}
			if !strings.Contains(err.Error(), tt.errMsg) {
				t.Fatalf("expected error containing %q, got: %v", tt.errMsg, err)
			}
		})
	}
}

func TestRuntimeErrorFromBuiltin(t *testing.T) {
	script := compileScript(t, `
    def divide(a, b)
      a / b
    end

    def calculate()
      divide(10, 0)
    end
    `)

	_, err := script.Call(context.Background(), "calculate", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected runtime error for division by zero")
	}

	var rtErr *RuntimeError
	if !errors.As(err, &rtErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if !strings.Contains(rtErr.Message, "division by zero") {
		t.Fatalf("expected division by zero error, got: %v", rtErr.Message)
	}

	// Should have stack frames showing where the error occurred
	if len(rtErr.Frames) < 2 {
		t.Fatalf("expected at least 2 frames, got %d", len(rtErr.Frames))
	}

	// Error occurred in divide function
	if rtErr.Frames[0].Function != "divide" {
		t.Fatalf("expected divide frame first, got %s", rtErr.Frames[0].Function)
	}
}

func TestRuntimeErrorNoCallStack(t *testing.T) {
	script := compileScript(t, `
    def test()
      1 / 0
    end
    `)

	_, err := script.Call(context.Background(), "test", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected runtime error")
	}

	var rtErr *RuntimeError
	if !errors.As(err, &rtErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}

	// Should have at least the error location
	if len(rtErr.Frames) == 0 {
		t.Fatalf("expected at least one frame")
	}
}
