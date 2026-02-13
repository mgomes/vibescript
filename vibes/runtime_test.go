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
	engine := MustNewEngine(Config{})
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

func TestHashExpandedHelpers(t *testing.T) {
	script := compileScript(t, `
    def helpers()
      record = { b: 2, a: 1, c: 3 }
      with_nil = { a: 1, b: nil, c: 3 }
      nested = { user: { profile: { name: "Alex" } } }

      each_pairs = []
      record.each do |k, v|
        each_pairs = each_pairs.push(k + "=" + v)
      end

      each_keys = []
      record.each_key do |k|
        each_keys = each_keys.push(k)
      end

	      each_values = []
	      record.each_value do |v|
	        each_values = each_values.push(v)
	      end

	      select_gt1 = record.select do |k, v|
	        v > 1
	      end

	      reject_even = record.reject do |k, v|
	        v % 2 == 0
	      end

	      transform_keys = record.transform_keys do |k|
	        "x_" + k
	      end

	      transform_values = record.transform_values do |v|
	        v * 10
	      end

	      collision = { b: 2, a: 1 }.transform_keys do |k|
	        "same"
	      end

	      {
	        size: record.size,
	        length: record.length,
        empty_false: record.empty?,
        empty_true: {}.empty?,
        key_symbol: record.key?(:a),
        key_string: record.has_key?("b"),
        include_symbol: record.include?(:c),
        missing_key: record.key?(:missing),
        keys: record.keys,
        values: record.values,
        fetch_hit: record.fetch(:a),
        fetch_default: record.fetch(:missing, 99),
        fetch_nil: record.fetch(:missing),
        dig_hit: nested.dig(:user, :profile, :name),
        dig_miss: nested.dig(:user, :profile, :missing),
        dig_through_scalar: nested.dig(:user, :profile, :name, :length),
        slice: record.slice(:a, "c"),
	        except: record.except(:b),
	        each_pairs: each_pairs,
	        each_keys: each_keys,
	        each_values: each_values,
	        select_gt1: select_gt1,
	        reject_even: reject_even,
	        transform_keys: transform_keys,
	        transform_values: transform_values,
	        compact: with_nil.compact(),
	        collision: collision
	      }
	    end
	    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if !got["size"].Equal(NewInt(3)) {
		t.Fatalf("size mismatch: %v", got["size"])
	}
	if !got["length"].Equal(NewInt(3)) {
		t.Fatalf("length mismatch: %v", got["length"])
	}
	if got["empty_false"].Bool() {
		t.Fatalf("expected empty_false to be false")
	}
	if !got["empty_true"].Bool() {
		t.Fatalf("expected empty_true to be true")
	}
	if !got["key_symbol"].Bool() || !got["key_string"].Bool() || !got["include_symbol"].Bool() {
		t.Fatalf("key/include mismatch: %#v", got)
	}
	if got["missing_key"].Bool() {
		t.Fatalf("missing_key should be false")
	}
	compareArrays(t, got["keys"], []Value{NewSymbol("a"), NewSymbol("b"), NewSymbol("c")})
	compareArrays(t, got["values"], []Value{NewInt(1), NewInt(2), NewInt(3)})

	if !got["fetch_hit"].Equal(NewInt(1)) {
		t.Fatalf("fetch_hit mismatch: %v", got["fetch_hit"])
	}
	if !got["fetch_default"].Equal(NewInt(99)) {
		t.Fatalf("fetch_default mismatch: %v", got["fetch_default"])
	}
	if got["fetch_nil"].Kind() != KindNil {
		t.Fatalf("fetch_nil expected nil, got %v", got["fetch_nil"])
	}
	if got["dig_hit"].Kind() != KindString || got["dig_hit"].String() != "Alex" {
		t.Fatalf("dig_hit mismatch: %v", got["dig_hit"])
	}
	if got["dig_miss"].Kind() != KindNil {
		t.Fatalf("dig_miss expected nil, got %v", got["dig_miss"])
	}
	if got["dig_through_scalar"].Kind() != KindNil {
		t.Fatalf("dig_through_scalar expected nil, got %v", got["dig_through_scalar"])
	}

	slice := got["slice"]
	if slice.Kind() != KindHash {
		t.Fatalf("slice expected hash, got %v", slice.Kind())
	}
	compareHash(t, slice.Hash(), map[string]Value{"a": NewInt(1), "c": NewInt(3)})

	except := got["except"]
	if except.Kind() != KindHash {
		t.Fatalf("except expected hash, got %v", except.Kind())
	}
	compareHash(t, except.Hash(), map[string]Value{"a": NewInt(1), "c": NewInt(3)})

	compareArrays(t, got["each_pairs"], []Value{NewString("a=1"), NewString("b=2"), NewString("c=3")})
	compareArrays(t, got["each_keys"], []Value{NewSymbol("a"), NewSymbol("b"), NewSymbol("c")})
	compareArrays(t, got["each_values"], []Value{NewInt(1), NewInt(2), NewInt(3)})

	selectGT1 := got["select_gt1"]
	if selectGT1.Kind() != KindHash {
		t.Fatalf("select_gt1 expected hash, got %v", selectGT1.Kind())
	}
	compareHash(t, selectGT1.Hash(), map[string]Value{"b": NewInt(2), "c": NewInt(3)})

	rejectEven := got["reject_even"]
	if rejectEven.Kind() != KindHash {
		t.Fatalf("reject_even expected hash, got %v", rejectEven.Kind())
	}
	compareHash(t, rejectEven.Hash(), map[string]Value{"a": NewInt(1), "c": NewInt(3)})

	transformedKeys := got["transform_keys"]
	if transformedKeys.Kind() != KindHash {
		t.Fatalf("transform_keys expected hash, got %v", transformedKeys.Kind())
	}
	compareHash(t, transformedKeys.Hash(), map[string]Value{"x_a": NewInt(1), "x_b": NewInt(2), "x_c": NewInt(3)})

	transformedValues := got["transform_values"]
	if transformedValues.Kind() != KindHash {
		t.Fatalf("transform_values expected hash, got %v", transformedValues.Kind())
	}
	compareHash(t, transformedValues.Hash(), map[string]Value{"a": NewInt(10), "b": NewInt(20), "c": NewInt(30)})

	compacted := got["compact"]
	if compacted.Kind() != KindHash {
		t.Fatalf("compact expected hash, got %v", compacted.Kind())
	}
	compareHash(t, compacted.Hash(), map[string]Value{"a": NewInt(1), "c": NewInt(3)})

	collision := got["collision"]
	if collision.Kind() != KindHash {
		t.Fatalf("collision expected hash, got %v", collision.Kind())
	}
	compareHash(t, collision.Hash(), map[string]Value{"same": NewInt(2)})
}

func TestHashHelpersSupportObjectReceiver(t *testing.T) {
	script := compileScript(t, `
	    def lookup(record)
	      {
	        fetch: record.fetch(:name),
	        has_key: record.key?(:name),
	        dig: record.dig(:meta, :title)
	      }
	    end
	    `)

	arg := NewObject(map[string]Value{
		"name": NewString("Alex"),
		"meta": NewHash(map[string]Value{
			"title": NewString("Captain"),
		}),
	})
	result := callFunc(t, script, "lookup", []Value{arg})
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if !got["fetch"].Equal(NewString("Alex")) {
		t.Fatalf("fetch mismatch: %v", got["fetch"])
	}
	if got["has_key"].Kind() != KindBool || !got["has_key"].Bool() {
		t.Fatalf("has_key mismatch: %v", got["has_key"])
	}
	if !got["dig"].Equal(NewString("Captain")) {
		t.Fatalf("dig mismatch: %v", got["dig"])
	}
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

func compareHash(t *testing.T, got map[string]Value, want map[string]Value) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("hash length mismatch: got %d want %d", len(got), len(want))
	}
	for key, wantValue := range want {
		gotValue, ok := got[key]
		if !ok {
			t.Fatalf("missing key %q", key)
		}
		if !gotValue.Equal(wantValue) {
			t.Fatalf("key %q mismatch: got %v want %v", key, gotValue, wantValue)
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

func TestArrayPhaseTwoHelpers(t *testing.T) {
	script := compileScript(t, `
    def helpers()
      values = [3, 1, 2, 1]
      truthy = [nil, false, 0]

      find_hit = values.find do |v|
        v > 2
      end
      find_miss = values.find do |v|
        v > 9
      end
      find_index_hit = values.find_index do |v|
        v % 2 == 0
      end
      find_index_miss = values.find_index do |v|
        v > 9
      end
      count_block = values.count do |v|
        v > 1
      end
      any_block = values.any? do |v|
        v > 2
      end
      all_block = values.all? do |v|
        v >= 1
      end
      none_block = values.none? do |v|
        v < 0
      end
      sort_block_desc = values.sort do |a, b|
        b - a
      end
      sort_by_words = ["bbb", "a", "cc"].sort_by do |v|
        v.length
      end
      partitioned = values.partition do |v|
        v % 2 == 0
      end
      grouped = values.group_by do |v|
        if v % 2 == 0
          :even
        else
          :odd
        end
      end
      tally_block = [1, 2, 3, 4].tally do |v|
        if v % 2 == 0
          :even
        else
          :odd
        end
      end

      {
        include_hit: values.include?(2),
        include_miss: values.include?(9),
        index_hit: values.index(1),
        index_offset_hit: values.index(1, 2),
        index_miss: values.index(9),
        rindex_hit: values.rindex(1),
        rindex_offset_hit: values.rindex(1, 2),
        rindex_miss: values.rindex(9),
        find_hit: find_hit,
        find_miss: find_miss,
        find_index_hit: find_index_hit,
        find_index_miss: find_index_miss,
        count_all: values.count,
        count_value: values.count(1),
        count_block: count_block,
        any_block: any_block,
        any_plain: truthy.any?,
        all_block: all_block,
        all_plain: [1, 2, 3].all?,
        none_block: none_block,
        none_plain: [nil, false].none?,
        reverse: values.reverse,
        sort_default: values.sort,
        sort_block_desc: sort_block_desc,
        sort_by_words: sort_by_words,
        partition: partitioned,
        group_by_parity: grouped,
        tally_plain: ["a", "b", "a", "a"].tally,
        tally_block: tally_block,
        original: values
      }
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if !got["include_hit"].Bool() || got["include_miss"].Bool() {
		t.Fatalf("include? mismatch: %#v", got)
	}
	if !got["index_hit"].Equal(NewInt(1)) || !got["index_offset_hit"].Equal(NewInt(3)) {
		t.Fatalf("index mismatch: hit=%v offset=%v", got["index_hit"], got["index_offset_hit"])
	}
	if got["index_miss"].Kind() != KindNil {
		t.Fatalf("index_miss expected nil, got %v", got["index_miss"])
	}
	if !got["rindex_hit"].Equal(NewInt(3)) || !got["rindex_offset_hit"].Equal(NewInt(1)) {
		t.Fatalf("rindex mismatch: hit=%v offset=%v", got["rindex_hit"], got["rindex_offset_hit"])
	}
	if got["rindex_miss"].Kind() != KindNil {
		t.Fatalf("rindex_miss expected nil, got %v", got["rindex_miss"])
	}
	if !got["find_hit"].Equal(NewInt(3)) || got["find_miss"].Kind() != KindNil {
		t.Fatalf("find mismatch: hit=%v miss=%v", got["find_hit"], got["find_miss"])
	}
	if !got["find_index_hit"].Equal(NewInt(2)) || got["find_index_miss"].Kind() != KindNil {
		t.Fatalf("find_index mismatch: hit=%v miss=%v", got["find_index_hit"], got["find_index_miss"])
	}
	if !got["count_all"].Equal(NewInt(4)) || !got["count_value"].Equal(NewInt(2)) || !got["count_block"].Equal(NewInt(2)) {
		t.Fatalf("count mismatch: %#v", got)
	}
	if !got["any_block"].Bool() || got["any_plain"].Bool() {
		t.Fatalf("any? mismatch: block=%v plain=%v", got["any_block"], got["any_plain"])
	}
	if !got["all_block"].Bool() || !got["all_plain"].Bool() {
		t.Fatalf("all? mismatch: block=%v plain=%v", got["all_block"], got["all_plain"])
	}
	if !got["none_block"].Bool() || !got["none_plain"].Bool() {
		t.Fatalf("none? mismatch: block=%v plain=%v", got["none_block"], got["none_plain"])
	}
	compareArrays(t, got["reverse"], []Value{NewInt(1), NewInt(2), NewInt(1), NewInt(3)})
	compareArrays(t, got["sort_default"], []Value{NewInt(1), NewInt(1), NewInt(2), NewInt(3)})
	compareArrays(t, got["sort_block_desc"], []Value{NewInt(3), NewInt(2), NewInt(1), NewInt(1)})
	compareArrays(t, got["sort_by_words"], []Value{NewString("a"), NewString("cc"), NewString("bbb")})

	if got["partition"].Kind() != KindArray {
		t.Fatalf("partition expected array, got %v", got["partition"].Kind())
	}
	partition := got["partition"].Array()
	if len(partition) != 2 {
		t.Fatalf("partition length mismatch: %d", len(partition))
	}
	compareArrays(t, partition[0], []Value{NewInt(2)})
	compareArrays(t, partition[1], []Value{NewInt(3), NewInt(1), NewInt(1)})

	grouped := got["group_by_parity"]
	if grouped.Kind() != KindHash {
		t.Fatalf("group_by_parity expected hash, got %v", grouped.Kind())
	}
	groupedHash := grouped.Hash()
	compareArrays(t, groupedHash["odd"], []Value{NewInt(3), NewInt(1), NewInt(1)})
	compareArrays(t, groupedHash["even"], []Value{NewInt(2)})

	tallyPlain := got["tally_plain"]
	if tallyPlain.Kind() != KindHash {
		t.Fatalf("tally_plain expected hash, got %v", tallyPlain.Kind())
	}
	compareHash(t, tallyPlain.Hash(), map[string]Value{
		"a": NewInt(3),
		"b": NewInt(1),
	})

	tallyBlock := got["tally_block"]
	if tallyBlock.Kind() != KindHash {
		t.Fatalf("tally_block expected hash, got %v", tallyBlock.Kind())
	}
	compareHash(t, tallyBlock.Hash(), map[string]Value{
		"odd":  NewInt(2),
		"even": NewInt(2),
	})

	compareArrays(t, got["original"], []Value{NewInt(3), NewInt(1), NewInt(2), NewInt(1)})
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
	engine := MustNewEngine(Config{})
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
	engine := MustNewEngine(Config{})
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
      60.seconds.after(base).to_s
    end

    def duration_ago(base)
      60.seconds.ago(base).to_s
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

    def duration_parse_empty()
      Duration.parse("P")
    end

    def duration_parse_fractional()
      Duration.parse("1.5s")
    end

    def duration_add()
      (4.seconds + 2.hours).to_i
    end

    def duration_subtract()
      (2.hours - 4.seconds).to_i
    end

    def duration_multiply()
      (10.seconds * 3).to_i
    end

    def duration_multiply_left()
      (3 * 10.seconds).to_i
    end

    def duration_divide()
      (10.seconds / 2).to_i
    end

    def duration_divide_duration()
      10.seconds / 4.seconds
    end

    def duration_modulo()
      (10.seconds % 4.seconds).to_i
    end

    def duration_compare()
      [2.seconds < 3.seconds, 5.seconds == 5.seconds, 10.seconds > 3.seconds]
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

	empty := compileScript(t, `
    def run()
      Duration.parse("P")
    end
    `)
	_, err = empty.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected parse error for empty duration")
	}

	fractional := compileScript(t, `
    def run()
      Duration.parse("1.5s")
    end
    `)
	_, err = fractional.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected parse error for fractional duration")
	}

	if got := callFunc(t, script, "duration_add", nil); !got.Equal(NewInt(7204)) {
		t.Fatalf("duration add mismatch: %v", got)
	}
	if got := callFunc(t, script, "duration_subtract", nil); !got.Equal(NewInt(7196)) {
		t.Fatalf("duration subtract mismatch: %v", got)
	}
	if got := callFunc(t, script, "duration_multiply", nil); !got.Equal(NewInt(30)) {
		t.Fatalf("duration multiply mismatch: %v", got)
	}
	if got := callFunc(t, script, "duration_multiply_left", nil); !got.Equal(NewInt(30)) {
		t.Fatalf("duration multiply (left) mismatch: %v", got)
	}
	if got := callFunc(t, script, "duration_divide", nil); !got.Equal(NewInt(5)) {
		t.Fatalf("duration divide mismatch: %v", got)
	}
	divDur := callFunc(t, script, "duration_divide_duration", nil)
	if divDur.Kind() != KindFloat || divDur.Float() != 2.5 {
		t.Fatalf("duration divide duration mismatch: %v", divDur)
	}
	if got := callFunc(t, script, "duration_modulo", nil); !got.Equal(NewInt(2)) {
		t.Fatalf("duration modulo mismatch: %v", got)
	}
	comp := callFunc(t, script, "duration_compare", nil)
	wantComp := arrayVal(boolVal(true), boolVal(true), boolVal(true))
	compareArrays(t, comp, wantComp.Array())
}

func TestFunctionDefinitionWithoutParens(t *testing.T) {
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

func TestTimeFormatUsesGoLayout(t *testing.T) {
	script := compileScript(t, `
    def run()
      t = Time.utc(2000, 1, 1, 20, 15, 1)
      {
        y2: t.format("06"),
        y4: t.format("2006"),
        date: t.format("2006-01-02"),
        time: t.format("15:04:05")
      }
    end
    `)

	result := callFunc(t, script, "run", nil)
	want := hashVal(map[string]Value{
		"y2":   NewString("00"),
		"y4":   NewString("2000"),
		"date": NewString("2000-01-01"),
		"time": NewString("20:15:01"),
	})
	if result.Kind() != KindHash {
		t.Fatalf("unexpected format output: %#v", result)
	}
	got := result.Hash()
	for key, expected := range want.Hash() {
		if val, ok := got[key]; !ok || !val.Equal(expected) {
			t.Fatalf("unexpected format output %s: got %v want %v", key, val, expected)
		}
	}
}

func TestTypedFunctions(t *testing.T) {
	script := compileScript(t, `
    def pick_second(n: int, m: int) -> int
      m
    end

    def pick_maybe(n: int, m: int = 0) -> int
      m
    end

    def nil_result() -> nil
      nil
    end

    def kw_only(n: int, m: int)
      m
    end

    def mixed(n: int, m: int) -> int
      n + m
    end

    def bad_return(n: int) -> int
      "oops"
    end

    def pick_optional(s: string? = nil) -> string?
      s
    end
    `)

	if fn, ok := script.Function("bad_return"); !ok || fn.ReturnTy == nil {
		t.Fatalf("expected bad_return to have return type")
	} else if fn.ReturnTy.Name != "int" {
		t.Fatalf("unexpected return type name: %s", fn.ReturnTy.Name)
	}

	if got := callFunc(t, script, "pick_second", []Value{NewInt(1), NewInt(2)}); !got.Equal(NewInt(2)) {
		t.Fatalf("pick_second mismatch: %v", got)
	}
	if got := callFunc(t, script, "pick_maybe", []Value{NewInt(1)}); !got.Equal(NewInt(0)) {
		t.Fatalf("pick_maybe default mismatch: %v", got)
	}
	if got := callFunc(t, script, "pick_optional", nil); !got.Equal(NewNil()) {
		t.Fatalf("pick_optional nil mismatch: %v", got)
	}
	if got := callFunc(t, script, "nil_result", nil); !got.Equal(NewNil()) {
		t.Fatalf("nil_result mismatch: %v", got)
	}

	kwPos := callFunc(t, script, "kw_only", []Value{NewInt(1), NewInt(2)})
	if !kwPos.Equal(NewInt(2)) {
		t.Fatalf("kw_only positional mismatch: %v", kwPos)
	}
	_, err := script.Call(context.Background(), "kw_only", []Value{NewInt(1)}, CallOptions{
		Globals: map[string]Value{},
	})
	if err == nil || !strings.Contains(err.Error(), "missing argument m") {
		t.Fatalf("expected kw_only missing arg error, got %v", err)
	}

	mixedResult := callFunc(t, script, "mixed", []Value{NewInt(1), NewInt(2)})
	if !mixedResult.Equal(NewInt(3)) {
		t.Fatalf("mixed result mismatch: %v", mixedResult)
	}

	_, err = script.Call(context.Background(), "pick_second", []Value{NewString("bad"), NewInt(2)}, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "expected int") {
		t.Fatalf("expected type error, got %v", err)
	}

	_, err = script.Call(context.Background(), "bad_return", []Value{NewInt(1)}, CallOptions{})
	if err == nil {
		res, _ := script.Call(context.Background(), "bad_return", []Value{NewInt(1)}, CallOptions{})
		t.Fatalf("expected return type error, got value %v (%v)", res, res.Kind())
	}
	if !strings.Contains(err.Error(), "expected int") {
		t.Fatalf("expected return type error, got %v", err)
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

func TestStringPredicatesAndLength(t *testing.T) {
	script := compileScript(t, `
    def helpers()
      {
        empty_true: "".empty?,
        empty_false: "hello".empty?,
        starts_true: "hello".start_with?("he"),
        starts_false: "hello".start_with?("lo"),
        ends_true: "hello".end_with?("lo"),
        ends_false: "hello".end_with?("he"),
        length_alias: "héllo".length,
        size: "héllo".size
      }
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if !got["empty_true"].Bool() {
		t.Fatalf("expected empty_true to be true")
	}
	if got["empty_false"].Bool() {
		t.Fatalf("expected empty_false to be false")
	}
	if !got["starts_true"].Bool() {
		t.Fatalf("expected starts_true to be true")
	}
	if got["starts_false"].Bool() {
		t.Fatalf("expected starts_false to be false")
	}
	if !got["ends_true"].Bool() {
		t.Fatalf("expected ends_true to be true")
	}
	if got["ends_false"].Bool() {
		t.Fatalf("expected ends_false to be false")
	}
	if got["length_alias"].Int() != 5 {
		t.Fatalf("length mismatch: %v", got["length_alias"])
	}
	if got["size"].Int() != 5 {
		t.Fatalf("size mismatch: %v", got["size"])
	}
}

func TestStringBoundaryHelpers(t *testing.T) {
	script := compileScript(t, `
    def helpers()
      {
        lstrip: "  hello\t".lstrip,
        rstrip: "\thello  ".rstrip,
        chomp_nl: "line\n".chomp,
        chomp_none: "line".chomp,
        chomp_custom: "path///".chomp("/"),
        chomp_empty_sep: "line\n\n".chomp(""),
        delete_prefix_hit: "unhappy".delete_prefix("un"),
        delete_prefix_miss: "happy".delete_prefix("un"),
        delete_suffix_hit: "report.csv".delete_suffix(".csv"),
        delete_suffix_miss: "report.csv".delete_suffix(".txt")
      }
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if got["lstrip"].String() != "hello\t" {
		t.Fatalf("lstrip mismatch: %q", got["lstrip"].String())
	}
	if got["rstrip"].String() != "\thello" {
		t.Fatalf("rstrip mismatch: %q", got["rstrip"].String())
	}
	if got["chomp_nl"].String() != "line" {
		t.Fatalf("chomp_nl mismatch: %q", got["chomp_nl"].String())
	}
	if got["chomp_none"].String() != "line" {
		t.Fatalf("chomp_none mismatch: %q", got["chomp_none"].String())
	}
	if got["chomp_custom"].String() != "path//" {
		t.Fatalf("chomp_custom mismatch: %q", got["chomp_custom"].String())
	}
	if got["chomp_empty_sep"].String() != "line" {
		t.Fatalf("chomp_empty_sep mismatch: %q", got["chomp_empty_sep"].String())
	}
	if got["delete_prefix_hit"].String() != "happy" {
		t.Fatalf("delete_prefix_hit mismatch: %q", got["delete_prefix_hit"].String())
	}
	if got["delete_prefix_miss"].String() != "happy" {
		t.Fatalf("delete_prefix_miss mismatch: %q", got["delete_prefix_miss"].String())
	}
	if got["delete_suffix_hit"].String() != "report" {
		t.Fatalf("delete_suffix_hit mismatch: %q", got["delete_suffix_hit"].String())
	}
	if got["delete_suffix_miss"].String() != "report.csv" {
		t.Fatalf("delete_suffix_miss mismatch: %q", got["delete_suffix_miss"].String())
	}
}

func TestStringSearchAndSlice(t *testing.T) {
	script := compileScript(t, `
    def helpers()
      text = "héllo hello"
      {
        include_true: text.include?("llo"),
        include_false: text.include?("zzz"),
        index_hit: text.index("llo"),
        index_offset_hit: text.index("llo", 6),
        index_miss: text.index("zzz"),
        rindex_hit: text.rindex("llo"),
        rindex_offset_hit: text.rindex("llo", 4),
        rindex_miss: text.rindex("zzz"),
        slice_char: text.slice(1),
        slice_range: text.slice(1, 4),
        slice_oob: text.slice(99),
        slice_negative_len: text.slice(1, -1),
        slice_huge_len: text.slice(1, 9223372036854775807)
      }
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if !got["include_true"].Bool() {
		t.Fatalf("include_true mismatch")
	}
	if got["include_false"].Bool() {
		t.Fatalf("include_false mismatch")
	}
	if got["index_hit"].Int() != 2 {
		t.Fatalf("index_hit mismatch: %v", got["index_hit"])
	}
	if got["index_offset_hit"].Int() != 8 {
		t.Fatalf("index_offset_hit mismatch: %v", got["index_offset_hit"])
	}
	if got["index_miss"].Kind() != KindNil {
		t.Fatalf("index_miss expected nil, got %v", got["index_miss"])
	}
	if got["rindex_hit"].Int() != 8 {
		t.Fatalf("rindex_hit mismatch: %v", got["rindex_hit"])
	}
	if got["rindex_offset_hit"].Int() != 2 {
		t.Fatalf("rindex_offset_hit mismatch: %v", got["rindex_offset_hit"])
	}
	if got["rindex_miss"].Kind() != KindNil {
		t.Fatalf("rindex_miss expected nil, got %v", got["rindex_miss"])
	}
	if got["slice_char"].String() != "é" {
		t.Fatalf("slice_char mismatch: %q", got["slice_char"].String())
	}
	if got["slice_range"].String() != "éllo" {
		t.Fatalf("slice_range mismatch: %q", got["slice_range"].String())
	}
	if got["slice_oob"].Kind() != KindNil {
		t.Fatalf("slice_oob expected nil, got %v", got["slice_oob"])
	}
	if got["slice_negative_len"].Kind() != KindNil {
		t.Fatalf("slice_negative_len expected nil, got %v", got["slice_negative_len"])
	}
	if got["slice_huge_len"].String() != "éllo hello" {
		t.Fatalf("slice_huge_len mismatch: %q", got["slice_huge_len"].String())
	}
}

func TestStringTransforms(t *testing.T) {
	script := compileScript(t, `
    def helpers()
      original = "  hello  "
      ids = "ID-12 ID-34"
      {
        bytesize: "hé".bytesize,
        ord: "hé".ord,
        chr: "hé".chr,
        chr_empty: "".chr,
        capitalize: "hÉLLo wORLD".capitalize,
        capitalize_bang: "hÉLLo wORLD".capitalize!,
        capitalize_bang_nochange: "Hello".capitalize!,
        swapcase: "Hello VIBE".swapcase,
        swapcase_bang: "Hello VIBE".swapcase!,
        upcase_bang_nochange: "HELLO".upcase!,
        reverse: "héllo".reverse,
        reverse_bang: "héllo".reverse!,
        sub_one: "bananas".sub("na", "NA"),
        sub_bang: "bananas".sub!("na", "NA"),
        sub_bang_nochange: "bananas".sub!("zz", "NA"),
        sub_miss: "bananas".sub("zz", "NA"),
        sub_regex: ids.sub("ID-[0-9]+", "X", regex: true),
        sub_regex_capture: ids.sub("ID-([0-9]+)", "X-$1", regex: true),
        sub_regex_boundary_short: "ba".sub("\\Ba", "X", regex: true),
        sub_regex_boundary: "foo".sub("\\Boo", "X", regex: true),
        sub_regex_boundary_full: "xfooy".sub("\\Bfoo\\B", "X", regex: true),
        gsub_all: "bananas".gsub("na", "NA"),
        gsub_bang: "bananas".gsub!("na", "NA"),
        gsub_bang_nochange: "bananas".gsub!("zz", "NA"),
        gsub_regex: ids.gsub("ID-[0-9]+", "X", regex: true),
        match: ids.match("ID-([0-9]+)"),
        match_optional_nil: "ID".match("(ID)(-([0-9]+))?"),
        match_miss: ids.match("ZZZ"),
        scan: ids.scan("ID-[0-9]+"),
        clear: "hello".clear,
        concat: "he".concat("llo", "!"),
        concat_noop: "hello".concat,
        replace: "old".replace("new"),
        strip_bang: original.strip!,
        strip_bang_nochange: "hello".strip!,
        original_unchanged: original
      }
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if got["bytesize"].Int() != 3 {
		t.Fatalf("bytesize mismatch: %v", got["bytesize"])
	}
	if got["ord"].Int() != 104 {
		t.Fatalf("ord mismatch: %v", got["ord"])
	}
	if got["chr"].String() != "h" {
		t.Fatalf("chr mismatch: %q", got["chr"].String())
	}
	if got["chr_empty"].Kind() != KindNil {
		t.Fatalf("chr_empty expected nil, got %v", got["chr_empty"])
	}
	if got["capitalize"].String() != "Héllo world" {
		t.Fatalf("capitalize mismatch: %q", got["capitalize"].String())
	}
	if got["capitalize_bang"].String() != "Héllo world" {
		t.Fatalf("capitalize_bang mismatch: %q", got["capitalize_bang"].String())
	}
	if got["capitalize_bang_nochange"].Kind() != KindNil {
		t.Fatalf("capitalize_bang_nochange expected nil, got %v", got["capitalize_bang_nochange"])
	}
	if got["swapcase"].String() != "hELLO vibe" {
		t.Fatalf("swapcase mismatch: %q", got["swapcase"].String())
	}
	if got["swapcase_bang"].String() != "hELLO vibe" {
		t.Fatalf("swapcase_bang mismatch: %q", got["swapcase_bang"].String())
	}
	if got["upcase_bang_nochange"].Kind() != KindNil {
		t.Fatalf("upcase_bang_nochange expected nil, got %v", got["upcase_bang_nochange"])
	}
	if got["reverse"].String() != "olléh" {
		t.Fatalf("reverse mismatch: %q", got["reverse"].String())
	}
	if got["reverse_bang"].String() != "olléh" {
		t.Fatalf("reverse_bang mismatch: %q", got["reverse_bang"].String())
	}
	if got["sub_one"].String() != "baNAnas" {
		t.Fatalf("sub_one mismatch: %q", got["sub_one"].String())
	}
	if got["sub_bang"].String() != "baNAnas" {
		t.Fatalf("sub_bang mismatch: %q", got["sub_bang"].String())
	}
	if got["sub_bang_nochange"].Kind() != KindNil {
		t.Fatalf("sub_bang_nochange expected nil, got %v", got["sub_bang_nochange"])
	}
	if got["sub_miss"].String() != "bananas" {
		t.Fatalf("sub_miss mismatch: %q", got["sub_miss"].String())
	}
	if got["sub_regex"].String() != "X ID-34" {
		t.Fatalf("sub_regex mismatch: %q", got["sub_regex"].String())
	}
	if got["sub_regex_capture"].String() != "X-12 ID-34" {
		t.Fatalf("sub_regex_capture mismatch: %q", got["sub_regex_capture"].String())
	}
	if got["sub_regex_boundary_short"].String() != "bX" {
		t.Fatalf("sub_regex_boundary_short mismatch: %q", got["sub_regex_boundary_short"].String())
	}
	if got["sub_regex_boundary"].String() != "fX" {
		t.Fatalf("sub_regex_boundary mismatch: %q", got["sub_regex_boundary"].String())
	}
	if got["sub_regex_boundary_full"].String() != "xXy" {
		t.Fatalf("sub_regex_boundary_full mismatch: %q", got["sub_regex_boundary_full"].String())
	}
	if got["gsub_all"].String() != "baNANAs" {
		t.Fatalf("gsub_all mismatch: %q", got["gsub_all"].String())
	}
	if got["gsub_bang"].String() != "baNANAs" {
		t.Fatalf("gsub_bang mismatch: %q", got["gsub_bang"].String())
	}
	if got["gsub_bang_nochange"].Kind() != KindNil {
		t.Fatalf("gsub_bang_nochange expected nil, got %v", got["gsub_bang_nochange"])
	}
	if got["gsub_regex"].String() != "X X" {
		t.Fatalf("gsub_regex mismatch: %q", got["gsub_regex"].String())
	}
	compareArrays(t, got["match"], []Value{NewString("ID-12"), NewString("12")})
	compareArrays(t, got["match_optional_nil"], []Value{NewString("ID"), NewString("ID"), NewNil(), NewNil()})
	if got["match_miss"].Kind() != KindNil {
		t.Fatalf("match_miss expected nil, got %v", got["match_miss"])
	}
	compareArrays(t, got["scan"], []Value{NewString("ID-12"), NewString("ID-34")})
	if got["clear"].String() != "" {
		t.Fatalf("clear mismatch: %q", got["clear"].String())
	}
	if got["concat"].String() != "hello!" {
		t.Fatalf("concat mismatch: %q", got["concat"].String())
	}
	if got["concat_noop"].String() != "hello" {
		t.Fatalf("concat_noop mismatch: %q", got["concat_noop"].String())
	}
	if got["replace"].String() != "new" {
		t.Fatalf("replace mismatch: %q", got["replace"].String())
	}
	if got["strip_bang"].String() != "hello" {
		t.Fatalf("strip_bang mismatch: %q", got["strip_bang"].String())
	}
	if got["strip_bang_nochange"].Kind() != KindNil {
		t.Fatalf("strip_bang_nochange expected nil, got %v", got["strip_bang_nochange"])
	}
	if got["original_unchanged"].String() != "  hello  " {
		t.Fatalf("original_unchanged mismatch: %q", got["original_unchanged"].String())
	}
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
			name:   "array.find without block",
			script: `def run() [1, 2, 3].find end`,
			errMsg: "array.find requires a block",
		},
		{
			name:   "array.find_index with argument",
			script: `def run() [1, 2, 3].find_index(1) end`,
			errMsg: "array.find_index does not take arguments",
		},
		{
			name:   "array.index with invalid offset",
			script: `def run() [1, 2, 3].index(2, -1) end`,
			errMsg: "offset must be non-negative integer",
		},
		{
			name:   "array.rindex with too many args",
			script: `def run() [1, 2, 3].rindex(2, 1, 0) end`,
			errMsg: "expects value and optional offset",
		},
		{
			name:   "array.rindex validates offset on empty array",
			script: `def run() [].rindex(1, -1) end`,
			errMsg: "offset must be non-negative integer",
		},
		{
			name: "array.count with argument and block",
			script: `def run()
  [1, 1].count(1) do |v|
    v == 1
  end
end`,
			errMsg: "does not accept both argument and block",
		},
		{
			name:   "array.any? with argument",
			script: `def run() [1].any?(1) end`,
			errMsg: "array.any? does not take arguments",
		},
		{
			name:   "array.sort with incomparable values",
			script: `def run() [1, "a"].sort end`,
			errMsg: "values are not comparable",
		},
		{
			name: "array.sort with non-numeric comparator",
			script: `def run()
  [2, 1].sort do |a, b|
    a > b
  end
end`,
			errMsg: "block must return numeric comparator",
		},
		{
			name:   "array.sort_by without block",
			script: `def run() [1, 2].sort_by end`,
			errMsg: "array.sort_by requires a block",
		},
		{
			name: "array.sort_by with incomparable keys",
			script: `def run()
  [1, 2].sort_by do |v|
    if v == 1
      "one"
    else
      2
    end
  end
end`,
			errMsg: "block values are not comparable",
		},
		{
			name:   "array.partition without block",
			script: `def run() [1, 2].partition end`,
			errMsg: "array.partition requires a block",
		},
		{
			name: "array.group_by with unsupported group key",
			script: `def run()
  [1, 2].group_by do |v|
    v
  end
end`,
			errMsg: "block must return symbol or string",
		},
		{
			name:   "array.tally with unsupported values",
			script: `def run() [1, 2].tally end`,
			errMsg: "values must be symbol or string",
		},
		{
			name:   "array.tally with argument",
			script: `def run() ["a"].tally(1) end`,
			errMsg: "array.tally does not take arguments",
		},
		{
			name:   "string unknown method",
			script: `def run() "hello".unknown_method() end`,
			errMsg: "unknown string method",
		},
		{
			name:   "string.empty? with argument",
			script: `def run() "hello".empty?(1) end`,
			errMsg: "string.empty? does not take arguments",
		},
		{
			name:   "string.start_with? with non-string prefix",
			script: `def run() "hello".start_with?(123) end`,
			errMsg: "prefix must be string",
		},
		{
			name:   "string.end_with? with missing suffix",
			script: `def run() "hello".end_with? end`,
			errMsg: "expects exactly one suffix",
		},
		{
			name:   "string.lstrip with argument",
			script: `def run() " hello".lstrip(1) end`,
			errMsg: "string.lstrip does not take arguments",
		},
		{
			name:   "string.chomp with non-string separator",
			script: `def run() "line\n".chomp(123) end`,
			errMsg: "separator must be string",
		},
		{
			name:   "string.delete_prefix with non-string prefix",
			script: `def run() "hello".delete_prefix(123) end`,
			errMsg: "prefix must be string",
		},
		{
			name:   "string.delete_suffix with missing suffix",
			script: `def run() "hello".delete_suffix end`,
			errMsg: "expects exactly one suffix",
		},
		{
			name:   "string.include? with non-string substring",
			script: `def run() "hello".include?(123) end`,
			errMsg: "substring must be string",
		},
		{
			name:   "string.index with invalid offset",
			script: `def run() "hello".index("e", -1) end`,
			errMsg: "offset must be non-negative integer",
		},
		{
			name:   "string.rindex with too many args",
			script: `def run() "hello".rindex("l", 0, 1) end`,
			errMsg: "expects substring and optional offset",
		},
		{
			name:   "string.slice with non-int length",
			script: `def run() "hello".slice(1, "x") end`,
			errMsg: "length must be integer",
		},
		{
			name:   "string.capitalize with argument",
			script: `def run() "hello".capitalize(1) end`,
			errMsg: "string.capitalize does not take arguments",
		},
		{
			name:   "string.sub with non-string replacement",
			script: `def run() "hello".sub("l", 1) end`,
			errMsg: "replacement must be string",
		},
		{
			name:   "string.gsub with missing argument",
			script: `def run() "hello".gsub("l") end`,
			errMsg: "expects pattern and replacement",
		},
		{
			name:   "string.match with invalid regex",
			script: `def run() "hello".match("[") end`,
			errMsg: "invalid regex",
		},
		{
			name:   "string.scan with non-string pattern",
			script: `def run() "hello".scan(1) end`,
			errMsg: "pattern must be string",
		},
		{
			name:   "string.match with keyword argument",
			script: `def run() "hello".match("h", foo: true) end`,
			errMsg: "does not take keyword arguments",
		},
		{
			name:   "string.scan with keyword argument",
			script: `def run() "hello".scan("h", foo: true) end`,
			errMsg: "does not take keyword arguments",
		},
		{
			name:   "string.ord on empty string",
			script: `def run() "".ord end`,
			errMsg: "requires non-empty string",
		},
		{
			name:   "string.sub with non-bool regex keyword",
			script: `def run() "ID-12".sub("ID-[0-9]+", "X", regex: 1) end`,
			errMsg: "regex keyword must be bool",
		},
		{
			name:   "string.gsub with unknown keyword",
			script: `def run() "ID-12".gsub("ID-[0-9]+", "X", foo: true) end`,
			errMsg: "supports only regex keyword",
		},
		{
			name:   "string.concat with non-string argument",
			script: `def run() "hello".concat(1) end`,
			errMsg: "expects string arguments",
		},
		{
			name:   "string.replace with non-string replacement",
			script: `def run() "hello".replace(1) end`,
			errMsg: "replacement must be string",
		},
		{
			name:   "string.strip! with argument",
			script: `def run() "hello".strip!(1) end`,
			errMsg: "string.strip! does not take arguments",
		},
		{
			name:   "string.gsub! with missing argument",
			script: `def run() "hello".gsub!("l") end`,
			errMsg: "expects pattern and replacement",
		},
		{
			name:   "hash.size with argument",
			script: `def run() {a: 1}.size(1) end`,
			errMsg: "hash.size does not take arguments",
		},
		{
			name:   "hash.key? with unsupported key type",
			script: `def run() {a: 1}.key?(1) end`,
			errMsg: "key must be symbol or string",
		},
		{
			name:   "hash.fetch with too many args",
			script: `def run() {a: 1}.fetch(:a, 1, 2) end`,
			errMsg: "expects key and optional default",
		},
		{
			name:   "hash.dig without keys",
			script: `def run() {a: 1}.dig end`,
			errMsg: "expects at least one key",
		},
		{
			name:   "hash.dig with unsupported key type",
			script: `def run() {a: 1}.dig(1) end`,
			errMsg: "path keys must be symbol or string",
		},
		{
			name:   "hash.each without block",
			script: `def run() {a: 1}.each end`,
			errMsg: "hash.each requires a block",
		},
		{
			name:   "hash.select without block",
			script: `def run() {a: 1}.select end`,
			errMsg: "hash.select requires a block",
		},
		{
			name:   "hash.slice with unsupported key type",
			script: `def run() {a: 1}.slice(1) end`,
			errMsg: "keys must be symbol or string",
		},
		{
			name: "hash.transform_keys invalid return type",
			script: `def run()
  {a: 1}.transform_keys do |k|
    1
  end
end`,
			errMsg: "block must return symbol or string",
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
