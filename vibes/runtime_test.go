package vibes

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func compileScript(t *testing.T, source string) *Script {
	t.Helper()
	return compileScriptDefault(t, source)
}

func callFunc(t *testing.T, script *Script, name string, args []Value) Value {
	t.Helper()
	result, err := script.Call(context.Background(), name, args, CallOptions{})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return result
}

func TestCompileMalformedCallTargetDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("compile panicked: %v", r)
		}
	}()

	_ = compileScriptErrorDefault(t, `be(in (000000000`)
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
      grouped_stable = values.group_by_stable do |v|
        if v % 2 == 0
          :even
        else
          :odd
        end
      end
      chunked = [1, 2, 3, 4, 5].chunk(2)
      windowed = [1, 2, 3, 4].window(3)
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
        group_by_stable_parity: grouped_stable,
        chunked: chunked,
        windowed: windowed,
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

	stableGrouped := got["group_by_stable_parity"]
	if stableGrouped.Kind() != KindArray {
		t.Fatalf("group_by_stable_parity expected array, got %v", stableGrouped.Kind())
	}
	stablePairs := stableGrouped.Array()
	if len(stablePairs) != 2 {
		t.Fatalf("group_by_stable_parity length mismatch: %d", len(stablePairs))
	}
	firstPair := stablePairs[0]
	if firstPair.Kind() != KindArray || len(firstPair.Array()) != 2 {
		t.Fatalf("group_by_stable first pair mismatch: %v", firstPair)
	}
	if !firstPair.Array()[0].Equal(NewSymbol("odd")) {
		t.Fatalf("group_by_stable first key mismatch: %v", firstPair.Array()[0])
	}
	compareArrays(t, firstPair.Array()[1], []Value{NewInt(3), NewInt(1), NewInt(1)})
	secondPair := stablePairs[1]
	if secondPair.Kind() != KindArray || len(secondPair.Array()) != 2 {
		t.Fatalf("group_by_stable second pair mismatch: %v", secondPair)
	}
	if !secondPair.Array()[0].Equal(NewSymbol("even")) {
		t.Fatalf("group_by_stable second key mismatch: %v", secondPair.Array()[0])
	}
	compareArrays(t, secondPair.Array()[1], []Value{NewInt(2)})

	chunked := got["chunked"]
	if chunked.Kind() != KindArray {
		t.Fatalf("chunked expected array, got %v", chunked.Kind())
	}
	chunks := chunked.Array()
	if len(chunks) != 3 {
		t.Fatalf("chunked length mismatch: %d", len(chunks))
	}
	compareArrays(t, chunks[0], []Value{NewInt(1), NewInt(2)})
	compareArrays(t, chunks[1], []Value{NewInt(3), NewInt(4)})
	compareArrays(t, chunks[2], []Value{NewInt(5)})

	windowed := got["windowed"]
	if windowed.Kind() != KindArray {
		t.Fatalf("windowed expected array, got %v", windowed.Kind())
	}
	windows := windowed.Array()
	if len(windows) != 2 {
		t.Fatalf("windowed length mismatch: %d", len(windows))
	}
	compareArrays(t, windows[0], []Value{NewInt(1), NewInt(2), NewInt(3)})
	compareArrays(t, windows[1], []Value{NewInt(2), NewInt(3), NewInt(4)})

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

func TestArrayChunkWindowValidation(t *testing.T) {
	script := compileScript(t, `
    def bad_chunk()
      [1, 2, 3].chunk(0)
    end

    def huge_chunk(size)
      [1, 2].chunk(size)
    end

    def huge_window(size)
      [1, 2, 3].window(size)
    end

    def bad_window()
      [1, 2, 3].window("2")
    end

    def bad_group_by_stable()
      [1, 2, 3].group_by_stable
    end
    `)

	_, err := script.Call(context.Background(), "bad_chunk", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "array.chunk size must be a positive integer") {
		t.Fatalf("expected chunk validation error, got %v", err)
	}
	nativeMaxInt := int64(^uint(0) >> 1)
	hugeChunk := callFunc(t, script, "huge_chunk", []Value{NewInt(nativeMaxInt)})
	if hugeChunk.Kind() != KindArray {
		t.Fatalf("expected huge chunk result to be array, got %v", hugeChunk.Kind())
	}
	chunks := hugeChunk.Array()
	if len(chunks) != 1 {
		t.Fatalf("expected one chunk for oversized chunk size, got %d", len(chunks))
	}
	compareArrays(t, chunks[0], []Value{NewInt(1), NewInt(2)})
	_, err = script.Call(context.Background(), "bad_window", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "array.window size must be a positive integer") {
		t.Fatalf("expected window validation error, got %v", err)
	}
	hugeWindow := callFunc(t, script, "huge_window", []Value{NewInt(nativeMaxInt)})
	if hugeWindow.Kind() != KindArray || len(hugeWindow.Array()) != 0 {
		t.Fatalf("expected huge window size to return empty array, got %v", hugeWindow)
	}

	overflowSize := int64(1 << 62)
	if nativeMaxInt < overflowSize {
		_, err = script.Call(context.Background(), "huge_chunk", []Value{NewInt(overflowSize)}, CallOptions{})
		if err == nil || !strings.Contains(err.Error(), "array.chunk size must be a positive integer") {
			t.Fatalf("expected chunk overflow validation error, got %v", err)
		}
		_, err = script.Call(context.Background(), "huge_window", []Value{NewInt(overflowSize)}, CallOptions{})
		if err == nil || !strings.Contains(err.Error(), "array.window size must be a positive integer") {
			t.Fatalf("expected window overflow validation error, got %v", err)
		}
	}
	_, err = script.Call(context.Background(), "bad_group_by_stable", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "array.group_by_stable requires a block") {
		t.Fatalf("expected group_by_stable block error, got %v", err)
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
	_ = compileScriptErrorDefault(t, `
    def broken()
      { "name" => "alex" }
    end
    `)
}

func TestParseErrorIncludesCodeFrameAndKeywordMessage(t *testing.T) {
	err := compileScriptErrorDefault(t, "def broken()\n  call(foo: )\nend\n")
	msg := err.Error()
	if !strings.Contains(msg, "missing value for keyword argument foo") {
		t.Fatalf("expected keyword argument parse error, got: %s", msg)
	}
	if !strings.Contains(msg, "--> line 2, column") {
		t.Fatalf("expected codeframe line marker, got: %s", msg)
	}
	if !strings.Contains(msg, "call(foo: )") {
		t.Fatalf("expected source line in codeframe, got: %s", msg)
	}
}

func TestReservedWordLabelsInHashesAndCallKwargs(t *testing.T) {
	script := compileScript(t, `
    def hash_payload(cursor)
      { next: cursor, break: cursor + 1 }
    end

    def call_payload(cursor)
      list(next: cursor, break: cursor + 1)
    end
    `)

	payload := callFunc(t, script, "hash_payload", []Value{NewInt(7)})
	if payload.Kind() != KindHash {
		t.Fatalf("expected hash result, got %v", payload.Kind())
	}
	compareHash(t, payload.Hash(), map[string]Value{
		"next":  NewInt(7),
		"break": NewInt(8),
	})
}

func TestParseErrorIncludesBlockParameterHint(t *testing.T) {
	err := compileScriptErrorDefault(t, "def broken()\n  [1].each do |a,|\n    a\n  end\nend\n")
	msg := err.Error()
	if !strings.Contains(msg, "trailing comma in block parameter list") {
		t.Fatalf("expected trailing comma hint, got: %s", msg)
	}
}

func TestTypedBlockSignatures(t *testing.T) {
	script := compileScript(t, `
    def increment_all(values)
      values.map do |n: int|
        n + 1
      end
    end

    def typed_union(values)
      values.map do |v: int | string|
        v
      end
    end

    def call_with_block(value)
      yield value
    end

    def enforce_yield_type(value)
      call_with_block(value) do |n: int|
        n
      end
    end

    def passthrough(values)
      values.map do |v|
        v
      end
    end
    `)

	inc := callFunc(t, script, "increment_all", []Value{
		NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
	})
	compareArrays(t, inc, []Value{NewInt(2), NewInt(3), NewInt(4)})

	unionResult := callFunc(t, script, "typed_union", []Value{
		NewArray([]Value{NewInt(7), NewString("ok")}),
	})
	compareArrays(t, unionResult, []Value{NewInt(7), NewString("ok")})

	if got := callFunc(t, script, "enforce_yield_type", []Value{NewInt(9)}); !got.Equal(NewInt(9)) {
		t.Fatalf("typed yield block mismatch: got %v", got)
	}

	untouched := callFunc(t, script, "passthrough", []Value{
		NewArray([]Value{NewInt(1), NewString("two")}),
	})
	compareArrays(t, untouched, []Value{NewInt(1), NewString("two")})

	_, err := script.Call(context.Background(), "increment_all", []Value{
		NewArray([]Value{NewInt(1), NewString("oops")}),
	}, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "argument n expected int, got string") {
		t.Fatalf("expected typed block argument error, got %v", err)
	}

	_, err = script.Call(context.Background(), "typed_union", []Value{
		NewArray([]Value{NewBool(true)}),
	}, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "argument v expected int | string, got bool") {
		t.Fatalf("expected typed union block argument error, got %v", err)
	}

	_, err = script.Call(context.Background(), "enforce_yield_type", []Value{NewString("bad")}, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "argument n expected int, got string") {
		t.Fatalf("expected typed yield argument error, got %v", err)
	}
}

func TestArraySumRejectsNonNumeric(t *testing.T) {
	script := compileScriptDefault(t, `
    def bad()
      ["a"].sum()
    end
    `)

	var err error
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
	if rtErr.CodeFrame == "" {
		t.Fatalf("expected runtime codeframe to be present")
	}
	if !strings.Contains(rtErr.Error(), "--> line") {
		t.Fatalf("expected formatted runtime error to include codeframe marker")
	}
}

func TestRuntimeErrorCondensesDeepStackRendering(t *testing.T) {
	script := compileScriptWithConfig(t, Config{RecursionLimit: 128}, `
    def recurse(n)
      if n <= 0
        1 / 0
      end
      recurse(n - 1)
    end

    def run()
      recurse(40)
    end
    `)

	var err error
	_, err = script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected runtime error")
	}

	var rtErr *RuntimeError
	if !errors.As(err, &rtErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if len(rtErr.Frames) <= 16 {
		t.Fatalf("expected deep frame set, got %d", len(rtErr.Frames))
	}
	rendered := rtErr.Error()
	if !strings.Contains(rendered, "frames omitted") {
		t.Fatalf("expected deep stack output to include omitted-frame marker: %s", rendered)
	}
	if !strings.Contains(rendered, "at recurse") {
		t.Fatalf("expected deep stack output to include recurse frames: %s", rendered)
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

func TestWhileLoops(t *testing.T) {
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

	engine := MustNewEngine(Config{StepQuota: 40})
	spinScript, err := engine.Compile(`
    def spin()
      while true
      end
    end
    `)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	_, err = spinScript.Call(context.Background(), "spin", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "step quota exceeded") {
		t.Fatalf("expected step quota error for infinite while loop, got %v", err)
	}
}

func TestUntilLoops(t *testing.T) {
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

	engine := MustNewEngine(Config{StepQuota: 40})
	spinScript, err := engine.Compile(`
    def spin_until()
      until false
      end
    end
    `)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	_, err = spinScript.Call(context.Background(), "spin_until", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "step quota exceeded") {
		t.Fatalf("expected step quota error for infinite until loop, got %v", err)
	}
}

func TestCaseWhenExpressions(t *testing.T) {
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
    `)

	if got := callFunc(t, script, "label", []Value{NewInt(100)}); !got.Equal(NewString("perfect")) {
		t.Fatalf("label(100) mismatch: %v", got)
	}
	if got := callFunc(t, script, "label", []Value{NewInt(95)}); !got.Equal(NewString("great")) {
		t.Fatalf("label(95) mismatch: %v", got)
	}
	if got := callFunc(t, script, "label", []Value{NewInt(70)}); !got.Equal(NewString("ok")) {
		t.Fatalf("label(70) mismatch: %v", got)
	}

	if got := callFunc(t, script, "classify", []Value{NewNil()}); !got.Equal(NewString("missing")) {
		t.Fatalf("classify(nil) mismatch: %v", got)
	}
	if got := callFunc(t, script, "classify", []Value{NewBool(true)}); !got.Equal(NewString("yes")) {
		t.Fatalf("classify(true) mismatch: %v", got)
	}
	if got := callFunc(t, script, "classify", []Value{NewInt(1)}); !got.Equal(NewString("other")) {
		t.Fatalf("classify(1) mismatch: %v", got)
	}

	if got := callFunc(t, script, "assign_case", []Value{NewInt(1)}); !got.Equal(NewInt(10)) {
		t.Fatalf("assign_case(1) mismatch: %v", got)
	}
	if got := callFunc(t, script, "assign_case", []Value{NewInt(2)}); !got.Equal(NewInt(20)) {
		t.Fatalf("assign_case(2) mismatch: %v", got)
	}

	if got := callFunc(t, script, "unmatched", []Value{NewInt(7)}); !got.Equal(NewNil()) {
		t.Fatalf("unmatched(7) expected nil, got %v", got)
	}
}

func TestBeginRescueEnsure(t *testing.T) {
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

	if got := callFunc(t, script, "safe_div", []Value{NewInt(10), NewInt(2)}); !got.Equal(NewFloat(5)) {
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

	_, err := script.Call(context.Background(), "ensure_without_rescue", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "division by zero") {
		t.Fatalf("expected ensure_without_rescue to preserve original error, got %v", err)
	}
}

func TestBeginRescueTypedMatching(t *testing.T) {
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

	_, err := script.Call(context.Background(), "rescue_mismatch", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "division by zero") {
		t.Fatalf("expected typed rescue mismatch to preserve original error, got %v", err)
	}
	var divideErr *RuntimeError
	if !errors.As(err, &divideErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if divideErr.Type != runtimeErrorTypeBase {
		t.Fatalf("expected runtime error type %s, got %s", runtimeErrorTypeBase, divideErr.Type)
	}

	_, err = script.Call(context.Background(), "assertion_passthrough", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "raw") {
		t.Fatalf("expected assertion passthrough error, got %v", err)
	}
	var assertionErr *RuntimeError
	if !errors.As(err, &assertionErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if assertionErr.Type != runtimeErrorTypeAssertion {
		t.Fatalf("expected runtime error type %s, got %s", runtimeErrorTypeAssertion, assertionErr.Type)
	}
}

func TestBeginRescueDoesNotCatchLoopControlSignals(t *testing.T) {
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

	var err error
	_, err = script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "step quota exceeded") {
		t.Fatalf("expected host quota signal to bypass rescue, got %v", err)
	}
}

func TestBeginRescueTypedUnknownTypeFailsCompile(t *testing.T) {
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

	_, err := script.Call(context.Background(), "outer", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected reraise error, got %v", err)
	}
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

	_, err = script.Call(context.Background(), "raise_outside", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "raise used outside of rescue") {
		t.Fatalf("expected raise outside rescue error, got %v", err)
	}

	_, err = script.Call(context.Background(), "raise_new_message", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "custom boom") {
		t.Fatalf("expected raise message error, got %v", err)
	}
	var raisedErr *RuntimeError
	if !errors.As(err, &raisedErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if raisedErr.Type != runtimeErrorTypeBase {
		t.Fatalf("expected runtime error type %s, got %s", runtimeErrorTypeBase, raisedErr.Type)
	}
}

func TestLoopControlBreakAndNext(t *testing.T) {
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

	_, err := script.Call(context.Background(), "break_outside", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "break used outside of loop") {
		t.Fatalf("expected outside-loop break error, got %v", err)
	}

	_, err = script.Call(context.Background(), "next_outside", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "next used outside of loop") {
		t.Fatalf("expected outside-loop next error, got %v", err)
	}
}

func TestLoopControlNestedAndBlockBoundaryBehavior(t *testing.T) {
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

	_, err := script.Call(context.Background(), "break_from_block_boundary", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "break cannot cross call boundary") {
		t.Fatalf("expected block-boundary break error, got %v", err)
	}

	_, err = script.Call(context.Background(), "next_from_block_boundary", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "next cannot cross call boundary") {
		t.Fatalf("expected block-boundary next error, got %v", err)
	}

	_, err = script.Call(context.Background(), "break_from_setter_boundary", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "break cannot cross call boundary") {
		t.Fatalf("expected setter-boundary break error, got %v", err)
	}

	_, err = script.Call(context.Background(), "next_from_setter_boundary", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "next cannot cross call boundary") {
		t.Fatalf("expected setter-boundary next error, got %v", err)
	}
}

func TestLoopControlInsideClassMethods(t *testing.T) {
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

func TestTimeParseAndAliases(t *testing.T) {
	script := compileScript(t, `
	    def helpers()
	      default = Time.parse("2024-01-02T03:04:05Z")
	      default_nil_layout = Time.parse("2024-01-02T03:04:05Z", nil)
	      custom = Time.parse("2024-01-02 03:04:05", "2006-01-02 15:04:05", in: "America/New_York")
	      {
	        default_to_s: default.to_s,
	        default_nil_layout_to_s: default_nil_layout.to_s,
	        default_iso: default.iso8601,
	        default_rfc3339: default.rfc3339,
	        custom_utc_offset: custom.utc_offset,
        custom_utc: custom.utc.to_s
      }
    end

    def parse_invalid()
      Time.parse("not-a-time")
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if got["default_to_s"].String() != "2024-01-02T03:04:05Z" {
		t.Fatalf("default_to_s mismatch: %q", got["default_to_s"].String())
	}
	if got["default_nil_layout_to_s"].String() != "2024-01-02T03:04:05Z" {
		t.Fatalf("default_nil_layout_to_s mismatch: %q", got["default_nil_layout_to_s"].String())
	}
	if got["default_iso"].String() != "2024-01-02T03:04:05Z" {
		t.Fatalf("default_iso mismatch: %q", got["default_iso"].String())
	}
	if got["default_rfc3339"].String() != "2024-01-02T03:04:05Z" {
		t.Fatalf("default_rfc3339 mismatch: %q", got["default_rfc3339"].String())
	}
	if got["custom_utc_offset"].Int() != -18000 {
		t.Fatalf("custom_utc_offset mismatch: %v", got["custom_utc_offset"])
	}
	if got["custom_utc"].String() != "2024-01-02T08:04:05Z" {
		t.Fatalf("custom_utc mismatch: %q", got["custom_utc"].String())
	}

	_, err := script.Call(context.Background(), "parse_invalid", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected parse error")
	}
	if !strings.Contains(err.Error(), "could not parse time") {
		t.Fatalf("unexpected parse error: %v", err)
	}
}

func TestTimeParseCommonLayouts(t *testing.T) {
	script := compileScript(t, `
    def parse_common()
      {
        slash_date: Time.parse("2024/01/02", in: "UTC").to_s,
        slash_datetime: Time.parse("2024/01/02 03:04:05", in: "UTC").to_s,
        us_date: Time.parse("01/02/2024", in: "UTC").to_s,
        us_datetime: Time.parse("01/02/2024 03:04:05", in: "UTC").to_s,
        iso_no_zone: Time.parse("2024-01-02T03:04:05", in: "UTC").to_s,
        rfc1123: Time.parse("Tue, 02 Jan 2024 03:04:05 UTC").to_s
      }
    end
    `)

	result := callFunc(t, script, "parse_common", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	expect := map[string]string{
		"slash_date":     "2024-01-02T00:00:00Z",
		"slash_datetime": "2024-01-02T03:04:05Z",
		"us_date":        "2024-01-02T00:00:00Z",
		"us_datetime":    "2024-01-02T03:04:05Z",
		"iso_no_zone":    "2024-01-02T03:04:05Z",
		"rfc1123":        "2024-01-02T03:04:05Z",
	}
	for key, want := range expect {
		val, ok := got[key]
		if !ok {
			t.Fatalf("missing key %s", key)
		}
		if val.Kind() != KindString || val.String() != want {
			t.Fatalf("%s mismatch: got %v want %s", key, val, want)
		}
	}
}

func TestNumericConversionBuiltins(t *testing.T) {
	script := compileScript(t, `
    def conversions()
      {
        int_from_int: to_int(5),
        int_from_float: to_int(5.0),
        int_from_string: to_int("42"),
        float_from_int: to_float(5),
        float_from_float: to_float(1.25),
        float_from_string: to_float("2.5")
      }
    end

    def bad_int_fraction()
      to_int(1.5)
    end

    def bad_int_string()
      to_int("abc")
    end

	    def bad_float_string()
	      to_float("abc")
	    end

	    def bad_float_nan()
	      to_float("NaN")
	    end

	    def bad_float_inf()
	      to_float("Inf")
	    end
	    `)

	result := callFunc(t, script, "conversions", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if !got["int_from_int"].Equal(NewInt(5)) || !got["int_from_float"].Equal(NewInt(5)) || !got["int_from_string"].Equal(NewInt(42)) {
		t.Fatalf("to_int conversions mismatch: %#v", got)
	}
	if got["float_from_int"].Kind() != KindFloat || got["float_from_int"].Float() != 5 {
		t.Fatalf("float_from_int mismatch: %v", got["float_from_int"])
	}
	if got["float_from_float"].Kind() != KindFloat || got["float_from_float"].Float() != 1.25 {
		t.Fatalf("float_from_float mismatch: %v", got["float_from_float"])
	}
	if got["float_from_string"].Kind() != KindFloat || got["float_from_string"].Float() != 2.5 {
		t.Fatalf("float_from_string mismatch: %v", got["float_from_string"])
	}

	_, err := script.Call(context.Background(), "bad_int_fraction", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "to_int cannot convert non-integer float") {
		t.Fatalf("expected fractional to_int error, got %v", err)
	}
	_, err = script.Call(context.Background(), "bad_int_string", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "to_int expects a base-10 integer string") {
		t.Fatalf("expected string to_int error, got %v", err)
	}
	_, err = script.Call(context.Background(), "bad_float_string", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "to_float expects a numeric string") {
		t.Fatalf("expected string to_float error, got %v", err)
	}
	_, err = script.Call(context.Background(), "bad_float_nan", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "to_float expects a finite numeric string") {
		t.Fatalf("expected NaN to_float error, got %v", err)
	}
	_, err = script.Call(context.Background(), "bad_float_inf", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "to_float expects a finite numeric string") {
		t.Fatalf("expected Inf to_float error, got %v", err)
	}
}

func TestJSONBuiltins(t *testing.T) {
	script := compileScript(t, `
    def parse_payload()
      JSON.parse("{\"name\":\"alex\",\"score\":10,\"tags\":[\"x\",true,null],\"ratio\":1.5}")
    end

    def stringify_payload()
      payload = { name: "alex", score: 10, tags: ["x", true, nil], ratio: 1.5 }
      JSON.stringify(payload)
    end

    def parse_invalid()
      JSON.parse("{bad")
    end

    def stringify_unsupported()
      JSON.stringify({ fn: helper })
    end

    def helper(value)
      value
    end
    `)

	parsed := callFunc(t, script, "parse_payload", nil)
	if parsed.Kind() != KindHash {
		t.Fatalf("expected parsed payload hash, got %v", parsed.Kind())
	}
	obj := parsed.Hash()
	if !obj["name"].Equal(NewString("alex")) {
		t.Fatalf("name mismatch: %v", obj["name"])
	}
	if !obj["score"].Equal(NewInt(10)) {
		t.Fatalf("score mismatch: %v", obj["score"])
	}
	if obj["ratio"].Kind() != KindFloat || obj["ratio"].Float() != 1.5 {
		t.Fatalf("ratio mismatch: %v", obj["ratio"])
	}
	compareArrays(t, obj["tags"], []Value{NewString("x"), NewBool(true), NewNil()})

	stringified := callFunc(t, script, "stringify_payload", nil)
	if stringified.Kind() != KindString {
		t.Fatalf("expected JSON.stringify to return string, got %v", stringified.Kind())
	}
	if got := stringified.String(); got != `{"name":"alex","ratio":1.5,"score":10,"tags":["x",true,null]}` {
		t.Fatalf("stringify mismatch: %q", got)
	}

	_, err := script.Call(context.Background(), "parse_invalid", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "JSON.parse invalid JSON") {
		t.Fatalf("expected parse invalid JSON error, got %v", err)
	}

	_, err = script.Call(context.Background(), "stringify_unsupported", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "JSON.stringify unsupported value type function") {
		t.Fatalf("expected stringify unsupported error, got %v", err)
	}
}

func TestRegexBuiltins(t *testing.T) {
	script := compileScript(t, `
	    def helpers()
	      {
	        match_hit: Regex.match("ID-[0-9]+", "ID-12 ID-34"),
	        match_miss: Regex.match("Z+", "ID-12"),
	        match_empty: Regex.match("^", "ID-12"),
	        replace_one: Regex.replace("ID-12 ID-34", "ID-[0-9]+", "X"),
	        replace_all: Regex.replace_all("ID-12 ID-34", "ID-[0-9]+", "X"),
	        replace_all_adjacent: Regex.replace_all("aaaa", "aa", "X"),
	        replace_all_anchor: Regex.replace_all("abc", "^", "X"),
	        replace_all_boundary: Regex.replace_all("ab", "\\b", "X"),
	        replace_all_abutting_empty: Regex.replace_all("aa", "aa|", "X"),
	        replace_capture: Regex.replace("ID-12 ID-34", "ID-([0-9]+)", "X-$1"),
	        replace_boundary: Regex.replace("ab", "\\Bb", "X")
	      }
    end

    def invalid_regex()
      Regex.match("[", "abc")
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	out := result.Hash()
	if !out["match_hit"].Equal(NewString("ID-12")) {
		t.Fatalf("match_hit mismatch: %v", out["match_hit"])
	}
	if out["match_miss"].Kind() != KindNil {
		t.Fatalf("expected match_miss nil, got %v", out["match_miss"])
	}
	if !out["match_empty"].Equal(NewString("")) {
		t.Fatalf("match_empty mismatch: %#v", out["match_empty"])
	}
	if !out["replace_one"].Equal(NewString("X ID-34")) {
		t.Fatalf("replace_one mismatch: %v", out["replace_one"])
	}
	if !out["replace_all"].Equal(NewString("X X")) {
		t.Fatalf("replace_all mismatch: %v", out["replace_all"])
	}
	if !out["replace_all_adjacent"].Equal(NewString("XX")) {
		t.Fatalf("replace_all_adjacent mismatch: %v", out["replace_all_adjacent"])
	}
	if !out["replace_all_anchor"].Equal(NewString("Xabc")) {
		t.Fatalf("replace_all_anchor mismatch: %v", out["replace_all_anchor"])
	}
	if !out["replace_all_boundary"].Equal(NewString("XabX")) {
		t.Fatalf("replace_all_boundary mismatch: %v", out["replace_all_boundary"])
	}
	if !out["replace_all_abutting_empty"].Equal(NewString("X")) {
		t.Fatalf("replace_all_abutting_empty mismatch: %v", out["replace_all_abutting_empty"])
	}
	if !out["replace_capture"].Equal(NewString("X-12 ID-34")) {
		t.Fatalf("replace_capture mismatch: %v", out["replace_capture"])
	}
	if !out["replace_boundary"].Equal(NewString("aX")) {
		t.Fatalf("replace_boundary mismatch: %v", out["replace_boundary"])
	}

	_, err := script.Call(context.Background(), "invalid_regex", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "Regex.match invalid regex") {
		t.Fatalf("expected invalid regex error, got %v", err)
	}
}

func TestJSONAndRegexMalformedInputs(t *testing.T) {
	script := compileScript(t, `
    def bad_json_trailing()
      JSON.parse("{\"a\":1}{\"b\":2}")
    end

    def bad_json_syntax()
      JSON.parse("{\"a\":")
    end

    def bad_regex_replace()
      Regex.replace("abc", "[", "x")
    end

    def bad_regex_replace_all()
      Regex.replace_all("abc", "[", "x")
    end
    `)

	_, err := script.Call(context.Background(), "bad_json_trailing", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "JSON.parse invalid JSON: trailing data") {
		t.Fatalf("expected trailing JSON error, got %v", err)
	}
	_, err = script.Call(context.Background(), "bad_json_syntax", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "JSON.parse invalid JSON") {
		t.Fatalf("expected malformed JSON syntax error, got %v", err)
	}
	_, err = script.Call(context.Background(), "bad_regex_replace", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "Regex.replace invalid regex") {
		t.Fatalf("expected regex replace error, got %v", err)
	}
	_, err = script.Call(context.Background(), "bad_regex_replace_all", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "Regex.replace_all invalid regex") {
		t.Fatalf("expected regex replace_all error, got %v", err)
	}
}

func TestJSONAndRegexSizeGuards(t *testing.T) {
	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 4 << 20}, `
    def parse_raw(raw)
      JSON.parse(raw)
    end

    def stringify_value(value)
      JSON.stringify(value)
    end

    def regex_match_guard(pattern, text)
      Regex.match(pattern, text)
    end

    def regex_replace_all_guard(text, pattern, replacement)
      Regex.replace_all(text, pattern, replacement)
    end
    `)

	largeJSON := `{"data":"` + strings.Repeat("x", maxJSONPayloadBytes) + `"}`
	var err error
	_, err = script.Call(context.Background(), "parse_raw", []Value{NewString(largeJSON)}, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "JSON.parse input exceeds limit") {
		t.Fatalf("expected JSON.parse size guard error, got %v", err)
	}

	largeValue := NewHash(map[string]Value{
		"data": NewString(strings.Repeat("x", maxJSONPayloadBytes)),
	})
	_, err = script.Call(context.Background(), "stringify_value", []Value{largeValue}, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "JSON.stringify output exceeds limit") {
		t.Fatalf("expected JSON.stringify size guard error, got %v", err)
	}

	largePattern := strings.Repeat("a", maxRegexPatternSize+1)
	_, err = script.Call(context.Background(), "regex_match_guard", []Value{NewString(largePattern), NewString("aaa")}, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "Regex.match pattern exceeds limit") {
		t.Fatalf("expected Regex.match pattern guard error, got %v", err)
	}

	largeText := strings.Repeat("a", maxRegexInputBytes+1)
	_, err = script.Call(context.Background(), "regex_match_guard", []Value{NewString("a+"), NewString(largeText)}, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "Regex.match text exceeds limit") {
		t.Fatalf("expected Regex.match text guard error, got %v", err)
	}

	hugeReplacement := strings.Repeat("x", maxRegexInputBytes/2)
	_, err = script.Call(
		context.Background(),
		"regex_replace_all_guard",
		[]Value{NewString("abc"), NewString(""), NewString(hugeReplacement)},
		CallOptions{},
	)
	if err == nil || !strings.Contains(err.Error(), "Regex.replace_all output exceeds limit") {
		t.Fatalf("expected Regex.replace_all output guard error, got %v", err)
	}

	largeRun := strings.Repeat("a", maxRegexInputBytes-1024)
	replaced, err := script.Call(
		context.Background(),
		"regex_replace_all_guard",
		[]Value{NewString(largeRun), NewString("(a)[a]*"), NewString("$1$1")},
		CallOptions{},
	)
	if err != nil {
		t.Fatalf("expected large capture replacement to succeed, got %v", err)
	}
	if replaced.Kind() != KindString || replaced.String() != "aa" {
		t.Fatalf("expected capture replacement to produce \"aa\", got %v", replaced)
	}
}

func TestLocaleSensitiveOperationsDeterministic(t *testing.T) {
	script := compileScript(t, `
    def locale_ops()
      {
        up_i: "i".upcase,
        down_i_dot: "İ".downcase,
        sorted_words: ["ä", "z", "a"].sort,
        sorted_case: ["b", "A", "a"].sort
      }
    end
    `)

	result := callFunc(t, script, "locale_ops", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	out := result.Hash()
	if !out["up_i"].Equal(NewString("I")) {
		t.Fatalf("up_i mismatch: %v", out["up_i"])
	}
	if !out["down_i_dot"].Equal(NewString("i")) {
		t.Fatalf("down_i_dot mismatch: %v", out["down_i_dot"])
	}
	compareArrays(t, out["sorted_words"], []Value{NewString("a"), NewString("z"), NewString("ä")})
	compareArrays(t, out["sorted_case"], []Value{NewString("A"), NewString("a"), NewString("b")})
}

func TestRandomIdentifierBuiltins(t *testing.T) {
	script := compileScriptWithConfig(t, Config{
		RandomReader: bytes.NewReader(bytes.Repeat([]byte{0xAB}, 128)),
	}, `
    def values()
      {
        uuid: uuid(),
        id8: random_id(8),
        id_default: random_id()
      }
    end

	    def bad_length_type()
	      random_id("x")
	    end

	    def bad_length_float()
	      random_id(8.9)
	    end

	    def bad_length_value()
	      random_id(0)
	    end

    def bad_uuid_args()
      uuid(1)
    end
    `)

	result, err := script.Call(context.Background(), "values", nil, CallOptions{})
	if err != nil {
		t.Fatalf("values call failed: %v", err)
	}
	if result.Kind() != KindHash {
		t.Fatalf("expected hash result, got %v", result.Kind())
	}
	out := result.Hash()
	uuidValue := out["uuid"]
	if uuidValue.Kind() != KindString {
		t.Fatalf("uuid should be string, got %v", uuidValue)
	}
	uuidText := uuidValue.String()
	if len(uuidText) != 36 {
		t.Fatalf("uuid length mismatch: %q", uuidText)
	}
	if uuidText[8] != '-' || uuidText[13] != '-' || uuidText[18] != '-' || uuidText[23] != '-' {
		t.Fatalf("uuid separator mismatch: %q", uuidText)
	}
	if uuidText[14] != '7' {
		t.Fatalf("uuid version mismatch: %q", uuidText)
	}
	if !strings.HasSuffix(uuidText, "-7bab-abab-abababababab") {
		t.Fatalf("uuid random suffix mismatch: %q", uuidText)
	}
	if !out["id8"].Equal(NewString("VVVVVVVV")) {
		t.Fatalf("id8 mismatch: %v", out["id8"])
	}
	if got := out["id_default"]; got.Kind() != KindString || got.String() != "VVVVVVVVVVVVVVVV" {
		t.Fatalf("id_default mismatch: %v", got)
	}

	if _, err := script.Call(context.Background(), "bad_length_type", nil, CallOptions{}); err == nil || !strings.Contains(err.Error(), "random_id length must be integer") {
		t.Fatalf("expected length type error, got %v", err)
	}
	if _, err := script.Call(context.Background(), "bad_length_float", nil, CallOptions{}); err == nil || !strings.Contains(err.Error(), "random_id length must be integer") {
		t.Fatalf("expected length float error, got %v", err)
	}
	if _, err := script.Call(context.Background(), "bad_length_value", nil, CallOptions{}); err == nil || !strings.Contains(err.Error(), "random_id length must be positive") {
		t.Fatalf("expected length value error, got %v", err)
	}
	if _, err := script.Call(context.Background(), "bad_uuid_args", nil, CallOptions{}); err == nil || !strings.Contains(err.Error(), "uuid does not take arguments") {
		t.Fatalf("expected uuid args error, got %v", err)
	}
}

func TestRandomIdentifierBuiltinsRandomSourceFailure(t *testing.T) {
	script := compileScriptWithConfig(t, Config{RandomReader: bytes.NewReader([]byte{1, 2, 3})}, `
    def run()
      uuid()
    end
    `)

	var err error
	_, err = script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "random source failed") {
		t.Fatalf("expected random source failure, got %v", err)
	}
}

func TestRandomIdentifierBuiltinsUsesUnbiasedSampling(t *testing.T) {
	script := compileScriptWithConfig(t, Config{RandomReader: bytes.NewReader([]byte{248, 1})}, `
    def run()
      random_id(1)
    end
    `)

	var err error
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if !got.Equal(NewString("b")) {
		t.Fatalf("expected unbiased sample to produce b, got %v", got)
	}
}

func TestRandomIdentifierBuiltinsRejectsStalledEntropy(t *testing.T) {
	script := compileScriptWithConfig(t, Config{RandomReader: bytes.NewReader(bytes.Repeat([]byte{0xFF}, 1024))}, `
    def run()
      random_id(4)
    end
    `)

	var err error
	_, err = script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "random_id entropy source rejected too many bytes") {
		t.Fatalf("expected stalled entropy error, got %v", err)
	}
}

func TestNumericHelpers(t *testing.T) {
	script := compileScript(t, `
    def int_helpers()
      {
        abs_neg: (-7).abs,
        abs_pos: 7.abs,
        clamp_mid: 7.clamp(1, 10),
        clamp_low: (-2).clamp(0, 5),
        clamp_high: 12.clamp(0, 5),
        even_true: 4.even?,
        even_false: 5.even?,
        odd_true: 5.odd?,
        odd_false: 4.odd?
      }
    end

    def float_helpers()
      {
        abs_neg: (-1.25).abs,
        clamp_mid: 1.5.clamp(1.0, 2.0),
        clamp_low: (-1.0).clamp(0.5, 2.0),
        clamp_high: 3.5.clamp(0.5, 2.0),
        round: 1.6.round,
        floor: 1.6.floor,
        ceil: 1.2.ceil
      }
    end
    `)

	intResult := callFunc(t, script, "int_helpers", nil)
	if intResult.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", intResult.Kind())
	}
	ints := intResult.Hash()
	if !ints["abs_neg"].Equal(NewInt(7)) {
		t.Fatalf("abs_neg mismatch: %v", ints["abs_neg"])
	}
	if !ints["abs_pos"].Equal(NewInt(7)) {
		t.Fatalf("abs_pos mismatch: %v", ints["abs_pos"])
	}
	if !ints["clamp_mid"].Equal(NewInt(7)) {
		t.Fatalf("clamp_mid mismatch: %v", ints["clamp_mid"])
	}
	if !ints["clamp_low"].Equal(NewInt(0)) {
		t.Fatalf("clamp_low mismatch: %v", ints["clamp_low"])
	}
	if !ints["clamp_high"].Equal(NewInt(5)) {
		t.Fatalf("clamp_high mismatch: %v", ints["clamp_high"])
	}
	if !ints["even_true"].Bool() || ints["even_false"].Bool() {
		t.Fatalf("even? mismatch: true=%v false=%v", ints["even_true"], ints["even_false"])
	}
	if !ints["odd_true"].Bool() || ints["odd_false"].Bool() {
		t.Fatalf("odd? mismatch: true=%v false=%v", ints["odd_true"], ints["odd_false"])
	}

	floatResult := callFunc(t, script, "float_helpers", nil)
	if floatResult.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", floatResult.Kind())
	}
	floats := floatResult.Hash()
	if floats["abs_neg"].Kind() != KindFloat || floats["abs_neg"].Float() != 1.25 {
		t.Fatalf("float abs mismatch: %v", floats["abs_neg"])
	}
	if floats["clamp_mid"].Kind() != KindFloat || floats["clamp_mid"].Float() != 1.5 {
		t.Fatalf("float clamp mid mismatch: %v", floats["clamp_mid"])
	}
	if floats["clamp_low"].Kind() != KindFloat || floats["clamp_low"].Float() != 0.5 {
		t.Fatalf("float clamp low mismatch: %v", floats["clamp_low"])
	}
	if floats["clamp_high"].Kind() != KindFloat || floats["clamp_high"].Float() != 2.0 {
		t.Fatalf("float clamp high mismatch: %v", floats["clamp_high"])
	}
	if !floats["round"].Equal(NewInt(2)) {
		t.Fatalf("float round mismatch: %v", floats["round"])
	}
	if !floats["floor"].Equal(NewInt(1)) {
		t.Fatalf("float floor mismatch: %v", floats["floor"])
	}
	if !floats["ceil"].Equal(NewInt(2)) {
		t.Fatalf("float ceil mismatch: %v", floats["ceil"])
	}
}

func TestReadmeLeaderboardExample(t *testing.T) {
	script := compileScript(t, `
    def leaderboard(players: array, since: time? = nil, limit: int = 5) -> array
      cutoff = since
      if cutoff == nil
        cutoff = 7.days.ago(Time.now)
      end
      recent = players.select do |p|
        Time.parse(p[:last_seen]) >= cutoff
      end

      ranked = recent.map do |p|
        {
          name: p[:name],
          score: p[:score],
          last_seen: Time.parse(p[:last_seen])
        }
      end

      sorted = ranked.sort do |a, b|
        b[:score] - a[:score]
      end

      top = sorted.first(limit)

      top.map do |entry|
        {
          name: entry[:name],
          score: entry[:score],
          last_seen: entry[:last_seen].format("2006-01-02 15:04:05")
        }
      end
    end
    `)

	players := NewArray([]Value{
		NewHash(map[string]Value{
			"name":      NewString("alex"),
			"score":     NewInt(10),
			"last_seen": NewString("2024-01-10T10:00:00Z"),
		}),
		NewHash(map[string]Value{
			"name":      NewString("cam"),
			"score":     NewInt(15),
			"last_seen": NewString("2024-01-09T11:00:00Z"),
		}),
		NewHash(map[string]Value{
			"name":      NewString("old"),
			"score":     NewInt(99),
			"last_seen": NewString("2023-12-25T00:00:00Z"),
		}),
	})
	since := NewTime(time.Date(2024, time.January, 8, 0, 0, 0, 0, time.UTC))

	result := callFunc(t, script, "leaderboard", []Value{players, since, NewInt(2)})
	if result.Kind() != KindArray {
		t.Fatalf("expected array, got %v", result.Kind())
	}
	arr := result.Array()
	if len(arr) != 2 {
		t.Fatalf("expected 2 leaderboard rows, got %d", len(arr))
	}

	first := arr[0].Hash()
	if first["name"].String() != "cam" || first["score"].Int() != 15 || first["last_seen"].String() != "2024-01-09 11:00:00" {
		t.Fatalf("unexpected first row: %#v", first)
	}
	second := arr[1].Hash()
	if second["name"].String() != "alex" || second["score"].Int() != 10 || second["last_seen"].String() != "2024-01-10 10:00:00" {
		t.Fatalf("unexpected second row: %#v", second)
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

    def union_echo(v: int | string) -> int | string
      v
    end

    def union_optional(v: int | nil = nil) -> int | nil
      v
    end

    def union_bad_return() -> int | string
      true
    end

    def ints_only(values: array<int>) -> array<int>
      values
    end

    def totals_by_player(totals: hash<string, int>) -> hash<string, int>
      totals
    end

    def mixed_items(values: array<int | string>) -> array<int | string>
      values
    end

    def player_payload(payload: { id: string, score: int, active: bool? }) -> { id: string, score: int, active: bool? }
      payload
    end

    def shaped_rows(rows: array<{ id: string, stats: { wins: int } }>) -> array<{ id: string, stats: { wins: int } }>
      rows
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
	if got := callFunc(t, script, "union_echo", []Value{NewInt(7)}); !got.Equal(NewInt(7)) {
		t.Fatalf("union_echo int mismatch: %v", got)
	}
	if got := callFunc(t, script, "union_echo", []Value{NewString("ok")}); !got.Equal(NewString("ok")) {
		t.Fatalf("union_echo string mismatch: %v", got)
	}
	if got := callFunc(t, script, "union_optional", nil); !got.Equal(NewNil()) {
		t.Fatalf("union_optional nil mismatch: %v", got)
	}
	if got := callFunc(t, script, "union_optional", []Value{NewInt(9)}); !got.Equal(NewInt(9)) {
		t.Fatalf("union_optional int mismatch: %v", got)
	}
	if got := callFunc(t, script, "ints_only", []Value{NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})}); got.Kind() != KindArray {
		t.Fatalf("ints_only expected array result, got %v", got.Kind())
	}
	if got := callFunc(t, script, "totals_by_player", []Value{NewHash(map[string]Value{
		"alice": NewInt(10),
		"bob":   NewInt(12),
	})}); got.Kind() != KindHash {
		t.Fatalf("totals_by_player expected hash result, got %v", got.Kind())
	}
	if got := callFunc(t, script, "mixed_items", []Value{NewArray([]Value{NewInt(1), NewString("two"), NewInt(3)})}); got.Kind() != KindArray {
		t.Fatalf("mixed_items expected array result, got %v", got.Kind())
	}
	if got := callFunc(t, script, "player_payload", []Value{NewHash(map[string]Value{
		"id":     NewString("p-1"),
		"score":  NewInt(42),
		"active": NewNil(),
	})}); got.Kind() != KindHash {
		t.Fatalf("player_payload expected hash result, got %v", got.Kind())
	}
	if got := callFunc(t, script, "shaped_rows", []Value{NewArray([]Value{
		NewHash(map[string]Value{
			"id": NewString("p-1"),
			"stats": NewHash(map[string]Value{
				"wins": NewInt(7),
			}),
		}),
	})}); got.Kind() != KindArray {
		t.Fatalf("shaped_rows expected array result, got %v", got.Kind())
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
	if err == nil ||
		!strings.Contains(err.Error(), "argument n expected int, got string") {
		t.Fatalf("expected argument type error, got %v", err)
	}

	_, err = script.Call(context.Background(), "bad_return", []Value{NewInt(1)}, CallOptions{})
	if err == nil {
		res, _ := script.Call(context.Background(), "bad_return", []Value{NewInt(1)}, CallOptions{})
		t.Fatalf("expected return type error, got value %v (%v)", res, res.Kind())
	}
	if !strings.Contains(err.Error(), "return value for bad_return expected int, got string") {
		t.Fatalf("expected return type error, got %v", err)
	}

	_, err = script.Call(context.Background(), "union_echo", []Value{NewBool(true)}, CallOptions{})
	if err == nil ||
		!strings.Contains(err.Error(), "argument v expected int | string, got bool") {
		t.Fatalf("expected union arg type error, got %v", err)
	}

	_, err = script.Call(context.Background(), "union_bad_return", nil, CallOptions{})
	if err == nil ||
		!strings.Contains(err.Error(), "return value for union_bad_return expected int | string, got bool") {
		t.Fatalf("expected union return type error, got %v", err)
	}

	_, err = script.Call(context.Background(), "ints_only", []Value{
		NewArray([]Value{NewInt(1), NewString("oops")}),
	}, CallOptions{})
	if err == nil ||
		!strings.Contains(err.Error(), "argument values expected array<int>, got array<int | string>") {
		t.Fatalf("expected typed array arg error, got %v", err)
	}

	_, err = script.Call(context.Background(), "totals_by_player", []Value{
		NewHash(map[string]Value{"alice": NewString("oops")}),
	}, CallOptions{})
	if err == nil ||
		!strings.Contains(err.Error(), "argument totals expected hash<string, int>, got { alice: string }") {
		t.Fatalf("expected typed hash arg error, got %v", err)
	}

	_, err = script.Call(context.Background(), "mixed_items", []Value{
		NewArray([]Value{NewBool(true)}),
	}, CallOptions{})
	if err == nil ||
		!strings.Contains(err.Error(), "argument values expected array<int | string>, got array<bool>") {
		t.Fatalf("expected typed union array arg error, got %v", err)
	}

	_, err = script.Call(context.Background(), "player_payload", []Value{
		NewHash(map[string]Value{
			"id":    NewString("p-1"),
			"score": NewInt(42),
			"role":  NewString("captain"),
		}),
	}, CallOptions{})
	if err == nil ||
		!strings.Contains(err.Error(), "argument payload expected { active: bool?, id: string, score: int }, got { id: string, role: string, score: int }") {
		t.Fatalf("expected shape extra-field error, got %v", err)
	}

	_, err = script.Call(context.Background(), "player_payload", []Value{
		NewHash(map[string]Value{
			"id":     NewString("p-1"),
			"score":  NewString("wrong"),
			"active": NewBool(true),
		}),
	}, CallOptions{})
	if err == nil ||
		!strings.Contains(err.Error(), "argument payload expected { active: bool?, id: string, score: int }, got { active: bool, id: string, score: string }") {
		t.Fatalf("expected shape field-type error, got %v", err)
	}

	_, err = script.Call(context.Background(), "shaped_rows", []Value{
		NewArray([]Value{
			NewHash(map[string]Value{
				"id": NewString("p-1"),
				"stats": NewHash(map[string]Value{
					"wins": NewString("bad"),
				}),
			}),
		}),
	}, CallOptions{})
	if err == nil ||
		!strings.Contains(err.Error(), "argument rows expected array<{ id: string, stats: { wins: int } }>, got array<{ id: string, stats: { wins: string } }>") {
		t.Fatalf("expected nested shape error, got %v", err)
	}
}

func TestTypeSemanticsContainersNullabilityCoercionAndKeywordStrictness(t *testing.T) {
	script := compileScript(t, `
    def accepts_numbers(values: array<number>) -> array<number>
      values
    end

    def accepts_ints(values: array<int>) -> array<int>
      values
    end

    def nullable_short(v: string?) -> string?
      v
    end

    def nullable_union(v: string | nil) -> string | nil
      v
    end

    def takes_int(v: int) -> int
      v
    end

    def typed_kw(a: int) -> int
      a
    end

    def untyped_kw(a)
      a
    end
    `)

	got := callFunc(t, script, "accepts_numbers", []Value{
		NewArray([]Value{NewInt(1), NewFloat(2.5)}),
	})
	if got.Kind() != KindArray {
		t.Fatalf("accepts_numbers mixed numeric mismatch: %v", got)
	}
	compareArrays(t, got, []Value{NewInt(1), NewFloat(2.5)})

	got = callFunc(t, script, "accepts_numbers", []Value{
		NewArray([]Value{NewInt(1), NewInt(2)}),
	})
	if got.Kind() != KindArray {
		t.Fatalf("accepts_numbers int-only mismatch: %v", got)
	}
	compareArrays(t, got, []Value{NewInt(1), NewInt(2)})

	got = callFunc(t, script, "accepts_ints", []Value{
		NewArray([]Value{NewInt(1), NewInt(2)}),
	})
	if got.Kind() != KindArray {
		t.Fatalf("accepts_ints int-only mismatch: %v", got)
	}
	compareArrays(t, got, []Value{NewInt(1), NewInt(2)})
	_, err := script.Call(context.Background(), "accepts_ints", []Value{
		NewArray([]Value{NewInt(1), NewFloat(2.5)}),
	}, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "argument values expected array<int>, got array<float | int>") {
		t.Fatalf("expected container element strictness error, got %v", err)
	}

	if got := callFunc(t, script, "nullable_short", []Value{NewNil()}); got.Kind() != KindNil {
		t.Fatalf("nullable_short nil mismatch: %#v", got)
	}
	if got := callFunc(t, script, "nullable_union", []Value{NewNil()}); got.Kind() != KindNil {
		t.Fatalf("nullable_union nil mismatch: %#v", got)
	}
	if got := callFunc(t, script, "nullable_short", []Value{NewString("ok")}); got.Kind() != KindString || got.String() != "ok" {
		t.Fatalf("nullable_short string mismatch: %#v", got)
	}
	if got := callFunc(t, script, "nullable_union", []Value{NewString("ok")}); got.Kind() != KindString || got.String() != "ok" {
		t.Fatalf("nullable_union string mismatch: %#v", got)
	}
	_, err = script.Call(context.Background(), "nullable_short", []Value{NewInt(1)}, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "argument v expected string?, got int") {
		t.Fatalf("expected nullable shorthand mismatch, got %v", err)
	}
	_, err = script.Call(context.Background(), "nullable_union", []Value{NewInt(1)}, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "argument v expected string | nil, got int") {
		t.Fatalf("expected nullable union mismatch, got %v", err)
	}

	_, err = script.Call(context.Background(), "takes_int", []Value{NewString("1")}, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "argument v expected int, got string") {
		t.Fatalf("expected no-coercion mismatch, got %v", err)
	}

	extraKw := map[string]Value{
		"a":     NewInt(1),
		"extra": NewInt(2),
	}
	_, err = script.Call(context.Background(), "typed_kw", nil, CallOptions{Keywords: extraKw})
	if err == nil || !strings.Contains(err.Error(), "unexpected keyword argument extra") {
		t.Fatalf("expected typed function unknown kwarg strictness, got %v", err)
	}
	_, err = script.Call(context.Background(), "untyped_kw", nil, CallOptions{Keywords: extraKw})
	if err == nil || !strings.Contains(err.Error(), "unexpected keyword argument extra") {
		t.Fatalf("expected untyped function unknown kwarg strictness, got %v", err)
	}
}

func TestTypedFunctionsRegressionAnyAndNullableBehavior(t *testing.T) {
	script := compileScript(t, `
    def takes_any(v: any) -> any
      v
    end

    def takes_nullable(v: string? = nil) -> string?
      v
    end

    def takes_nullable_union(v: string | nil) -> string | nil
      v
    end
    `)

	anyBuiltin := NewBuiltin("tmp.any", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewNil(), nil
	})
	if got := callFunc(t, script, "takes_any", []Value{anyBuiltin}); got.Kind() != KindBuiltin {
		t.Fatalf("takes_any builtin mismatch: %#v", got)
	}
	if got := callFunc(t, script, "takes_any", []Value{NewHash(map[string]Value{"x": NewInt(1)})}); got.Kind() != KindHash {
		t.Fatalf("takes_any hash mismatch: %#v", got)
	}
	if got := callFunc(t, script, "takes_any", []Value{NewNil()}); got.Kind() != KindNil {
		t.Fatalf("takes_any nil mismatch: %#v", got)
	}

	if got := callFunc(t, script, "takes_nullable", nil); got.Kind() != KindNil {
		t.Fatalf("takes_nullable default nil mismatch: %#v", got)
	}
	if got := callFunc(t, script, "takes_nullable", []Value{NewString("ok")}); got.Kind() != KindString || got.String() != "ok" {
		t.Fatalf("takes_nullable string mismatch: %#v", got)
	}
	_, err := script.Call(context.Background(), "takes_nullable", []Value{NewInt(1)}, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "argument v expected string?, got int") {
		t.Fatalf("expected nullable type mismatch, got %v", err)
	}

	if got := callFunc(t, script, "takes_nullable_union", []Value{NewNil()}); got.Kind() != KindNil {
		t.Fatalf("takes_nullable_union nil mismatch: %#v", got)
	}
	if got := callFunc(t, script, "takes_nullable_union", []Value{NewString("ok")}); got.Kind() != KindString || got.String() != "ok" {
		t.Fatalf("takes_nullable_union string mismatch: %#v", got)
	}
	_, err = script.Call(context.Background(), "takes_nullable_union", []Value{NewInt(1)}, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "argument v expected string | nil, got int") {
		t.Fatalf("expected nullable union mismatch, got %v", err)
	}
}

func TestTypedFunctionsRejectCyclicHashInputWithoutInfiniteRecursion(t *testing.T) {
	script := compileScript(t, `
    def run(payload: hash<string, hash<string, int>>) -> hash<string, hash<string, int>>
      payload
    end
    `)

	entries := map[string]Value{}
	payload := NewHash(entries)
	entries["self"] = payload

	done := make(chan error, 1)
	go func() {
		_, err := script.Call(context.Background(), "run", []Value{payload}, CallOptions{})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected type validation error for cyclic payload")
		}
		if !strings.Contains(err.Error(), "argument payload expected hash<string, hash<string, int>>") {
			t.Fatalf("unexpected type error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("type validation did not terminate for cyclic payload")
	}
}

func TestExistingUntypedScriptsRemainCompatible(t *testing.T) {
	script := compileScript(t, `
    def identity(v)
      v
    end

    def run()
      first = identity(1)
      second = identity("two")
      third = identity({ ok: true })
      {
        first: first,
        second: second,
        third_ok: third[:ok]
      }
    end
    `)

	got := callFunc(t, script, "run", nil)
	if got.Kind() != KindHash {
		t.Fatalf("expected hash result, got %v", got.Kind())
	}
	hash := got.Hash()
	if hash["first"].Kind() != KindInt || hash["first"].Int() != 1 {
		t.Fatalf("unexpected first value: %#v", hash["first"])
	}
	if hash["second"].Kind() != KindString || hash["second"].String() != "two" {
		t.Fatalf("unexpected second value: %#v", hash["second"])
	}
	if hash["third_ok"].Kind() != KindBool || !hash["third_ok"].Bool() {
		t.Fatalf("unexpected third_ok value: %#v", hash["third_ok"])
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

	    def hash_remap()
	      { first_name: "Alex", total_raised: 10 }.remap_keys({ first_name: :name, total_raised: :raised })
	    end

	    def hash_remap_collision()
	      { a: 1, b: 2 }.remap_keys({ a: :x, b: :x })
	    end

    def hash_deep_transform()
      payload = {
        player_id: 7,
        profile: { total_raised: 12 },
        events: [{ amount_cents: 300 }]
      }
      payload.deep_transform_keys do |k|
        if k == :player_id
          :playerId
        elsif k == :total_raised
          :totalRaised
        elsif k == :amount_cents
          :amountCents
        else
          k
        end
      end
    end

    def bad_hash_remap()
      { a: 1 }.remap_keys({ a: 1 })
    end

	    def bad_deep_transform()
	      { a: 1 }.deep_transform_keys do |k|
	        1
	      end
	    end

	    def bad_deep_transform_cycle()
	      cyc = {}
	      cyc[:self] = cyc
	      cyc.deep_transform_keys do |k|
	        k
	      end
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

	remapped := callFunc(t, script, "hash_remap", nil)
	if remapped.Kind() != KindHash {
		t.Fatalf("expected remapped hash, got %v", remapped.Kind())
	}
	compareHash(t, remapped.Hash(), map[string]Value{
		"name":   NewString("Alex"),
		"raised": NewInt(10),
	})

	colliding := callFunc(t, script, "hash_remap_collision", nil)
	if colliding.Kind() != KindHash {
		t.Fatalf("expected colliding remap hash, got %v", colliding.Kind())
	}
	if got := colliding.Hash()["x"]; got.Kind() != KindInt || got.Int() != 2 {
		t.Fatalf("expected deterministic collision winner x=2, got %#v", got)
	}

	deepTransformed := callFunc(t, script, "hash_deep_transform", nil)
	if deepTransformed.Kind() != KindHash {
		t.Fatalf("expected deep transformed hash, got %v", deepTransformed.Kind())
	}
	dh := deepTransformed.Hash()
	if !dh["playerId"].Equal(NewInt(7)) {
		t.Fatalf("playerId mismatch: %v", dh["playerId"])
	}
	profileVal := dh["profile"]
	if profileVal.Kind() != KindHash {
		t.Fatalf("profile expected hash, got %v", profileVal.Kind())
	}
	profile, ok := profileVal.Hash()["totalRaised"]
	if !ok || !profile.Equal(NewInt(12)) {
		t.Fatalf("profile.totalRaised mismatch: %#v", profileVal)
	}
	events := dh["events"]
	if events.Kind() != KindArray || len(events.Array()) != 1 {
		t.Fatalf("events mismatch: %v", events)
	}
	event := events.Array()[0]
	if event.Kind() != KindHash {
		t.Fatalf("event expected hash, got %v", event.Kind())
	}
	if !event.Hash()["amountCents"].Equal(NewInt(300)) {
		t.Fatalf("amountCents mismatch: %v", event.Hash()["amountCents"])
	}

	_, err := script.Call(context.Background(), "bad_hash_remap", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "hash.remap_keys mapping values must be symbol or string") {
		t.Fatalf("expected bad remap error, got %v", err)
	}
	_, err = script.Call(context.Background(), "bad_deep_transform", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "hash.deep_transform_keys block must return symbol or string") {
		t.Fatalf("expected bad deep transform error, got %v", err)
	}
	_, err = script.Call(context.Background(), "bad_deep_transform_cycle", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "hash.deep_transform_keys does not support cyclic structures") {
		t.Fatalf("expected cyclic deep transform error, got %v", err)
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
      template_context = {
        user: { name: "Alex", score: 42 },
        id: :p_1,
        missing_nil: nil
      }
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
        squish: "  hello \n\t world  ".squish,
        squish_bang: "  hello \n\t world  ".squish!,
        squish_bang_nochange: "hello world".squish!,
        template_basic: "Hello {{name}}".template({ name: "Alex" }),
        template_nested: "Player {{user.name}} scored {{user.score}}".template(template_context),
        template_symbol: "ID={{id}}".template(template_context),
        template_nil: "Value={{missing_nil}}".template(template_context),
        template_missing_passthrough: "Hello {{missing}}".template({ name: "Alex" }),
        template_spacing: "Hello {{ name }}".template({ name: "Alex" }),
        template_multiple: "{{name}}/{{name}}".template({ name: "Alex" }),
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
	if got["squish"].String() != "hello world" {
		t.Fatalf("squish mismatch: %q", got["squish"].String())
	}
	if got["squish_bang"].String() != "hello world" {
		t.Fatalf("squish_bang mismatch: %q", got["squish_bang"].String())
	}
	if got["squish_bang_nochange"].Kind() != KindNil {
		t.Fatalf("squish_bang_nochange expected nil, got %v", got["squish_bang_nochange"])
	}
	if got["template_basic"].String() != "Hello Alex" {
		t.Fatalf("template_basic mismatch: %q", got["template_basic"].String())
	}
	if got["template_nested"].String() != "Player Alex scored 42" {
		t.Fatalf("template_nested mismatch: %q", got["template_nested"].String())
	}
	if got["template_symbol"].String() != "ID=p_1" {
		t.Fatalf("template_symbol mismatch: %q", got["template_symbol"].String())
	}
	if got["template_nil"].String() != "Value=" {
		t.Fatalf("template_nil mismatch: %q", got["template_nil"].String())
	}
	if got["template_missing_passthrough"].String() != "Hello {{missing}}" {
		t.Fatalf("template_missing_passthrough mismatch: %q", got["template_missing_passthrough"].String())
	}
	if got["template_spacing"].String() != "Hello Alex" {
		t.Fatalf("template_spacing mismatch: %q", got["template_spacing"].String())
	}
	if got["template_multiple"].String() != "Alex/Alex" {
		t.Fatalf("template_multiple mismatch: %q", got["template_multiple"].String())
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
			name:   "string.squish with argument",
			script: `def run() "hello".squish(1) end`,
			errMsg: "string.squish does not take arguments",
		},
		{
			name:   "string.gsub! with missing argument",
			script: `def run() "hello".gsub!("l") end`,
			errMsg: "expects pattern and replacement",
		},
		{
			name:   "string.template without context",
			script: `def run() "hello {{name}}".template end`,
			errMsg: "expects exactly one context hash",
		},
		{
			name:   "string.template with non-hash context",
			script: `def run() "hello {{name}}".template(1) end`,
			errMsg: "context must be hash",
		},
		{
			name:   "string.template with unknown keyword",
			script: `def run() "hello {{name}}".template({}, foo: true) end`,
			errMsg: "supports only strict keyword",
		},
		{
			name:   "string.template with non-bool strict keyword",
			script: `def run() "hello {{name}}".template({}, strict: 1) end`,
			errMsg: "strict keyword must be bool",
		},
		{
			name:   "string.template strict missing key",
			script: `def run() "hello {{name}}".template({}, strict: true) end`,
			errMsg: "missing placeholder name",
		},
		{
			name:   "string.template with non-scalar value",
			script: `def run() "hello {{items}}".template({ items: [1, 2] }) end`,
			errMsg: "placeholder items value must be scalar",
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
		{
			name:   "Time.parse unknown keyword",
			script: `def run() Time.parse("2024-01-01T00:00:00Z", foo: "bar") end`,
			errMsg: "unknown keyword",
		},
		{
			name:   "Time.parse layout must be string",
			script: `def run() Time.parse("2024-01-01T00:00:00Z", 123) end`,
			errMsg: "layout must be string",
		},
		{
			name:   "int.clamp with wrong arity",
			script: `def run() 5.clamp(1) end`,
			errMsg: "expects min and max",
		},
		{
			name:   "int.clamp with inverted bounds",
			script: `def run() 5.clamp(10, 1) end`,
			errMsg: "min must be <= max",
		},
		{
			name:   "float.round with argument",
			script: `def run() 1.5.round(1) end`,
			errMsg: "does not take arguments",
		},
		{
			name:   "float.clamp with non-numeric bounds",
			script: `def run() 1.5.clamp("a", 2.0) end`,
			errMsg: "expects numeric min and max",
		},
		{
			name:   "float.round overflow",
			script: `def run() 100000000000000000000.0.round end`,
			errMsg: "out of int64 range",
		},
		{
			name:   "float.floor overflow",
			script: `def run() 100000000000000000000.0.floor end`,
			errMsg: "out of int64 range",
		},
		{
			name:   "float.ceil overflow",
			script: `def run() 100000000000000000000.0.ceil end`,
			errMsg: "out of int64 range",
		},
		{
			name:   "float.round int64 boundary overflow",
			script: `def run() 9223372036854775808.0.round end`,
			errMsg: "out of int64 range",
		},
		{
			name:   "float.floor int64 boundary overflow",
			script: `def run() 9223372036854775808.0.floor end`,
			errMsg: "out of int64 range",
		},
		{
			name:   "float.ceil int64 boundary overflow",
			script: `def run() 9223372036854775808.0.ceil end`,
			errMsg: "out of int64 range",
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
