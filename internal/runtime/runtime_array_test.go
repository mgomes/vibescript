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

func TestArrayPushCallShapes(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def push_no_parens(values)
      values.push
    end

    def push_empty_parens(values)
      values.push()
    end

    def push_one(values, extra)
      values.push(extra)
    end

    def push_many(values, a, b, c)
      values.push(a, b, c)
    end
    `)

	tests := []struct {
		name     string
		function string
		args     []Value
		want     []Value
	}{
		{
			name:     "no parens is a no-op returning the array",
			function: "push_no_parens",
			args:     []Value{NewArray([]Value{NewInt(1), NewInt(2)})},
			want:     []Value{NewInt(1), NewInt(2)},
		},
		{
			name:     "empty parens is a no-op returning the array",
			function: "push_empty_parens",
			args:     []Value{NewArray([]Value{NewInt(1), NewInt(2)})},
			want:     []Value{NewInt(1), NewInt(2)},
		},
		{
			name:     "single value appends",
			function: "push_one",
			args:     []Value{NewArray([]Value{NewInt(1), NewInt(2)}), NewInt(3)},
			want:     []Value{NewInt(1), NewInt(2), NewInt(3)},
		},
		{
			name:     "multiple values append in order",
			function: "push_many",
			args:     []Value{NewArray([]Value{NewInt(1), NewInt(2)}), NewInt(3), NewInt(4), NewInt(5)},
			want:     []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4), NewInt(5)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			compareArrays(t, callFunc(t, script, tt.function, tt.args), tt.want)
		})
	}
}

func TestArrayPushRejectsKeywordArguments(t *testing.T) {
	t.Parallel()
	// Keyword-only push (args empty, kwargs non-empty) must not silently drop
	// the keyword map; it raises a clear error instead. Bare push and push()
	// with no positional or keyword arguments stay valid no-ops.
	script := compileScript(t, `
    def push_keyword(values)
      values.push(foo: 1)
    end

    def push_no_parens(values)
      values.push
    end

    def push_empty_parens(values)
      values.push()
    end
    `)

	base := []Value{NewArray([]Value{NewInt(1), NewInt(2)})}
	requireCallErrorContains(t, script, "push_keyword", base, CallOptions{},
		"array.push does not take keyword arguments")
	compareArrays(t, callFunc(t, script, "push_no_parens", base), []Value{NewInt(1), NewInt(2)})
	compareArrays(t, callFunc(t, script, "push_empty_parens", base), []Value{NewInt(1), NewInt(2)})
}

func TestArrayPushAppendAssignmentZeroArgs(t *testing.T) {
	t.Parallel()
	// x = x.push and x = x.push() exercise the in-place append-assignment
	// fast path; with no values they must be no-ops that keep x intact and
	// preserve alias isolation against later appends.
	script := compileScript(t, `
    def no_parens()
      x = [1, 2]
      x = x.push
      x
    end

    def empty_parens()
      x = [1, 2]
      x = x.push()
      x
    end

    def alias_isolation()
      a = [1]
      b = a
      a = a.push
      a = a.push(2)
      b[0] = 9
      { a: a, b: b }
    end
    `)

	compareArrays(t, callFunc(t, script, "no_parens", nil), []Value{NewInt(1), NewInt(2)})
	compareArrays(t, callFunc(t, script, "empty_parens", nil), []Value{NewInt(1), NewInt(2)})

	aliased := callFunc(t, script, "alias_isolation", nil).Hash()
	compareArrays(t, aliased["a"], []Value{NewInt(1), NewInt(2)})
	compareArrays(t, aliased["b"], []Value{NewInt(9)})
}

func TestArrayAppendAssignmentAccumulation(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def push_accumulate(n)
      out = []
      for i in 1..n
        out = out.push(i)
      end
      out
    end

    def concat_accumulate(n)
      out = []
      for i in 1..n
        out = out + [i]
      end
      out
    end
    `)

	want := []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4), NewInt(5)}
	compareArrays(t, callFunc(t, script, "push_accumulate", []Value{NewInt(5)}), want)
	compareArrays(t, callFunc(t, script, "concat_accumulate", []Value{NewInt(5)}), want)
}

func TestArrayAppendAssignmentPreservesAliasIsolation(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def push_alias()
      a = [1]
      b = a
      a = a.push(2)
      b[0] = 9
      { a: a, b: b }
    end

    def concat_alias()
      a = [1]
      b = a
      a = a + [2]
      b[0] = 9
      { a: a, b: b }
    end

    def repeated_alias()
      a = []
      a = a.push(1)
      b = a
      a = a.push(2)
      b = b.push(3)
      { a: a, b: b }
    end
    `)

	push := callFunc(t, script, "push_alias", nil).Hash()
	compareArrays(t, push["a"], []Value{NewInt(1), NewInt(2)})
	compareArrays(t, push["b"], []Value{NewInt(9)})

	concat := callFunc(t, script, "concat_alias", nil).Hash()
	compareArrays(t, concat["a"], []Value{NewInt(1), NewInt(2)})
	compareArrays(t, concat["b"], []Value{NewInt(9)})

	repeated := callFunc(t, script, "repeated_alias", nil).Hash()
	compareArrays(t, repeated["a"], []Value{NewInt(1), NewInt(2)})
	compareArrays(t, repeated["b"], []Value{NewInt(1), NewInt(3)})
}

func TestArrayAppendAssignmentDetachesEscapedBlockResults(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def block_results()
      out = []
      results = [1, 2].map do |v|
        out = out.push(v)
      end
      second = results[1]
      second[0] = 9
      results
    end
    `)

	results := callFunc(t, script, "block_results", nil).Array()
	if len(results) != 2 {
		t.Fatalf("block_results length = %d, want 2", len(results))
	}
	compareArrays(t, results[0], []Value{NewInt(1)})
	compareArrays(t, results[1], []Value{NewInt(9), NewInt(2)})
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
        fetch_negative: values.fetch(-1),
        fetch_default: values.fetch(9, 42),
        fetch_block: values.fetch(9) { |idx| idx + 10 },
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
	if !got["fetch_negative"].Equal(NewInt(1)) {
		t.Fatalf("fetch_negative expected 1, got %v", got["fetch_negative"])
	}
	if !got["fetch_block"].Equal(NewInt(19)) {
		t.Fatalf("fetch_block expected 19, got %v", got["fetch_block"])
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

func TestArrayCountValueIgnoresBlock(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def count_value_with_block()
      [1, 2, 1, 3].count(1) do |v|
        raise "block must not run"
      end
    end
    `)

	// Ruby's Array#count(value) ignores any attached block: the call succeeds,
	// returns the value count, and never invokes the block.
	got := callFunc(t, script, "count_value_with_block", nil)
	if !got.Equal(NewInt(2)) {
		t.Fatalf("count(value) with block mismatch: want 2, got %#v", got)
	}
}

func TestArrayPredicateValueArgument(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want bool
	}{
		// any?(value): true when at least one element matches; empty is false.
		{name: "any hit", expr: "[1, 2, 3].any?(2)", want: true},
		{name: "any miss", expr: "[1, 2, 3].any?(9)", want: false},
		{name: "any empty", expr: "[].any?(1)", want: false},
		{name: "any matches falsy element", expr: "[nil, false].any?(false)", want: true},
		{name: "any cross type no match", expr: "[1, 2, 3].any?(\"2\")", want: false},
		// Range patterns are matched with case equality (===), so the argument
		// tests membership rather than object identity.
		{name: "any range hit", expr: "[2].any?(1..3)", want: true},
		{name: "any range miss", expr: "[5].any?(1..3)", want: false},
		{name: "any range exclusive boundary", expr: "[3].any?(1...3)", want: false},
		// all?(value): true when every element matches; empty is vacuously true.
		{name: "all hit", expr: "[1, 1, 1].all?(1)", want: true},
		{name: "all miss", expr: "[1, 2, 1].all?(1)", want: false},
		{name: "all empty", expr: "[].all?(1)", want: true},
		{name: "all range hit", expr: "[1, 2].all?(1..3)", want: true},
		{name: "all range miss", expr: "[1, 4].all?(1..3)", want: false},
		// none?(value): true when no element matches; empty is vacuously true.
		{name: "none hit", expr: "[3, 4].none?(1)", want: true},
		{name: "none miss", expr: "[1, 2].none?(1)", want: false},
		{name: "none empty", expr: "[].none?(1)", want: true},
		{name: "none range hit", expr: "[4, 5].none?(1..3)", want: true},
		{name: "none range miss", expr: "[2, 5].none?(1..3)", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend\n")
			got := callFunc(t, script, "run", nil)
			if got.Kind() != KindBool {
				t.Fatalf("%s expected bool, got %v", tc.expr, got.Kind())
			}
			if got.Bool() != tc.want {
				t.Fatalf("%s = %v, want %v", tc.expr, got.Bool(), tc.want)
			}
		})
	}
}

func TestArrayPredicateValueArgumentIgnoresBlock(t *testing.T) {
	t.Parallel()
	// As in Ruby, an explicit value argument takes precedence over an attached
	// block: the call succeeds and the block is never invoked.
	cases := []struct {
		name string
		expr string
		want bool
	}{
		{name: "any?", expr: "[1, 2, 1, 3].any?(1) do |v| raise \"block must not run\" end", want: true},
		{name: "all?", expr: "[1, 1].all?(1) do |v| raise \"block must not run\" end", want: true},
		{name: "none?", expr: "[3, 4].none?(1) do |v| raise \"block must not run\" end", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend\n")
			got := callFunc(t, script, "run", nil)
			if got.Kind() != KindBool || got.Bool() != tc.want {
				t.Fatalf("%s = %#v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestArrayPredicateRejectsExtraArguments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want string
	}{
		{name: "any?", expr: "[1, 2, 3].any?(1, 2)", want: "array.any? accepts at most one value argument"},
		{name: "all?", expr: "[1, 2, 3].all?(1, 2)", want: "array.all? accepts at most one value argument"},
		{name: "none?", expr: "[1, 2, 3].none?(1, 2)", want: "array.none? accepts at most one value argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend\n")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

func TestArrayPredicateRejectsKeywordArguments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want string
	}{
		{name: "any?", expr: "[1].any?(1, unexpected: true)", want: "array.any? does not take keyword arguments"},
		{name: "all?", expr: "[1].all?(1, unexpected: true)", want: "array.all? does not take keyword arguments"},
		{name: "none?", expr: "[1].none?(1, unexpected: true)", want: "array.none? does not take keyword arguments"},
		{name: "any? without value", expr: "[1].any?(unexpected: true)", want: "array.any? does not take keyword arguments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend\n")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

func TestArrayFlattenDepthArguments(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def flatten_default()
      [1, [2, [3]]].flatten
    end

    def flatten_nil()
      [1, [2, [3]]].flatten(nil)
    end

    def flatten_negative()
      [1, [2, [3]]].flatten(-1)
    end

    def flatten_deep_negative()
      [1, [2, [3]]].flatten(-5)
    end

    def flatten_zero()
      [1, [2, [3]]].flatten(0)
    end

    def flatten_one()
      [1, [2, [3]]].flatten(1)
    end

    def flatten_two()
      [1, [2, [3]]].flatten(2)
    end

    def flatten_float()
      [1, [2, [3]]].flatten(1.2)
    end

    def flatten_string()
      [1, [2, [3]]].flatten("1")
    end

    def flatten_too_many()
      [1, [2, [3]]].flatten(1, 2)
    end
    `)

	// nil, no argument, and any negative depth flatten fully, matching Ruby.
	full := []Value{NewInt(1), NewInt(2), NewInt(3)}
	for _, fn := range []string{"flatten_default", "flatten_nil", "flatten_negative", "flatten_deep_negative", "flatten_two"} {
		compareArrays(t, callFunc(t, script, fn, nil), full)
	}

	// Zero depth returns a shallow copy without flattening any nesting.
	compareArrays(t, callFunc(t, script, "flatten_zero", nil), []Value{
		NewInt(1),
		NewArray([]Value{NewInt(2), NewArray([]Value{NewInt(3)})}),
	})

	// A positive depth flattens that many levels; a float is truncated to int.
	oneLevel := []Value{NewInt(1), NewInt(2), NewArray([]Value{NewInt(3)})}
	compareArrays(t, callFunc(t, script, "flatten_one", nil), oneLevel)
	compareArrays(t, callFunc(t, script, "flatten_float", nil), oneLevel)

	// Nonnumeric depths are rejected, as are extra arguments.
	requireCallErrorContains(t, script, "flatten_string", nil, CallOptions{}, "array.flatten depth must be an integer")
	requireCallErrorContains(t, script, "flatten_too_many", nil, CallOptions{}, "array.flatten accepts at most one depth argument")
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

func TestArrayValuesAt(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def lookup(values)
      values.values_at(0, -1, 9)
    end

    def lookup_negative(values)
      values.values_at(-1, -2, -3)
    end

    def lookup_duplicates(values)
      values.values_at(0, 0, 1, 1)
    end

    def lookup_empty(values)
      values.values_at
    end

    def lookup_out_of_range(values)
      values.values_at(3, -4)
    end

    def lookup_float(values)
      values.values_at(1.5, -1.9)
    end
    `)

	values := NewArray([]Value{NewInt(10), NewInt(20), NewInt(30)})

	t.Run("returns values in request order with nil for misses", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "lookup", []Value{values})
		compareArrays(t, got, []Value{NewInt(10), NewInt(30), NewNil()})
	})

	t.Run("negative indexes count from the end", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "lookup_negative", []Value{values})
		compareArrays(t, got, []Value{NewInt(30), NewInt(20), NewInt(10)})
	})

	t.Run("duplicate indexes repeat their values", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "lookup_duplicates", []Value{values})
		compareArrays(t, got, []Value{NewInt(10), NewInt(10), NewInt(20), NewInt(20)})
	})

	t.Run("no indexes returns empty array", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "lookup_empty", []Value{values})
		compareArrays(t, got, []Value{})
	})

	t.Run("out-of-range indexes yield nil", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "lookup_out_of_range", []Value{values})
		compareArrays(t, got, []Value{NewNil(), NewNil()})
	})

	t.Run("float indexes truncate toward zero like Ruby", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "lookup_float", []Value{values})
		compareArrays(t, got, []Value{NewInt(20), NewInt(30)})
	})
}

func TestArrayValuesAtRejectsNonIntegerArguments(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def lookup_string(values)
      values.values_at("0")
    end

    def lookup_keyword(values)
      values.values_at(index: 0)
    end
    `)

	values := NewArray([]Value{NewInt(10), NewInt(20), NewInt(30)})
	requireCallErrorContains(t, script, "lookup_string", []Value{values}, CallOptions{}, "array.values_at index must be integer")
	requireCallErrorContains(t, script, "lookup_keyword", []Value{values}, CallOptions{}, "array.values_at does not take keyword arguments")
}

func TestArrayValuesAtRangeSelectors(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def in_bounds(values)
      values.values_at(0..1)
    end

    def mixed_range_and_int(values)
      values.values_at(0..1, -1)
    end

    def exclusive(values)
      values.values_at(0...2)
    end

    def end_past_length(values)
      values.values_at(0..5)
    end

    def partial_out_of_range(values)
      values.values_at(2..5)
    end

    def begin_at_length(values)
      values.values_at(3..7)
    end

    def fully_out_of_range(values)
      values.values_at(5..7)
    end

    def negative_bounds(values)
      values.values_at(-2..-1)
    end

    def negative_begin_past_start(values)
      values.values_at(-3..5)
    end

    def empty_begin_after_end(values)
      values.values_at(2..0)
    end

    def empty_exclusive(values)
      values.values_at(0...0)
    end

    def negative_empty(values)
      values.values_at(-1..-3)
    end

    def full_via_negative_end(values)
      values.values_at(0..-1)
    end

    def interleaved(values)
      values.values_at(0, 1..2, -1)
    end

    def float_range(values)
      values.values_at(0.5..2.9)
    end

    def max_int_singleton(values)
      values.values_at(9223372036854775807..9223372036854775807)
    end

    def max_int_singleton_exclusive(values)
      values.values_at(9223372036854775807...9223372036854775807)
    end

    def near_max_int_pair(values)
      values.values_at(9223372036854775806..9223372036854775807)
    end
    `)

	values := NewArray([]Value{NewInt(10), NewInt(20), NewInt(30)})
	nilV := NewNil()

	cases := []struct {
		name string
		fn   string
		want []Value
	}{
		{"range within bounds", "in_bounds", []Value{NewInt(10), NewInt(20)}},
		{"range mixed with negative int", "mixed_range_and_int", []Value{NewInt(10), NewInt(20), NewInt(30)}},
		{"exclusive range", "exclusive", []Value{NewInt(10), NewInt(20)}},
		{"range end past length pads nil", "end_past_length", []Value{NewInt(10), NewInt(20), NewInt(30), nilV, nilV, nilV}},
		{"partial out of range pads nil", "partial_out_of_range", []Value{NewInt(30), nilV, nilV, nilV}},
		{"begin at length pads nil", "begin_at_length", []Value{nilV, nilV, nilV, nilV, nilV}},
		{"fully out of range pads nil", "fully_out_of_range", []Value{nilV, nilV, nilV}},
		{"negative bounds", "negative_bounds", []Value{NewInt(20), NewInt(30)}},
		{"negative begin with end past length", "negative_begin_past_start", []Value{NewInt(10), NewInt(20), NewInt(30), nilV, nilV, nilV}},
		{"begin after end is empty", "empty_begin_after_end", []Value{}},
		{"exclusive empty range", "empty_exclusive", []Value{}},
		{"negative reversed range is empty", "negative_empty", []Value{}},
		{"full range via negative end", "full_via_negative_end", []Value{NewInt(10), NewInt(20), NewInt(30)}},
		{"int and range interleaved flatten in order", "interleaved", []Value{NewInt(10), NewInt(20), NewInt(30), NewInt(30)}},
		{"float range bounds truncate toward zero", "float_range", []Value{NewInt(10), NewInt(20), NewInt(30)}},
		// An inclusive range ending at MaxInt64 must report its true (tiny) span
		// rather than overflowing the exclusive-end calculation and rejecting the
		// call. The selected position is far past the receiver, so it pads with nil.
		{"max int singleton pads nil", "max_int_singleton", []Value{nilV}},
		{"max int singleton exclusive is empty", "max_int_singleton_exclusive", []Value{}},
		{"near max int inclusive pair pads nil", "near_max_int_pair", []Value{nilV, nilV}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, []Value{values})
			compareArrays(t, got, tc.want)
		})
	}
}

func TestArrayValuesAtRangeOnEmptyReceiver(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def inclusive(values)
      values.values_at(0..2)
    end

    def full(values)
      values.values_at(0..-1)
    end
    `)

	empty := NewArray([]Value{})
	nilV := NewNil()

	got := callFunc(t, script, "inclusive", []Value{empty})
	compareArrays(t, got, []Value{nilV, nilV, nilV})

	got = callFunc(t, script, "full", []Value{empty})
	compareArrays(t, got, []Value{})
}

func TestArrayValuesAtRejectsNegativeBeginPastStart(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def out_of_range(values)
      values.values_at(-4..-1)
    end

    def out_of_range_empty_window(values)
      values.values_at(-4..-5)
    end
    `)

	values := NewArray([]Value{NewInt(10), NewInt(20), NewInt(30)})
	requireCallErrorContains(t, script, "out_of_range", []Value{values}, CallOptions{}, "array.values_at range -4..-1 out of range")
	// Ruby rejects a negative begin past the start even when the window would be
	// empty, so the begin check runs before any emptiness short-circuit.
	requireCallErrorContains(t, script, "out_of_range_empty_window", []Value{values}, CallOptions{}, "array.values_at range -4..-5 out of range")
}

func TestArrayValuesAtRangeRejectsGenuinelyHugeMaxIntWindow(t *testing.T) {
	t.Parallel()

	// An inclusive range beginning at zero and ending at MaxInt64 spans one past
	// the representable int64 maximum: its window genuinely cannot be materialized,
	// so it must still be rejected even though the singleton MaxInt64..MaxInt64
	// case now succeeds. This guards against a fix that simply stopped rejecting
	// every MaxInt64 endpoint.
	script := compileScript(t, `
    def huge_inclusive(values)
      values.values_at(0..9223372036854775807)
    end
    `)

	values := NewArray([]Value{NewInt(10), NewInt(20), NewInt(30)})
	requireCallErrorContains(t, script, "huge_inclusive", []Value{values}, CallOptions{}, "array.values_at window is too large")
}

func TestArrayValuesAtRangeTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	// A range selector pads positions past the receiver with nil, so a huge
	// window would reserve a backing slice that dwarfs the quota. The projected
	// check rejects the call before the slice is built rather than the
	// statement-level check catching it after the allocation already happened.
	receiver := NewArray([]Value{NewInt(10), NewInt(20), NewInt(30)})
	member, err := arrayMember(receiver, "values_at")
	if err != nil {
		t.Fatalf("arrayMember(values_at): %v", err)
	}
	builtin := valueBuiltin(member)
	if builtin == nil {
		t.Fatalf("array member values_at is not a builtin")
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 8 * 1024}
	hugeRange := NewRange(Range{Start: 0, End: 100_000_000})
	_, err = builtin.Fn(exec, receiver, []Value{hugeRange}, nil, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

func TestArrayValuesAtScalarSelectorsReserveBackingUpFront(t *testing.T) {
	t.Parallel()

	// Many scalar selectors size the result's initial backing slice to their count
	// (bounded by arrayValuesAtInitialCap). When that slot array alone overflows the
	// quota, the build must be rejected before make reserves the backing rather than
	// letting make transiently allocate it and the first emit report the overrun
	// afterward. The empty receiver makes every selected element nil with no payload,
	// so only the reserved slot array can trip the quota, isolating the up-front
	// reservation as the cause.
	receiver := NewArray([]Value{})
	member, err := arrayMember(receiver, "values_at")
	if err != nil {
		t.Fatalf("arrayMember(values_at): %v", err)
	}
	builtin := valueBuiltin(member)
	if builtin == nil {
		t.Fatalf("array member values_at is not a builtin")
	}

	args := make([]Value, 200)
	for i := range args {
		args[i] = NewInt(0)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 4096}
	_, err = builtin.Fn(exec, receiver, args, nil, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)

	// step() runs once per emitted element, so a zero step count proves the build was
	// rejected by the up-front reserveSlots before make allocated the backing and
	// before any element was appended. A reservation deferred until the first emit
	// would have stepped once over the already-allocated slot array.
	if exec.steps != 0 {
		t.Fatalf("values_at stepped %d times before rejecting the backing reservation; want 0 (reservation must precede make)", exec.steps)
	}
}

func TestArrayValuesAtRangeChargesEphemeralReceiver(t *testing.T) {
	t.Parallel()

	// The receiver is reachable only through the call roots (an array literal or
	// capability result invoked immediately), so its payload is invisible to
	// estimateMemoryUsageBase. A range selector pads positions past the receiver
	// with fresh nil slots, and the projected check must charge those slots on top
	// of the live receiver: a quota that admits the result backing alone but not the
	// receiver plus the backing has to be rejected before the padded window
	// materializes. Without seeding the receiver into the projection's baseline the
	// backing could grow another full quota beyond a receiver that already consumes
	// most of it, with the excess only caught after materialization.
	big := func() string { return string(make([]byte, 2000)) }
	receiver := NewArray([]Value{
		NewString(big()),
		NewString(big()),
		NewString(big()),
		NewString(big()),
	})
	member, err := arrayMember(receiver, "values_at")
	if err != nil {
		t.Fatalf("arrayMember(values_at): %v", err)
	}
	builtin := valueBuiltin(member)
	if builtin == nil {
		t.Fatalf("array member values_at is not a builtin")
	}

	// The receiver payload (~8.3 KB) fits under the quota, and the padded window's
	// backing (~6.5 KB for 201 slots) fits under it too, but their sum does not.
	// A projection that ignored the receiver would admit the backing and overrun.
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 12 * 1024}
	paddedRange := NewRange(Range{Start: 0, End: 200})
	_, err = builtin.Fn(exec, receiver, []Value{paddedRange}, nil, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

func TestArraySumChargesEphemeralReceiverWhileAccumulating(t *testing.T) {
	t.Parallel()

	// The receiver is reachable only through the call roots (an array literal or a
	// capability result summed immediately), so its payload is invisible to
	// estimateMemoryUsageBase. sum builds a growing string accumulator on top of
	// that still-live receiver; the per-iteration check must charge the receiver as
	// a call root, not just the accumulator. A quota that admits the final
	// concatenated string alone, and the receiver alone, but not their sum has to be
	// rejected before the accumulator finishes. Without seeding the receiver into the
	// check the accumulator could grow another full receiver beyond the quota, with
	// the excess only caught after the builtin returned.
	const parts = 8
	chunk := string(make([]byte, 1000))
	elements := make([]Value, parts)
	for i := range elements {
		elements[i] = NewString(chunk)
	}
	receiver := NewArray(elements)

	member, err := arrayMember(receiver, "sum")
	if err != nil {
		t.Fatalf("arrayMember(sum): %v", err)
	}
	builtin := valueBuiltin(member)
	if builtin == nil {
		t.Fatalf("array member sum is not a builtin")
	}

	// The receiver payload and the final concatenated accumulator each fit under the
	// quota on their own, but both are live at the last iteration, so their combined
	// footprint must not. Measuring both lets the quota land strictly between the
	// larger single footprint and the sum, where only the receiver-aware check rejects.
	finalString := NewString(string(make([]byte, parts*len(chunk))))
	receiverBytes := newMemoryEstimator().value(receiver)
	finalBytes := newMemoryEstimator().value(finalString)
	larger := max(receiverBytes, finalBytes)
	quota := larger + (receiverBytes+finalBytes-larger)/2
	if quota <= larger {
		t.Fatalf("quota %d does not exceed the larger single footprint %d; widen the chunks", quota, larger)
	}
	if quota >= receiverBytes+finalBytes {
		t.Fatalf("quota %d does not stay below the combined footprint %d; widen the chunks", quota, receiverBytes+finalBytes)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err = builtin.Fn(exec, receiver, []Value{NewString("")}, nil, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

func TestArraySumChargesLiveBlockResultWhileAccumulating(t *testing.T) {
	t.Parallel()

	// sum's block form holds the block result (the contribution) live on the Go
	// stack while arraySumAdd builds the next accumulator from a fresh copy, so a
	// block that returns a large value coexists with the new accumulator at the
	// step's allocation peak. The per-iteration check must charge that contribution,
	// not just the new accumulator: a quota that admits the new accumulator alone but
	// not accumulator + contribution has to be rejected before the builtin returns.
	// Without seeding the contribution the step could allocate a full extra block
	// result beyond the quota, with the excess only caught after the builtin returned.
	//
	// A single-element receiver isolates the contribution: the loop runs one
	// iteration, so the only thing distinguishing the buggy check (accumulator only)
	// from the correct one (accumulator + contribution) is the live block result.
	const initialBytes = 1000
	const contributionBytes = 1000
	receiver := NewArray([]Value{NewInt(0)})
	initial := NewString(string(make([]byte, initialBytes)))
	block := freshStringBlockValue(contributionBytes)

	member, err := arrayMember(receiver, "sum")
	if err != nil {
		t.Fatalf("arrayMember(sum): %v", err)
	}
	builtin := valueBuiltin(member)
	if builtin == nil {
		t.Fatalf("array member sum is not a builtin")
	}

	// At the single iteration's peak three distinct buffers are live: the initial
	// string (the call argument), the contribution (the block result), and the new
	// accumulator (initial + contribution). Measure the footprint the correct check
	// sees (call roots + accumulator + contribution) and the footprint the buggy
	// check saw (call roots + accumulator only), then land the quota strictly
	// between them so only the contribution-aware check rejects.
	contribution := NewString(string(make([]byte, contributionBytes)))
	accumulator := NewString(string(make([]byte, initialBytes+contributionBytes)))
	accumulatorOnly := func() int {
		est := newMemoryEstimator()
		used := est.value(receiver)
		used += est.value(initial)
		used += est.value(block)
		used += est.value(accumulator)
		return used
	}()
	withContribution := func() int {
		est := newMemoryEstimator()
		used := est.value(receiver)
		used += est.value(initial)
		used += est.value(block)
		used += est.value(accumulator)
		used += est.value(contribution)
		return used
	}()
	if accumulatorOnly >= withContribution {
		t.Fatalf("contribution adds no footprint: accumulator-only %d, with-contribution %d", accumulatorOnly, withContribution)
	}
	quota := accumulatorOnly + (withContribution-accumulatorOnly)/2
	if quota <= accumulatorOnly {
		t.Fatalf("quota %d does not exceed the accumulator-only footprint %d; widen the chunks", quota, accumulatorOnly)
	}
	if quota >= withContribution {
		t.Fatalf("quota %d does not stay below the contribution-aware footprint %d; widen the chunks", quota, withContribution)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err = builtin.Fn(exec, receiver, []Value{initial}, nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

func TestArraySumChargesPriorAccumulatorWhileGrowing(t *testing.T) {
	t.Parallel()

	// sum grows a string accumulator across iterations: arraySumAdd builds the next
	// accumulator from a fresh copy of the prior total and the contribution, so at
	// every step the prior total, the contribution, and the new accumulator all
	// coexist on the Go stack. Once the prior total has grown into a large
	// concatenated buffer it is reachable only from that Go-local — it is neither a
	// receiver element nor a call root — so estimateMemoryUsageBase never sees it.
	// The per-iteration check must charge that prior total, not just the new
	// accumulator and the contribution: a quota that admits call roots + new
	// accumulator (the contribution aliases a receiver element here, so it adds
	// nothing) but not call roots + prior total + new accumulator has to be rejected
	// before the builtin returns. Without charging the prior total the step could
	// allocate a full extra accumulator's worth beyond the quota, with the excess only
	// caught after the builtin returned.
	const parts = 8
	const chunkBytes = 1000
	chunk := string(make([]byte, chunkBytes))
	elements := make([]Value, parts)
	for i := range elements {
		elements[i] = NewString(chunk)
	}
	receiver := NewArray(elements)
	seed := NewString("")

	member, err := arrayMember(receiver, "sum")
	if err != nil {
		t.Fatalf("arrayMember(sum): %v", err)
	}
	builtin := valueBuiltin(member)
	if builtin == nil {
		t.Fatalf("array member sum is not a builtin")
	}

	// The peak is the final iteration: the prior total spans the first parts-1
	// elements, the new accumulator spans all parts, and the contribution is the last
	// element (which aliases a receiver element, so it dedups against the receiver and
	// adds nothing). Measure the footprint the buggy check saw (call roots + new
	// accumulator, no prior total) and the footprint the correct check sees (plus the
	// prior total), then land the quota strictly between them so only the
	// prior-total-aware check rejects.
	priorTotal := NewString(string(make([]byte, (parts-1)*chunkBytes)))
	newAccumulator := NewString(string(make([]byte, parts*chunkBytes)))
	without := func() int {
		est := newMemoryEstimator()
		used := est.value(receiver)
		used += est.value(seed)
		used += est.value(newAccumulator)
		return used
	}()
	with := func() int {
		est := newMemoryEstimator()
		used := est.value(receiver)
		used += est.value(seed)
		used += est.value(newAccumulator)
		used += est.value(priorTotal)
		return used
	}()
	if without >= with {
		t.Fatalf("prior total adds no footprint: without %d, with %d", without, with)
	}
	quota := without + (with-without)/2
	if quota <= without {
		t.Fatalf("quota %d does not exceed the prior-total-free footprint %d; widen the chunks", quota, without)
	}
	if quota >= with {
		t.Fatalf("quota %d does not stay below the prior-total-aware footprint %d; widen the chunks", quota, with)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err = builtin.Fn(exec, receiver, []Value{seed}, nil, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

func TestArrayValuesAtRangeTripsStepQuota(t *testing.T) {
	t.Parallel()

	// A huge range selector pads positions past the receiver with nil one slot at
	// a time. With the memory quota disabled the up-front projection passes, so the
	// per-element step() is what bounds the materialization: a small step quota
	// must abort the call instead of running through every padded position.
	receiver := NewArray([]Value{NewInt(10), NewInt(20), NewInt(30)})
	member, err := arrayMember(receiver, "values_at")
	if err != nil {
		t.Fatalf("arrayMember(values_at): %v", err)
	}
	builtin := valueBuiltin(member)
	if builtin == nil {
		t.Fatalf("array member values_at is not a builtin")
	}

	exec := &Execution{ctx: context.Background(), quota: 1024, memoryQuota: 0}
	hugeRange := NewRange(Range{Start: 0, End: 100_000_000})
	_, err = builtin.Fn(exec, receiver, []Value{hugeRange}, nil, NewNil())
	requireErrorIs(t, err, errStepQuotaExceeded)
}

func TestArrayValuesAtRangeHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	// step() polls cancellation on its first invocation, so a canceled context
	// aborts the expansion before the huge padded window materializes, even with
	// both quotas effectively disabled.
	receiver := NewArray([]Value{NewInt(10), NewInt(20), NewInt(30)})
	member, err := arrayMember(receiver, "values_at")
	if err != nil {
		t.Fatalf("arrayMember(values_at): %v", err)
	}
	builtin := valueBuiltin(member)
	if builtin == nil {
		t.Fatalf("array member values_at is not a builtin")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exec := &Execution{ctx: ctx, quota: 1 << 30, memoryQuota: 0}
	hugeRange := NewRange(Range{Start: 0, End: 100_000_000})
	_, err = builtin.Fn(exec, receiver, []Value{hugeRange}, nil, NewNil())
	requireErrorIs(t, err, context.Canceled)
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

func TestArraySum(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want Value
	}{
		{
			name: "plain integer sum",
			body: `[1, 2, 3].sum()`,
			want: NewInt(6),
		},
		{
			name: "empty array sums to zero",
			body: `[].sum()`,
			want: NewInt(0),
		},
		{
			name: "mixed numeric promotes to float",
			body: `[1, 2.5].sum()`,
			want: NewFloat(3.5),
		},
		{
			name: "initial value seeds the accumulator",
			body: `[1, 2, 3].sum(10)`,
			want: NewInt(16),
		},
		{
			name: "initial value used when array is empty",
			body: `[].sum(5)`,
			want: NewInt(5),
		},
		{
			name: "float initial promotes the result",
			body: `[1, 2, 3].sum(0.5)`,
			want: NewFloat(6.5),
		},
		{
			name: "block adds each transformed element",
			body: `[1, 2, 3].sum { |n| n * 2 }`,
			want: NewInt(12),
		},
		{
			name: "block maps non-numeric elements to numbers",
			body: `["a", "bb", "ccc"].sum { |s| s.length() }`,
			want: NewInt(6),
		},
		{
			name: "initial and block combine",
			body: `[1, 2, 3].sum(10) { |n| n * 2 }`,
			want: NewInt(22),
		},
		{
			name: "string initial concatenates elements",
			body: `["a", "b", "c"].sum("")`,
			want: NewString("abc"),
		},
		{
			name: "string initial with block concatenates block results",
			body: `["a", "b", "c"].sum("") { |s| s.upcase() }`,
			want: NewString("ABC"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, "def run()\n  "+tt.body+"\nend\n")
			got := callFunc(t, script, "run", nil)
			if !got.Equal(tt.want) {
				t.Fatalf("%s = %#v, want %#v", tt.body, got, tt.want)
			}
		})
	}
}

func TestArraySumErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "default initial cannot add strings",
			body: `["a", "b"].sum()`,
			want: "array.sum cannot add incompatible values",
		},
		{
			name: "numeric initial cannot add a string element",
			body: `[1].sum("a")`,
			want: "array.sum cannot add incompatible values",
		},
		{
			name: "block result type must be compatible",
			body: `[1].sum("x") { |n| n }`,
			want: "array.sum cannot add incompatible values",
		},
		{
			name: "rejects more than one positional argument",
			body: `[1].sum(1, 2)`,
			want: "array.sum accepts at most an initial value",
		},
		{
			name: "rejects keyword arguments",
			body: `[1].sum(foo: 1)`,
			want: "array.sum does not take keyword arguments",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, "def run()\n  "+tt.body+"\nend\n")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.want)
		})
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

// TestArrayFirstLastArity confirms first and last keep their zero-argument and
// single-count behavior while rejecting extra positional arguments and any
// keyword arguments, matching Ruby's Array#first/Array#last arity of 0..1.
func TestArrayFirstLastArity(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def first_default()
      [1, 2, 3].first
    end

    def first_count(n)
      [1, 2, 3].first(n)
    end

    def first_extra()
      [1, 2, 3].first(1, 2)
    end

    def first_kwarg()
      [1, 2, 3].first(n: 2)
    end

    def first_count_kwarg()
      [1, 2, 3].first(1, n: 2)
    end

    def last_default()
      [1, 2, 3].last
    end

    def last_count(n)
      [1, 2, 3].last(n)
    end

    def last_extra()
      [1, 2, 3].last(1, 2)
    end

    def last_kwarg()
      [1, 2, 3].last(n: 2)
    end

    def last_count_kwarg()
      [1, 2, 3].last(1, n: 2)
    end
    `)

	if got := callFunc(t, script, "first_default", nil); !got.Equal(NewInt(1)) {
		t.Fatalf("first (no args) mismatch: want 1, got %#v", got)
	}
	if got := callFunc(t, script, "last_default", nil); !got.Equal(NewInt(3)) {
		t.Fatalf("last (no args) mismatch: want 3, got %#v", got)
	}

	countCases := []struct {
		name string
		fn   string
		arg  int64
		want []Value
	}{
		{name: "first zero count", fn: "first_count", arg: 0, want: nil},
		{name: "first single count", fn: "first_count", arg: 2, want: []Value{NewInt(1), NewInt(2)}},
		{name: "last zero count", fn: "last_count", arg: 0, want: nil},
		{name: "last single count", fn: "last_count", arg: 2, want: []Value{NewInt(2), NewInt(3)}},
	}
	for _, tc := range countCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, []Value{NewInt(tc.arg)})
			compareArrays(t, got, tc.want)
		})
	}

	requireCallErrorContains(t, script, "first_extra", nil, CallOptions{}, "array.first accepts at most one count")
	requireCallErrorContains(t, script, "last_extra", nil, CallOptions{}, "array.last accepts at most one count")
	requireCallErrorContains(t, script, "first_kwarg", nil, CallOptions{}, "array.first does not take keyword arguments")
	requireCallErrorContains(t, script, "first_count_kwarg", nil, CallOptions{}, "array.first does not take keyword arguments")
	requireCallErrorContains(t, script, "last_kwarg", nil, CallOptions{}, "array.last does not take keyword arguments")
	requireCallErrorContains(t, script, "last_count_kwarg", nil, CallOptions{}, "array.last does not take keyword arguments")
}

// TestArrayIndexFamily covers the Ruby-aligned value and block forms of
// Array#index, Array#find_index, and Array#rindex (issue #487).
func TestArrayIndexFamily(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want Value
	}{
		// index(value): first matching index, with Vibescript's offset extension.
		{name: "index value hit", expr: "[1, 2, 3, 2].index(2)", want: NewInt(1)},
		{name: "index value miss", expr: "[1, 2, 3].index(9)", want: NewNil()},
		{name: "index value offset", expr: "[1, 2, 3, 2].index(2, 2)", want: NewInt(3)},
		// index { block }: first index whose block result is truthy.
		{name: "index block hit", expr: "[1, 2, 3].index { |x| x > 1 }", want: NewInt(1)},
		{name: "index block miss", expr: "[1, 2, 3].index { |x| x > 9 }", want: NewNil()},
		{name: "index block empty", expr: "[].index { |x| x > 0 }", want: NewNil()},
		// find_index mirrors index exactly.
		{name: "find_index value hit", expr: "[1, 2, 3].find_index(2)", want: NewInt(1)},
		{name: "find_index value miss", expr: "[1, 2, 3].find_index(9)", want: NewNil()},
		{name: "find_index block hit", expr: "[1, 2, 3].find_index { |x| x > 1 }", want: NewInt(1)},
		{name: "find_index block miss", expr: "[1, 2, 3].find_index { |x| x > 9 }", want: NewNil()},
		// rindex(value): last matching index, scanning backward.
		{name: "rindex value hit", expr: "[1, 2, 3, 2].rindex(2)", want: NewInt(3)},
		{name: "rindex value miss", expr: "[1, 2, 3].rindex(9)", want: NewNil()},
		{name: "rindex value offset", expr: "[1, 2, 3, 2].rindex(2, 2)", want: NewInt(1)},
		// rindex { block }: last index whose block result is truthy.
		{name: "rindex block hit", expr: "[1, 2, 3, 2].rindex { |x| x == 2 }", want: NewInt(3)},
		{name: "rindex block miss", expr: "[1, 2, 3].rindex { |x| x > 9 }", want: NewNil()},
		{name: "rindex block empty", expr: "[].rindex { |x| x > 0 }", want: NewNil()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run() "+tt.expr+" end")
			got := callFunc(t, script, "run", nil)
			if !got.Equal(tt.want) {
				t.Fatalf("%s = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

// TestArrayIndexFamilyErrors covers the rejected argument shapes for the index
// family: missing value/block, mixed value+block, and invalid offsets.
func TestArrayIndexFamilyErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "index no args", expr: "[1, 2, 3].index", want: "array.index expects a value (with optional offset) or a block"},
		{name: "index value and block", expr: "[1, 2, 3].index(2) { |x| x > 1 }", want: "array.index takes a value or a block, not both"},
		{name: "index negative offset", expr: "[1, 2, 3].index(2, -1)", want: "array.index offset must be non-negative integer"},
		{name: "find_index no args", expr: "[1, 2, 3].find_index", want: "array.find_index expects a value (with optional offset) or a block"},
		{name: "find_index value and block", expr: "[1, 2, 3].find_index(2) { |x| x > 1 }", want: "array.find_index takes a value or a block, not both"},
		{name: "rindex no args", expr: "[1, 2, 3].rindex", want: "array.rindex expects a value (with optional offset) or a block"},
		{name: "rindex value and block", expr: "[1, 2, 3].rindex(2) { |x| x > 1 }", want: "array.rindex takes a value or a block, not both"},
		{name: "rindex negative offset", expr: "[1, 2, 3].rindex(2, -1)", want: "array.rindex offset must be non-negative integer"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run() "+tt.expr+" end")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.want)
		})
	}
}

// TestArrayFetch covers the Ruby-aligned fetch contract: present index returns
// the element (including negative offsets), missing index falls back to an
// explicit default or a block, the block receives the requested index, and a
// block supersedes a default value argument when both are supplied.
func TestArrayFetch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want Value
	}{
		{name: "in-bounds index", expr: "[1, 2, 3].fetch(1)", want: NewInt(2)},
		{name: "negative index", expr: "[1, 2, 3].fetch(-1)", want: NewInt(3)},
		{name: "missing uses default", expr: "[1, 2, 3].fetch(9, 7)", want: NewInt(7)},
		{name: "missing evaluates block", expr: "[1, 2, 3].fetch(9) { |idx| idx + 10 }", want: NewInt(19)},
		{name: "negative miss evaluates block", expr: "[1, 2, 3].fetch(-9) { |idx| idx }", want: NewInt(-9)},
		{name: "present index skips block", expr: "[1, 2, 3].fetch(0) { |idx| -1 }", want: NewInt(1)},
		{name: "block supersedes default on miss", expr: "[1, 2, 3].fetch(9, 7) { |idx| idx + 10 }", want: NewInt(19)},
		{name: "present index ignores default and block", expr: "[1, 2, 3].fetch(0, 7) { |idx| -1 }", want: NewInt(1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run() "+tt.expr+" end")
			got := callFunc(t, script, "run", nil)
			if !got.Equal(tt.want) {
				t.Fatalf("%s = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

// TestArrayFetchErrors covers the rejected shapes for fetch: an out-of-range
// index with no fallback, a non-integer index, and too many arguments.
func TestArrayFetchErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "missing without fallback", expr: "[1, 2, 3].fetch(9)", want: "array.fetch index 9 outside of array bounds: -3...3"},
		{name: "negative miss without fallback", expr: "[1, 2, 3].fetch(-9)", want: "array.fetch index -9 outside of array bounds: -3...3"},
		{name: "non-integer index", expr: `[1, 2, 3].fetch("x")`, want: "array.fetch index must be integer"},
		{name: "fractional index", expr: "[1, 2, 3].fetch(1.5)", want: "array.fetch index must be integer"},
		{name: "too many arguments", expr: "[1, 2, 3].fetch(0, 1, 2)", want: "array.fetch expects index and optional default"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run() "+tt.expr+" end")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.want)
		})
	}
}

func TestArrayAppendAndPrepend(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def append_call(values, a, b)
      values.append(a, b)
    end

    def append_none(values)
      values.append
    end

    def prepend_call(values, a, b)
      values.prepend(a, b)
    end

    def prepend_one(values, a)
      values.prepend(a)
    end

    def prepend_none(values)
      values.prepend
    end
    `)

	tests := []struct {
		name     string
		function string
		args     []Value
		want     []Value
	}{
		{
			name:     "append adds values in order to the end",
			function: "append_call",
			args:     []Value{NewArray([]Value{NewInt(1)}), NewInt(2), NewInt(3)},
			want:     []Value{NewInt(1), NewInt(2), NewInt(3)},
		},
		{
			name:     "append with no values returns the array unchanged",
			function: "append_none",
			args:     []Value{NewArray([]Value{NewInt(1), NewInt(2)})},
			want:     []Value{NewInt(1), NewInt(2)},
		},
		{
			name:     "prepend inserts values in order at the front",
			function: "prepend_call",
			args:     []Value{NewArray([]Value{NewInt(3)}), NewInt(1), NewInt(2)},
			want:     []Value{NewInt(1), NewInt(2), NewInt(3)},
		},
		{
			name:     "prepend a single value at the front",
			function: "prepend_one",
			args:     []Value{NewArray([]Value{NewInt(2)}), NewInt(1)},
			want:     []Value{NewInt(1), NewInt(2)},
		},
		{
			name:     "prepend onto an empty array",
			function: "prepend_call",
			args:     []Value{NewArray([]Value{}), NewInt(1), NewInt(2)},
			want:     []Value{NewInt(1), NewInt(2)},
		},
		{
			name:     "prepend with no values returns the array unchanged",
			function: "prepend_none",
			args:     []Value{NewArray([]Value{NewInt(1), NewInt(2)})},
			want:     []Value{NewInt(1), NewInt(2)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			compareArrays(t, callFunc(t, script, tt.function, tt.args), tt.want)
		})
	}
}

func TestArrayAppendIsNonMutating(t *testing.T) {
	t.Parallel()
	// append and prepend mirror push: they return a new array and leave the
	// receiver untouched, matching Vibescript's non-mutating collection model.
	script := compileScript(t, `
    def append_preserves_source(values, extra)
      appended = values.append(extra)
      { source: values, appended: appended }
    end

    def prepend_preserves_source(values, extra)
      prepended = values.prepend(extra)
      { source: values, prepended: prepended }
    end
    `)

	appended := callFunc(t, script, "append_preserves_source",
		[]Value{NewArray([]Value{NewInt(1), NewInt(2)}), NewInt(3)}).Hash()
	compareArrays(t, appended["source"], []Value{NewInt(1), NewInt(2)})
	compareArrays(t, appended["appended"], []Value{NewInt(1), NewInt(2), NewInt(3)})

	prepended := callFunc(t, script, "prepend_preserves_source",
		[]Value{NewArray([]Value{NewInt(2), NewInt(3)}), NewInt(1)}).Hash()
	compareArrays(t, prepended["source"], []Value{NewInt(2), NewInt(3)})
	compareArrays(t, prepended["prepended"], []Value{NewInt(1), NewInt(2), NewInt(3)})
}

func TestArrayAppendPrependRejectKeywordArguments(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def append_keyword(values)
      values.append(foo: 1)
    end

    def prepend_keyword(values)
      values.prepend(foo: 1)
    end
    `)

	base := []Value{NewArray([]Value{NewInt(1), NewInt(2)})}
	requireCallErrorContains(t, script, "append_keyword", base, CallOptions{},
		"array.append does not take keyword arguments")
	requireCallErrorContains(t, script, "prepend_keyword", base, CallOptions{},
		"array.prepend does not take keyword arguments")
}

func TestArrayAppendAssignmentReturnsFreshArray(t *testing.T) {
	t.Parallel()
	// append is a documented non-mutating helper. Unlike push, x = x.append(i)
	// must not route through the accumulator fast path that reuses a hidden
	// backing buffer: every append has to return a fresh array so escaped
	// aliases never observe later appends. Accumulation in a loop still works
	// because each iteration starts from the previous (independent) result.
	script := compileScript(t, `
    def append_accumulate(n)
      out = []
      for i in 1..n
        out = out.append(i)
      end
      out
    end

    def append_alias()
      a = [1]
      b = a
      a = a.append(2)
      b[0] = 9
      { a: a, b: b }
    end

    def repeated_append_alias()
      a = []
      a = a.append(1)
      a = a.append(2)
      a = a.append(3)
      b = a
      a = a.append(4)
      b[0] = 9
      { a: a, b: b }
    end
    `)

	want := []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4), NewInt(5)}
	compareArrays(t, callFunc(t, script, "append_accumulate", []Value{NewInt(5)}), want)

	aliased := callFunc(t, script, "append_alias", nil).Hash()
	compareArrays(t, aliased["a"], []Value{NewInt(1), NewInt(2)})
	compareArrays(t, aliased["b"], []Value{NewInt(9)})

	// Several appends grow the result past its exact length before b escapes via
	// b = a, then a appends once more. b must retain [9, 2, 3] and a [1, 2, 3, 4]:
	// append never lets an escaped alias observe a later append.
	repeated := callFunc(t, script, "repeated_append_alias", nil).Hash()
	compareArrays(t, repeated["a"], []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)})
	compareArrays(t, repeated["b"], []Value{NewInt(9), NewInt(2), NewInt(3)})
}

// TestArrayAppendAssignmentStaysOffPushFastPath pins the routing contract behind
// append's non-mutating guarantee. push is the accumulator pattern, so
// x = x.push(i) is allowed to retain a hidden backing buffer on x and reuse it
// for the next push. append is documented non-mutating: routing x = x.append(i)
// through that shared buffer would make the optimization's correctness depend on
// the read-escape guard that drops the buffer whenever x is read elsewhere.
// Excluding append from the fast path removes that dependency, so append must
// never leave a retained append buffer behind. This white-box check fails if
// append is ever re-added to evalArrayAppendAssignment's fast path.
func TestArrayAppendAssignmentStaysOffPushFastPath(t *testing.T) {
	t.Parallel()

	retainedBufferAfter := func(t *testing.T, method string) bool {
		t.Helper()
		script := compileScriptDefault(t, "def run()\n  nil\nend")
		root := newEnv(nil)
		exec := newExecutionForCall(script, context.Background(), root, CallOptions{})
		env := newEnv(root)
		env.Define("a", NewArray([]Value{NewInt(1)}))

		// a = a.<method>(2), driven through the real statement dispatcher so the
		// fast path and its normal-evaluation fallback both behave as in scripts.
		stmt := &AssignStmt{
			Target: &Identifier{Name: "a"},
			Value: &CallExpr{
				Callee: &MemberExpr{
					Object:   &Identifier{Name: "a"},
					Property: method,
				},
				Args: []Expression{&IntegerLiteral{Value: 2}},
			},
		}

		if _, _, err := exec.evalStatement(stmt, env); err != nil {
			t.Fatalf("evalStatement(a = a.%s(2)): %v", method, err)
		}
		got, _ := env.Get("a")
		compareArrays(t, got, []Value{NewInt(1), NewInt(2)})

		_, retained := env.arrayAppendBuffer("a")
		return retained
	}

	if !retainedBufferAfter(t, "push") {
		t.Fatal("push must retain a backing buffer for the accumulator fast path")
	}
	if retainedBufferAfter(t, "append") {
		t.Fatal("append must not retain a fast-path backing buffer; it must return a fresh array")
	}
}
