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
