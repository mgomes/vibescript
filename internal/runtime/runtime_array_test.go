package runtime

import (
	"context"
	"testing"
)

func TestArrayPushPopAndSum(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
        size: values.size,
        length: values.length,
        empty_false: values.empty?,
        empty_true: [].empty?,
        include_hit: values.include?(2),
        include_miss: values.include?(9),
        index_hit: values.index(1),
        index_offset_hit: values.index(1, 2),
        index_miss: values.index(9),
        rindex_hit: values.rindex(1),
        rindex_offset_hit: values.rindex(1, 2),
        rindex_miss: values.rindex(9),
        fetch_hit: values.fetch(2),
        fetch_default: values.fetch(9, 42),
        fetch_miss: values.fetch(9),
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
	if !got["size"].Equal(NewInt(4)) || !got["length"].Equal(NewInt(4)) {
		t.Fatalf("size/length mismatch: size=%v length=%v", got["size"], got["length"])
	}
	if got["empty_false"].Bool() || !got["empty_true"].Bool() {
		t.Fatalf("empty? mismatch: false=%v true=%v", got["empty_false"], got["empty_true"])
	}
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
	if !got["fetch_hit"].Equal(NewInt(2)) || !got["fetch_default"].Equal(NewInt(42)) {
		t.Fatalf("fetch mismatch: hit=%v default=%v", got["fetch_hit"], got["fetch_default"])
	}
	if got["fetch_miss"].Kind() != KindNil {
		t.Fatalf("fetch_miss expected nil, got %v", got["fetch_miss"])
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
	t.Parallel()
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

	requireCallErrorContains(t, script, "bad_chunk", nil, CallOptions{}, "array.chunk size must be a positive integer")
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
	requireCallErrorContains(t, script, "bad_window", nil, CallOptions{}, "array.window size must be a positive integer")
	hugeWindow := callFunc(t, script, "huge_window", []Value{NewInt(nativeMaxInt)})
	if hugeWindow.Kind() != KindArray || len(hugeWindow.Array()) != 0 {
		t.Fatalf("expected huge window size to return empty array, got %v", hugeWindow)
	}

	overflowSize := int64(1 << 62)
	if nativeMaxInt < overflowSize {
		requireCallErrorContains(t, script, "huge_chunk", []Value{NewInt(overflowSize)}, CallOptions{}, "array.chunk size must be a positive integer")
		requireCallErrorContains(t, script, "huge_window", []Value{NewInt(overflowSize)}, CallOptions{}, "array.window size must be a positive integer")
	}
	requireCallErrorContains(t, script, "bad_group_by_stable", nil, CallOptions{}, "array.group_by_stable requires a block")
}

func TestArrayConcatAndSubtract(t *testing.T) {
	t.Parallel()
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

func TestArrayUniqUsesScalarKeysAndCompositeFallback(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def uniq_values(values)
      values.uniq()
    end
    `)

	values := NewArray([]Value{
		NewInt(1),
		NewString("1"),
		NewSymbol("1"),
		NewInt(1),
		NewString("1"),
		NewArray([]Value{NewInt(2)}),
		NewArray([]Value{NewInt(2)}),
		NewHash(map[string]Value{"nested": NewArray([]Value{NewInt(3)})}),
		NewHash(map[string]Value{"nested": NewArray([]Value{NewInt(3)})}),
	})

	unique := callFunc(t, script, "uniq_values", []Value{values})
	compareArrays(t, unique, []Value{
		NewInt(1),
		NewString("1"),
		NewSymbol("1"),
		NewArray([]Value{NewInt(2)}),
		NewHash(map[string]Value{"nested": NewArray([]Value{NewInt(3)})}),
	})
}

func TestArraySubtractUsesScalarKeysAndCompositeFallback(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def subtract(first, second)
      first - second
    end
    `)

	first := NewArray([]Value{
		NewInt(1),
		NewString("1"),
		NewSymbol("1"),
		NewArray([]Value{NewInt(2)}),
		NewHash(map[string]Value{"nested": NewArray([]Value{NewInt(3)})}),
		NewArray([]Value{NewInt(4)}),
	})
	second := NewArray([]Value{
		NewString("1"),
		NewArray([]Value{NewInt(2)}),
		NewHash(map[string]Value{"nested": NewArray([]Value{NewInt(3)})}),
	})

	subtracted := callFunc(t, script, "subtract", []Value{first, second})
	compareArrays(t, subtracted, []Value{
		NewInt(1),
		NewSymbol("1"),
		NewArray([]Value{NewInt(4)}),
	})
}

func TestArraySumRejectsNonNumeric(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `
    def bad()
      ["a"].sum()
    end
    `)

	_, err := script.Call(context.Background(), "bad", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected runtime error for non-numeric sum")
	}
}

func TestArrayAndHashHelpers(t *testing.T) {
	t.Parallel()
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

	requireCallErrorContains(t, script, "bad_hash_remap", nil, CallOptions{}, "hash.remap_keys mapping values must be symbol or string")
	requireCallErrorContains(t, script, "bad_deep_transform", nil, CallOptions{}, "hash.deep_transform_keys block must return symbol or string")
	requireCallErrorContains(t, script, "bad_deep_transform_cycle", nil, CallOptions{}, "hash.deep_transform_keys does not support cyclic structures")
}
