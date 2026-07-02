package runtime

import (
	"context"
	"errors"
	"testing"
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
	if diff := valuesDiff(want, value.Array()); diff != "" {
		t.Fatalf("array mismatch (-want +got):\n%s", diff)
	}
}

func compareHash(t *testing.T, got, want map[string]Value) {
	t.Helper()
	if diff := valueMapDiff(want, got); diff != "" {
		t.Fatalf("hash mismatch (-want +got):\n%s", diff)
	}
}

func TestHashArbitraryKeys(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run()
  hash = {1 => "one", [1, 2] => "array", "name" => "string", :name => "symbol"}
  hash[[3, 4]] = "assigned"
	  stored = hash.store([5], "stored")
	  copy = hash.dup
	  mixed = { "name" => "string", :name => "symbol" }
	  groups = [1, 2, 3].group_by { |n| n % 2 }
	  tally = [[1], [1], [2]].tally
	  mixed_groups = ["name", :name, "name"].group_by { |key| key }
	  mixed_tally = ["name", :name, "name"].tally
	  pairs = [[1, "pair-int"], [[2], "pair-array"]].to_h
	  {
    int: hash[1],
    array: hash[[1, 2]],
    string_key: hash["name"],
    symbol_key: hash[:name],
	    assigned_array: hash[[3, 4]],
	    stored_array: stored[[5]],
	    copied_int: copy[1],
	    copied_array: copy[[1, 2]],
	    mixed_size: mixed.size,
	    mixed_keys: mixed.keys,
	    mixed_values: mixed.values,
	    odd_group: groups[1],
	    even_group: groups[0],
	    tally_array: tally[[1]],
	    mixed_group_size: mixed_groups.size,
	    mixed_group_string: mixed_groups["name"],
	    mixed_group_symbol: mixed_groups[:name],
	    mixed_tally_size: mixed_tally.size,
	    mixed_tally_string: mixed_tally["name"],
	    mixed_tally_symbol: mixed_tally[:name],
    pair_int: pairs[1],
    pair_array: pairs[[2]]
  }
end`)

	result := callFunc(t, script, "run", nil)
	if result.Kind() != KindHash {
		t.Fatalf("Hash arbitrary key summary kind = %v, want hash", result.Kind())
	}
	got := result.Hash()
	checks := map[string]Value{
		"int":                NewString("one"),
		"array":              NewString("array"),
		"string_key":         NewString("string"),
		"symbol_key":         NewString("symbol"),
		"assigned_array":     NewString("assigned"),
		"stored_array":       NewString("stored"),
		"copied_int":         NewString("one"),
		"copied_array":       NewString("array"),
		"mixed_size":         NewInt(2),
		"tally_array":        NewInt(2),
		"mixed_group_size":   NewInt(2),
		"mixed_tally_size":   NewInt(2),
		"mixed_tally_string": NewInt(2),
		"mixed_tally_symbol": NewInt(1),
		"pair_int":           NewString("pair-int"),
		"pair_array":         NewString("pair-array"),
	}
	for key, want := range checks {
		if got := got[key]; !got.Equal(want) {
			t.Fatalf("arbitrary hash key %s = %s, want %s", key, got.Inspect(), want.Inspect())
		}
	}
	compareArrays(t, got["mixed_keys"], []Value{NewString("name"), NewSymbol("name")})
	compareArrays(t, got["mixed_values"], []Value{NewString("string"), NewString("symbol")})
	compareArrays(t, got["odd_group"], []Value{NewInt(1), NewInt(3)})
	compareArrays(t, got["even_group"], []Value{NewInt(2)})
	compareArrays(t, got["mixed_group_string"], []Value{NewString("name"), NewString("name")})
	compareArrays(t, got["mixed_group_symbol"], []Value{NewSymbol("name")})
}

func TestTypedHashAnnotationsUseOriginalKeyValues(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def int_key(hash: hash<int, string>)
  hash[1]
end

def array_key(hash: hash<array<int>, string>)
  hash[[1, 2]]
end

def reject_symbol(hash: hash<int, string>)
  hash
end

def build_int_hash()
  {1 => "one"}
end

def build_array_hash()
  {[1, 2] => "array"}
end

def build_symbol_hash()
  {a: "symbol"}
end`)

	intHash := callFunc(t, script, "build_int_hash", nil)
	intResult, err := script.Call(context.Background(), "int_key", []Value{intHash}, CallOptions{})
	if err != nil {
		t.Fatalf("int_key({1 => string}) error = %v, want nil", err)
	}
	if !intResult.Equal(NewString("one")) {
		t.Fatalf("int_key({1 => string}) = %s, want one", intResult.Inspect())
	}
	arrayHash := callFunc(t, script, "build_array_hash", nil)
	arrayResult, err := script.Call(context.Background(), "array_key", []Value{arrayHash}, CallOptions{})
	if err != nil {
		t.Fatalf("array_key({[1, 2] => string}) error = %v, want nil", err)
	}
	if !arrayResult.Equal(NewString("array")) {
		t.Fatalf("array_key({[1, 2] => string}) = %s, want array", arrayResult.Inspect())
	}
	symbolHash := callFunc(t, script, "build_symbol_hash", nil)
	_, err = script.Call(context.Background(), "reject_symbol", []Value{symbolHash}, CallOptions{})
	if err == nil {
		t.Fatalf("reject_symbol({a: string}) error = nil, want type mismatch")
	}
}

func TestTypedHashKeysSurviveHashHelpers(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run()
  hash = { "a" => 1, :a => 2 }
  merged = hash.merge({ :a => 3 })
  sliced = hash.slice("a", :a)
  excepted = hash.except(:a)
  selected = hash.select { |key, value| key == "a" }
  rejected = hash.reject { |key, value| key == :a }
  transformed = hash.transform_values { |value| value + 10 }
  compacted = { "a" => 1, :a => nil }.compact
  looped = []
  for pair in hash
    looped = looped.push(pair)
  end
  {
    merge_size: merged.size,
    merge_str: merged["a"],
    merge_sym: merged[:a],
    slice_size: sliced.size,
    slice_str: sliced["a"],
    slice_sym: sliced[:a],
    except_size: excepted.size,
    except_str: excepted["a"],
    except_sym_nil: excepted[:a] == nil,
    select_size: selected.size,
    select_str: selected["a"],
    select_sym_nil: selected[:a] == nil,
    reject_size: rejected.size,
    reject_str: rejected["a"],
    reject_sym_nil: rejected[:a] == nil,
    transform_size: transformed.size,
    transform_str: transformed["a"],
    transform_sym: transformed[:a],
    compact_size: compacted.size,
    compact_str: compacted["a"],
    compact_sym_nil: compacted[:a] == nil,
    looped: looped
  }
end`)

	result := callFunc(t, script, "run", nil)
	if result.Kind() != KindHash {
		t.Fatalf("typed hash helper summary kind = %v, want hash", result.Kind())
	}
	got := result.Hash()
	checks := map[string]Value{
		"merge_size":      NewInt(2),
		"merge_str":       NewInt(1),
		"merge_sym":       NewInt(3),
		"slice_size":      NewInt(2),
		"slice_str":       NewInt(1),
		"slice_sym":       NewInt(2),
		"except_size":     NewInt(1),
		"except_str":      NewInt(1),
		"except_sym_nil":  NewBool(true),
		"select_size":     NewInt(1),
		"select_str":      NewInt(1),
		"select_sym_nil":  NewBool(true),
		"reject_size":     NewInt(1),
		"reject_str":      NewInt(1),
		"reject_sym_nil":  NewBool(true),
		"transform_size":  NewInt(2),
		"transform_str":   NewInt(11),
		"transform_sym":   NewInt(12),
		"compact_size":    NewInt(1),
		"compact_str":     NewInt(1),
		"compact_sym_nil": NewBool(true),
	}
	for key, want := range checks {
		if got := got[key]; !got.Equal(want) {
			t.Fatalf("typed hash helper %s = %s, want %s", key, got.Inspect(), want.Inspect())
		}
	}
	compareArrays(t, got["looped"], []Value{
		NewArray([]Value{NewString("a"), NewInt(1)}),
		NewArray([]Value{NewSymbol("a"), NewInt(2)}),
	})
}

func TestTypedHashMergeDoesNotMaterializeReceiverMirror(t *testing.T) {
	t.Parallel()

	receiver := NewTypedHash(0)
	if err := hashSet(receiver, NewString("a"), NewInt(1)); err != nil {
		t.Fatalf("hashSet(string key) error = %v", err)
	}
	if err := hashSet(receiver, NewSymbol("a"), NewInt(2)); err != nil {
		t.Fatalf("hashSet(symbol key) error = %v", err)
	}
	arg := NewTypedHash(0)
	if err := hashSet(arg, NewSymbol("a"), NewInt(3)); err != nil {
		t.Fatalf("hashSet(arg symbol key) error = %v", err)
	}
	if entries, ok := hashStringMapIfMaterialized(receiver); ok || entries != nil {
		t.Fatalf("typed receiver materialized before merge = %v, %v; want nil, false", entries, ok)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30}
	merged, err := callHashMember(t, exec, receiver, "merge", []Value{arg}, NewNil())
	if err != nil {
		t.Fatalf("typed merge error = %v", err)
	}
	if got, ok, err := hashGet(merged, NewString("a")); err != nil || !ok || !got.Equal(NewInt(1)) {
		t.Fatalf("merged string key = %s, %v, %v; want 1, true, nil", got.Inspect(), ok, err)
	}
	if got, ok, err := hashGet(merged, NewSymbol("a")); err != nil || !ok || !got.Equal(NewInt(3)) {
		t.Fatalf("merged symbol key = %s, %v, %v; want 3, true, nil", got.Inspect(), ok, err)
	}
	if entries, ok := hashStringMapIfMaterialized(receiver); ok || entries != nil {
		t.Fatalf("typed receiver materialized after merge = %v, %v; want nil, false", entries, ok)
	}
}

func TestHostClonePreservesTypedHashKeys(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def build()
  {1 => "one", [1] => "array", "name" => "string", :name => "symbol"}
end

def fetch(hash)
  {
    int: hash[1],
    array: hash[[1]],
    string_key: hash["name"],
    symbol_key: hash[:name]
  }
end`)

	exported := callFunc(t, script, "build", nil)
	result := callFunc(t, script, "fetch", []Value{exported})
	if result.Kind() != KindHash {
		t.Fatalf("fetch summary kind = %v, want hash", result.Kind())
	}
	compareHash(t, result.Hash(), map[string]Value{
		"int":        NewString("one"),
		"array":      NewString("array"),
		"string_key": NewString("string"),
		"symbol_key": NewString("symbol"),
	})
}

func TestHostBackedHashTypedWriteReplacesMatchingLegacyKey(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def rewrite(record)
  record[:array] = []
  {
    size: record.size,
    value: record[:array],
    keys: record.keys
  }
end`)

	record := NewHash(map[string]Value{"array": NewString("old")})
	result := callFunc(t, script, "rewrite", []Value{record})
	if result.Kind() != KindHash {
		t.Fatalf("rewrite summary kind = %v, want hash", result.Kind())
	}
	got := result.Hash()
	if !got["size"].Equal(NewInt(1)) {
		t.Fatalf("record size = %s, want 1", got["size"].Inspect())
	}
	if value := got["value"]; value.Kind() != KindArray || len(value.Array()) != 0 {
		t.Fatalf("record[:array] = %s, want empty array", value.Inspect())
	}
	keys := got["keys"].Array()
	if len(keys) != 1 || !keys[0].Equal(NewSymbol("array")) {
		t.Fatalf("record keys = %s, want [:array]", got["keys"].Inspect())
	}
}

func TestHashMergeAndKeys(t *testing.T) {
	t.Parallel()
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

func TestHashMergeConflictBlock(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def resolve()
      { a: 1, b: 2 }.merge({ a: 10, c: 3 }) do |key, old, new|
        old + new
      end
    end

    def receives_symbol_key()
      seen = nil
      { a: 1 }.merge({ a: 2 }) do |key, old, new|
        seen = key
        old
      end
      seen
    end

    def string_keys()
      { "a": 1 }.merge({ "a": 10, "b": 5 }) do |key, old, new|
        old + new
      end
    end

    def mixed_symbol_string_keys()
      { a: 1 }.merge({ "a": 10 }) do |key, old, new|
        old + new
      end
    end

    def arity_one_block()
      { a: 1, b: 2 }.merge({ a: 9 }) do |key|
        key
      end
    end

    def blockless_incoming_wins()
      { a: 1, b: 2 }.merge({ a: 10 })
    end

    def block_left_unchanged_when_no_conflict()
      { a: 1 }.merge({ b: 2 }) do |key, old, new|
        99
      end
    end
    `)

	tests := []struct {
		name string
		fn   string
		want any
	}{
		{
			name: "conflicting keys call block and non-conflicting keys pass through",
			fn:   "resolve",
			want: map[string]Value{"a": NewInt(11), "b": NewInt(2), "c": NewInt(3)},
		},
		{
			name: "block receives the key as a symbol",
			fn:   "receives_symbol_key",
			want: NewSymbol("a"),
		},
		{
			name: "string keys collide using their string form",
			fn:   "string_keys",
			want: map[string]Value{"a": NewInt(11), "b": NewInt(5)},
		},
		{
			name: "symbol and string keys with the same name stay distinct",
			fn:   "mixed_symbol_string_keys",
			want: map[string]Value{"a": NewInt(10)},
		},
		{
			name: "block with fewer params defaults extra args away",
			fn:   "arity_one_block",
			want: map[string]Value{"a": NewSymbol("a"), "b": NewInt(2)},
		},
		{
			name: "no block keeps the incoming hash winning",
			fn:   "blockless_incoming_wins",
			want: map[string]Value{"a": NewInt(10), "b": NewInt(2)},
		},
		{
			name: "keys present on one side never invoke the block",
			fn:   "block_left_unchanged_when_no_conflict",
			want: map[string]Value{"a": NewInt(1), "b": NewInt(2)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tt.fn, nil)
			switch want := tt.want.(type) {
			case map[string]Value:
				if got.Kind() != KindHash {
					t.Fatalf("expected hash, got %v", got.Kind())
				}
				compareHash(t, got.Hash(), want)
			case Value:
				if !got.Equal(want) {
					t.Fatalf("value mismatch: got %v want %v", got, want)
				}
			default:
				t.Fatalf("unhandled want type %T", tt.want)
			}
		})
	}
}

func TestHashMergeRejectsMisuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{name: "non-hash argument", source: "def run() { a: 1 }.merge(5) end", wantErr: "hash.merge argument 1 must be a hash"},
		{name: "non-hash second argument", source: "def run() { a: 1 }.merge({ b: 2 }, 5) end", wantErr: "hash.merge argument 2 must be a hash"},
		{name: "update non-hash argument", source: "def run() { a: 1 }.update(5) end", wantErr: "hash.update argument 1 must be a hash"},
		{name: "merge! non-hash argument", source: "def run() { a: 1 }.merge!(5) end", wantErr: "hash.merge! argument 1 must be a hash"},
		{name: "merge keyword argument", source: "def run() { a: 1 }.merge(b: 2) end", wantErr: "hash.merge does not accept keyword arguments"},
		{name: "merge parenless keyword argument", source: "def run() { a: 1 }.merge b: 2 end", wantErr: "hash.merge does not accept keyword arguments"},
		{name: "update keyword argument", source: "def run() { a: 1 }.update(b: 2) end", wantErr: "hash.update does not accept keyword arguments"},
		{name: "merge! keyword argument", source: "def run() { a: 1 }.merge!(b: 2) end", wantErr: "hash.merge! does not accept keyword arguments"},
		{name: "merge keyword with positional hash", source: "def run() { a: 1 }.merge({ b: 2 }, c: 3) end", wantErr: "hash.merge does not accept keyword arguments"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.wantErr)
		})
	}
}

func TestHashMergeMultipleHashes(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def three_hashes()
      { a: 1 }.merge({ b: 2 }, { c: 3 })
    end

    def later_wins()
      { a: 1 }.merge({ a: 2 }, { a: 3 })
    end

    def block_folds_each_step()
      { a: 1 }.merge({ a: 2 }, { a: 3 }) do |key, old, new|
        old + new
      end
    end

    def no_arguments_copies()
      { a: 1, b: 2 }.merge()
    end

    def parenless_copies()
      ({ a: 1, b: 2 }).merge
    end

    def receiver_unchanged()
      original = { a: 1 }
      merged = original.merge({ b: 2 }, { c: 3 })
      { original: original, merged: merged }
    end
    `)

	tests := []struct {
		name string
		fn   string
		want any
	}{
		{
			name: "later hashes add their entries",
			fn:   "three_hashes",
			want: map[string]Value{"a": NewInt(1), "b": NewInt(2), "c": NewInt(3)},
		},
		{
			name: "later hashes win on conflicts without a block",
			fn:   "later_wins",
			want: map[string]Value{"a": NewInt(3)},
		},
		{
			name: "block folds the accumulated value across each hash",
			fn:   "block_folds_each_step",
			want: map[string]Value{"a": NewInt(6)},
		},
		{
			name: "no arguments returns a copy of the receiver",
			fn:   "no_arguments_copies",
			want: map[string]Value{"a": NewInt(1), "b": NewInt(2)},
		},
		{
			name: "parenless invocation returns a copy of the receiver",
			fn:   "parenless_copies",
			want: map[string]Value{"a": NewInt(1), "b": NewInt(2)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tt.fn, nil)
			if got.Kind() != KindHash {
				t.Fatalf("expected hash, got %v", got.Kind())
			}
			compareHash(t, got.Hash(), tt.want.(map[string]Value))
		})
	}

	t.Run("receiver is not mutated", func(t *testing.T) {
		t.Parallel()
		result := callFunc(t, script, "receiver_unchanged", nil).Hash()
		compareHash(t, result["original"].Hash(), map[string]Value{"a": NewInt(1)})
		compareHash(t, result["merged"].Hash(), map[string]Value{"a": NewInt(1), "b": NewInt(2), "c": NewInt(3)})
	})
}

func TestHashUpdateAndMergeBangAliases(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def update_returns_new_hash()
      original = { a: 1 }
      updated = original.update({ b: 2 })
      { original: original, updated: updated }
    end

    def update_multiple()
      { a: 1 }.update({ b: 2 }, { c: 3 })
    end

    def update_conflict_block()
      { a: 1 }.update({ a: 2 }) do |key, old, new|
        old + new
      end
    end

    def merge_bang_returns_new_hash()
      original = { a: 1 }
      merged = original.merge!({ a: 5 })
      { original: original, merged: merged }
    end

    def update_parenless_copies()
      ({ a: 1, b: 2 }).update
    end

    def merge_bang_parenless_copies()
      ({ a: 1, b: 2 }).merge!
    end
    `)

	t.Run("update returns a new hash and leaves the receiver unchanged", func(t *testing.T) {
		t.Parallel()
		result := callFunc(t, script, "update_returns_new_hash", nil).Hash()
		compareHash(t, result["original"].Hash(), map[string]Value{"a": NewInt(1)})
		compareHash(t, result["updated"].Hash(), map[string]Value{"a": NewInt(1), "b": NewInt(2)})
	})

	t.Run("update accepts multiple hashes", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "update_multiple", nil)
		compareHash(t, got.Hash(), map[string]Value{"a": NewInt(1), "b": NewInt(2), "c": NewInt(3)})
	})

	t.Run("update honors the conflict block", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "update_conflict_block", nil)
		compareHash(t, got.Hash(), map[string]Value{"a": NewInt(3)})
	})

	t.Run("merge! returns a new hash and leaves the receiver unchanged", func(t *testing.T) {
		t.Parallel()
		result := callFunc(t, script, "merge_bang_returns_new_hash", nil).Hash()
		compareHash(t, result["original"].Hash(), map[string]Value{"a": NewInt(1)})
		compareHash(t, result["merged"].Hash(), map[string]Value{"a": NewInt(5)})
	})

	t.Run("parenless update returns a copy of the receiver", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "update_parenless_copies", nil)
		compareHash(t, got.Hash(), map[string]Value{"a": NewInt(1), "b": NewInt(2)})
	})

	t.Run("parenless merge! returns a copy of the receiver", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "merge_bang_parenless_copies", nil)
		compareHash(t, got.Hash(), map[string]Value{"a": NewInt(1), "b": NewInt(2)})
	})
}

func TestHashReplace(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def replace_returns_replacement()
      original = { a: 1 }
      replaced = original.replace({ b: 2 })
      { original: original, replaced: replaced }
    end

    def replace_with_empty()
      { a: 1, b: 2 }.replace({})
    end
    `)

	t.Run("replace adopts the argument and leaves the receiver unchanged", func(t *testing.T) {
		t.Parallel()
		result := callFunc(t, script, "replace_returns_replacement", nil).Hash()
		compareHash(t, result["original"].Hash(), map[string]Value{"a": NewInt(1)})
		compareHash(t, result["replaced"].Hash(), map[string]Value{"b": NewInt(2)})
	})

	t.Run("replace with an empty hash clears the contents", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "replace_with_empty", nil)
		compareHash(t, got.Hash(), map[string]Value{})
	})
}

func TestHashReplaceRejectsMisuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{name: "no arguments", source: "def run() { a: 1 }.replace() end", wantErr: "hash.replace expects a single hash argument"},
		{name: "too many arguments", source: "def run() { a: 1 }.replace({ b: 2 }, { c: 3 }) end", wantErr: "hash.replace expects a single hash argument"},
		{name: "non-hash argument", source: "def run() { a: 1 }.replace([1, 2]) end", wantErr: "hash.replace expects a single hash argument"},
		{name: "keyword argument", source: "def run() { a: 1 }.replace(b: 2) end", wantErr: "hash.replace does not accept keyword arguments"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.wantErr)
		})
	}
}

func TestHashFlatten(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def default_depth()
      { a: 1, b: 2 }.flatten
    end

    def nested_value_kept_at_default_depth()
      { a: 1, b: [2, 3] }.flatten
    end

    def depth_two_flattens_value_arrays()
      { a: 1, b: [2, 3] }.flatten(2)
    end

    def depth_zero_keeps_pairs()
      { a: 1, b: [2, 3] }.flatten(0)
    end

    def negative_depth_flattens_fully()
      { a: 1, b: [2, [3, 4]] }.flatten(-1)
    end

    def float_depth_truncates()
      { a: 1, b: [2, 3] }.flatten(1.9)
    end

    def empty_hash()
      {}.flatten
    end
    `)

	tests := []struct {
		name string
		fn   string
		want []Value
	}{
		{
			name: "default depth spreads pairs",
			fn:   "default_depth",
			want: []Value{NewSymbol("a"), NewInt(1), NewSymbol("b"), NewInt(2)},
		},
		{
			name: "default depth keeps nested value arrays",
			fn:   "nested_value_kept_at_default_depth",
			want: []Value{NewSymbol("a"), NewInt(1), NewSymbol("b"), NewArray([]Value{NewInt(2), NewInt(3)})},
		},
		{
			name: "depth two flattens value arrays one more level",
			fn:   "depth_two_flattens_value_arrays",
			want: []Value{NewSymbol("a"), NewInt(1), NewSymbol("b"), NewInt(2), NewInt(3)},
		},
		{
			name: "depth zero keeps key-value pairs nested",
			fn:   "depth_zero_keeps_pairs",
			want: []Value{
				NewArray([]Value{NewSymbol("a"), NewInt(1)}),
				NewArray([]Value{NewSymbol("b"), NewArray([]Value{NewInt(2), NewInt(3)})}),
			},
		},
		{
			name: "negative depth flattens completely",
			fn:   "negative_depth_flattens_fully",
			want: []Value{NewSymbol("a"), NewInt(1), NewSymbol("b"), NewInt(2), NewInt(3), NewInt(4)},
		},
		{
			name: "float depth is truncated like Ruby",
			fn:   "float_depth_truncates",
			want: []Value{NewSymbol("a"), NewInt(1), NewSymbol("b"), NewArray([]Value{NewInt(2), NewInt(3)})},
		},
		{
			name: "empty hash flattens to an empty array",
			fn:   "empty_hash",
			want: []Value{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tt.fn, nil)
			compareArrays(t, got, tt.want)
		})
	}
}

func TestHashFlattenRejectsMisuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{name: "too many arguments", source: "def run() { a: 1 }.flatten(1, 2) end", wantErr: "hash.flatten accepts at most one depth argument"},
		{name: "non-integer depth", source: `def run() { a: 1 }.flatten("deep") end`, wantErr: "hash.flatten depth must be integer"},
		{name: "keyword argument", source: "def run() { a: 1 }.flatten(depth: 2) end", wantErr: "hash.flatten does not accept keyword arguments"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.wantErr)
		})
	}
}

func TestQuotedHashLiteralKeys(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def payload()
      {"name": "Ada", "first-name": "Lovelace", active: true}
    end

    def lookups()
      row = {"name": "Ada", "first-name": "Lovelace"}
      {
        symbol_name: row[:name],
        string_name: row["name"],
        hyphenated: row["first-name"]
      }
    end

	    def collision()
	      hash = {name: "symbol", "name": "string"}
	      { size: hash.size, symbol_name: hash[:name], string_name: hash["name"] }
	    end
    `)

	payload := callFunc(t, script, "payload", nil)
	if payload.Kind() != KindHash {
		t.Fatalf("payload() = %s, want hash", payload.Kind())
	}
	compareHash(t, payload.Hash(), map[string]Value{
		"name":       NewString("Ada"),
		"first-name": NewString("Lovelace"),
		"active":     NewBool(true),
	})

	lookups := callFunc(t, script, "lookups", nil)
	if lookups.Kind() != KindHash {
		t.Fatalf("lookups() = %s, want hash", lookups.Kind())
	}
	compareHash(t, lookups.Hash(), map[string]Value{
		"symbol_name": NewNil(),
		"string_name": NewString("Ada"),
		"hyphenated":  NewString("Lovelace"),
	})

	collision := callFunc(t, script, "collision", nil)
	if collision.Kind() != KindHash {
		t.Fatalf("collision() = %s, want hash", collision.Kind())
	}
	compareHash(t, collision.Hash(), map[string]Value{
		"size":        NewInt(2),
		"symbol_name": NewString("symbol"),
		"string_name": NewString("string"),
	})
}

func TestMemberAccessAllowsKeywordNamedHashKeys(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def keyword_members()
      payload = JSON.parse("{\"raise\":1,\"begin\":2,\"rescue\":3,\"ensure\":4,\"export\":5}")
      payload.raise + payload.begin + payload.rescue + payload.ensure + payload.export
    end
    `)

	result := callFunc(t, script, "keyword_members", nil)
	if !result.Equal(NewInt(15)) {
		t.Fatalf("keyword_members = %v, want 15", result)
	}
}

func TestHashMethodNamesWinOverKeys(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def hash_collisions()
      record = { size: "XL", keys: "raw keys", fetch: "raw fetch" }
      {
        size_method: record.size,
        size_key: record[:size],
        keys_method: record.keys,
        keys_key: record[:keys],
        fetch_method: record.fetch(:fetch),
        fetch_key: record[:fetch]
      }
    end

    def object_collisions(record)
      {
        size: record.size,
        keys: record.keys
      }
    end
    `)

	collisions := callFunc(t, script, "hash_collisions", nil).Hash()
	if !collisions["size_method"].Equal(NewInt(3)) {
		t.Fatalf("size_method = %v, want 3", collisions["size_method"])
	}
	if !collisions["size_key"].Equal(NewString("XL")) {
		t.Fatalf("size_key = %v, want XL", collisions["size_key"])
	}
	compareArrays(t, collisions["keys_method"], []Value{NewSymbol("fetch"), NewSymbol("keys"), NewSymbol("size")})
	if !collisions["keys_key"].Equal(NewString("raw keys")) {
		t.Fatalf("keys_key = %v, want raw keys", collisions["keys_key"])
	}
	if !collisions["fetch_method"].Equal(NewString("raw fetch")) {
		t.Fatalf("fetch_method = %v, want raw fetch", collisions["fetch_method"])
	}
	if !collisions["fetch_key"].Equal(NewString("raw fetch")) {
		t.Fatalf("fetch_key = %v, want raw fetch", collisions["fetch_key"])
	}

	object := NewObject(map[string]Value{
		"size": NewString("object size"),
		"keys": NewString("object keys"),
	})
	objectCollisions := callFunc(t, script, "object_collisions", []Value{object}).Hash()
	if !objectCollisions["size"].Equal(NewString("object size")) {
		t.Fatalf("object size = %v, want object size", objectCollisions["size"])
	}
	if !objectCollisions["keys"].Equal(NewString("object keys")) {
		t.Fatalf("object keys = %v, want object keys", objectCollisions["keys"])
	}
}

func TestHashExpandedHelpers(t *testing.T) {
	t.Parallel()
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
        fetch_block: record.fetch(:missing) { |key| key },
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
	if !got["key_symbol"].Bool() || got["key_string"].Bool() || !got["include_symbol"].Bool() {
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
	if !got["fetch_block"].Equal(NewSymbol("missing")) {
		t.Fatalf("fetch_block mismatch: %v", got["fetch_block"])
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
	compareHash(t, slice.Hash(), map[string]Value{"a": NewInt(1)})

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

func TestHashEachBlockArgumentShape(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		params string
		body   string
		want   []Value
	}{
		{
			name:   "single parameter receives the key/value pair",
			params: "|pair|",
			body:   `out = out.push(pair)`,
			want: []Value{
				NewArray([]Value{NewSymbol("a"), NewInt(1)}),
				NewArray([]Value{NewSymbol("b"), NewInt(2)}),
			},
		},
		{
			name:   "destructuring single parameter unpacks the pair",
			params: "|(k, v)|",
			body:   `out = out.push([k, v])`,
			want: []Value{
				NewArray([]Value{NewSymbol("a"), NewInt(1)}),
				NewArray([]Value{NewSymbol("b"), NewInt(2)}),
			},
		},
		{
			name:   "destructuring rest collects the value into an array",
			params: "|(k, *rest)|",
			body:   `out = out.push([k, rest])`,
			want: []Value{
				NewArray([]Value{NewSymbol("a"), NewArray([]Value{NewInt(1)})}),
				NewArray([]Value{NewSymbol("b"), NewArray([]Value{NewInt(2)})}),
			},
		},
		{
			name:   "two parameters receive key and value",
			params: "|key, value|",
			body:   `out = out.push([key, value])`,
			want: []Value{
				NewArray([]Value{NewSymbol("a"), NewInt(1)}),
				NewArray([]Value{NewSymbol("b"), NewInt(2)}),
			},
		},
		{
			name:   "extra parameters receive nil",
			params: "|key, value, extra|",
			body:   `out = out.push([key, value, extra])`,
			want: []Value{
				NewArray([]Value{NewSymbol("a"), NewInt(1), NewNil()}),
				NewArray([]Value{NewSymbol("b"), NewInt(2), NewNil()}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `
				def run()
					out = []
					{ a: 1, b: 2 }.each do `+tt.params+`
						`+tt.body+`
					end
					out
				end
			`)
			got := callFunc(t, script, "run", nil)
			compareArrays(t, got, tt.want)
		})
	}
}

func TestHashEachReturnsReceiver(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
		def run()
			source = { a: 1, b: 2 }
			source.each do |pair|
			end
		end
	`)
	got := callFunc(t, script, "run", nil)
	if got.Kind() != KindHash {
		t.Fatalf("each return = %v, want hash receiver", got.Kind())
	}
	compareHash(t, got.Hash(), map[string]Value{"a": NewInt(1), "b": NewInt(2)})
}

func TestHashEachRejectsArguments(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
		def run()
			{ a: 1 }.each(:extra) do |pair|
			end
		end
	`)
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "hash.each does not take arguments")
}

// TestHashEachEmptySingleParamFitsCallRoots pins that an empty receiver iterated by
// a single-parameter block allocates no [key, value] pair, so a quota sized to the
// bare call roots (an empty receiver heaps no sorted-key buffer) must admit the
// walk. The collapsed-pair reservation must not charge a phantom pair for a hash the
// loop never iterates.
func TestHashEachEmptySingleParamFitsCallRoots(t *testing.T) {
	t.Parallel()

	receiver := NewHash(map[string]Value{})
	block := func() Value {
		pos := Position{Line: 1, Column: 1}
		params := []Param{{Name: "pair", Kind: ParamNormal}}
		body := []Statement{&ExprStmt{Expr: &Identifier{Name: "pair", Position: pos}, Position: pos}}
		return NewBlock(params, body, newEnv(nil))
	}()

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	roots := probe.hashCallRootBytes(receiver, nil, nil, block)

	// At exactly the call roots the empty walk must fit (no pair is allocated). The
	// probe and exec share the same (nil) root env, so the measured roots match the
	// base the walk charges.
	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: roots}
	if _, err := callHashMember(t, exec, receiver, "each", nil, block); err != nil {
		t.Fatalf("{}.each at the exact call-roots quota %d = %v, want success", roots, err)
	}

	// ...while one byte below the call roots still rejects, proving the success above
	// is the roots fitting exactly and not an unbounded short circuit.
	if roots > 0 {
		tight := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: roots - 1}
		if _, err := callHashMember(t, tight, receiver, "each", nil, block); !errors.Is(err, errMemoryQuotaExceeded) {
			t.Fatalf("{}.each one byte below the call roots = %v, want errMemoryQuotaExceeded", err)
		}
	}
}

// TestHashEachSingleParamPairChargedAgainstQuota verifies that the [key, value]
// pair Hash#each materializes for a single-parameter block is charged against the
// memory quota. The single-parameter form allocates a fresh pair array per entry
// while the two-parameter form binds the key and value directly and allocates
// nothing extra, so at a quota that exactly admits the two-parameter walk the
// single-parameter walk must trip the limit by the pair's footprint. Without that
// charge a large receiver could yield many uncharged pair arrays and escape the
// sandbox memory bound.
func TestHashEachSingleParamPairChargedAgainstQuota(t *testing.T) {
	t.Parallel()

	entries := make(map[string]Value, 200)
	for i := range 200 {
		entries["k"+string(rune('a'+i%26))+string(rune('a'+i/26))] = NewInt(int64(i))
	}
	receiver := NewHash(entries)

	runEach := func(params string, quota int) error {
		source := `def run(h)
			acc = 0
			h.each do ` + params + `
				acc = acc + 1
			end
			acc
		end`
		script := compileScriptWithConfig(t, Config{StepQuota: 5_000_000, MemoryQuotaBytes: quota}, source)
		_, err := script.Call(context.Background(), "run", []Value{receiver}, CallOptions{})
		return err
	}

	// Find the smallest quota that admits the two-parameter walk (no pair
	// allocation). Searching keeps the test robust to changes in the estimator
	// constants rather than pinning a fragile byte value.
	minTwoParamQuota := 0
	for quota := 5_000; quota <= 200_000; quota++ {
		if runEach("|key, value|", quota) == nil {
			minTwoParamQuota = quota
			break
		}
	}
	if minTwoParamQuota == 0 {
		t.Fatal("two-parameter hash.each never fit within the searched quota range")
	}

	// The two-parameter form passes at its minimum quota...
	if err := runEach("|key, value|", minTwoParamQuota); err != nil {
		t.Fatalf("two-parameter hash.each = %v, want success at quota %d", err, minTwoParamQuota)
	}

	// ...while the single-parameter form, which allocates a pair per entry, must
	// trip the memory limit at that same quota.
	err := runEach("|pair|", minTwoParamQuota)
	if err == nil {
		t.Fatalf("single-parameter hash.each unexpectedly fit within quota %d; pair array not charged", minTwoParamQuota)
	}
	requireErrorContains(t, err, "memory quota exceeded")
	requireRuntimeErrorType(t, err, runtimeErrorTypeLimit)
}

// TestHashEachDestructureRestGrownEntryBoundedByQuota exercises an end-to-end
// destructure-rest walk under a memory quota. A destructuring block with a rest
// target (|(k, (head, *tail))|) grows a not-yet-visited entry the walk reaches a
// later iteration (h[:c] = big_array while binding :a, over sorted keys a < b < c).
// Binding :c then makes AssignDestructure collect a tail rest array sized to the
// grown value inside callBlock; while that binding is live the block body's memory
// check charges it against the quota. The walk must therefore stay bounded: a quota
// below the walk's true peak trips, and a quota at or above it completes and leaves
// :c destructured from its live (mutated) value.
func TestHashEachDestructureRestGrownEntryBoundedByQuota(t *testing.T) {
	t.Parallel()

	// {a: [], b: [], c: []}: sorted keys let the :a iteration grow the later :c entry
	// before the walk reaches and destructures it.
	makeReceiver := func() Value {
		return NewHash(map[string]Value{
			"a": NewArray(nil),
			"b": NewArray(nil),
			"c": NewArray(nil),
		})
	}

	source := `def run(h)
		h.each do |(k, (head, *tail))|
			if k == :a
				h[:c] = (1..20000).to_a
			end
		end
		h
	end`

	run := func(quota int) (Value, error) {
		script := compileScriptWithConfig(t, Config{StepQuota: 5_000_000, MemoryQuotaBytes: quota}, source)
		return script.Call(context.Background(), "run", []Value{makeReceiver()}, CallOptions{})
	}

	// Binary-search the exact smallest quota that admits the whole walk (the grow plus
	// the grown tail rest array :c's destructure later collects). Searching keeps the
	// test robust to estimator constants rather than pinning a fragile byte value.
	const lowReject, highAdmit = 50_000, 8_000_000
	if _, err := run(lowReject); err == nil {
		t.Fatalf("walk fit at the low search bound %d; lower the bound to bracket the threshold", lowReject)
	}
	if _, err := run(highAdmit); err != nil {
		t.Fatalf("walk never fit by the high search bound %d: %v", highAdmit, err)
	}
	lo, hi := lowReject, highAdmit
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if _, err := run(mid); err == nil {
			hi = mid
		} else {
			lo = mid
		}
	}
	minQuota := hi

	// One byte below the exact threshold must reject: the walk's peak (the grown :c
	// value plus the equally large tail rest array its destructure collects) no longer
	// fits, so the in-body memory check trips while binding :c.
	_, err := run(minQuota - 1)
	requireErrorContains(t, err, "memory quota exceeded")
	requireRuntimeErrorType(t, err, runtimeErrorTypeLimit)

	// Safety twin: at the floor the walk completes and leaves :c grown, proving the
	// rejection above is quota tightness, not a categorical failure, and that binding
	// :c destructured the live (mutated) value.
	got, err := run(minQuota)
	if err != nil {
		t.Fatalf("destructure-rest walk at its floor quota %d = %v, want success", minQuota, err)
	}
	if got.Kind() != KindHash {
		t.Fatalf("walk returned %v, want the receiver hash", got.Kind())
	}
	grownC := got.Hash()["c"]
	if grownC.Kind() != KindArray || len(grownC.Array()) != 20000 {
		t.Fatalf("entry :c after the walk = %v with %d elements, want a 20000-element array", grownC.Kind(), len(grownC.Array()))
	}
}

func TestHashFetchValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []Value
	}{
		{
			name:   "returns values in requested order",
			source: `def run() { a: 1, b: 2, c: 3 }.fetch_values(:c, :a) end`,
			want:   []Value{NewInt(3), NewInt(1)},
		},
		{
			name:   "no keys yields empty array",
			source: `def run() { a: 1 }.fetch_values() end`,
			want:   []Value{},
		},
		{
			name:   "block supplies values for missing keys",
			source: `def run() { a: 1 }.fetch_values(:a, :missing) { |key| key } end`,
			want:   []Value{NewInt(1), NewSymbol("missing")},
		},
		{
			name:   "block only runs for absent keys",
			source: `def run() { a: 1, b: 2 }.fetch_values(:a, :b) { |key| 0 } end`,
			want:   []Value{NewInt(1), NewInt(2)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			compareArrays(t, callFunc(t, script, "run", nil), tt.want)
		})
	}
}

func TestHashFetchValuesErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name:    "missing symbol key without block raises",
			source:  `def run() { a: 1 }.fetch_values(:a, :missing) end`,
			wantErr: "hash.fetch_values key not found: :missing",
		},
		{
			name:    "missing string key without block raises",
			source:  `def run() { a: 1 }.fetch_values("missing") end`,
			wantErr: `hash.fetch_values key not found: "missing"`,
		},
		{
			name:    "string key distinct from symbol key raises",
			source:  `def run() { a: 1 }.fetch_values("a") end`,
			wantErr: `hash.fetch_values key not found: "a"`,
		},
		{
			name:    "unsupported key type rejected",
			source:  `def run() { a: 1 }.fetch_values({ bad: 1 }) end`,
			wantErr: "hash.fetch_values key is unsupported hash key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.wantErr)
		})
	}
}

func TestHashFetch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   Value
	}{
		{
			name:   "present key returns value",
			source: `def run() { a: 1, b: 2 }.fetch(:b) end`,
			want:   NewInt(2),
		},
		{
			name:   "missing key uses explicit default",
			source: `def run() { a: 1 }.fetch(:missing, 99) end`,
			want:   NewInt(99),
		},
		{
			name:   "missing key evaluates block default",
			source: `def run() { a: 1 }.fetch(:missing) { |key| key } end`,
			want:   NewSymbol("missing"),
		},
		{
			name:   "present key skips block",
			source: `def run() { a: 1 }.fetch(:a) { |key| :unused } end`,
			want:   NewInt(1),
		},
		{
			name:   "block supersedes default on miss",
			source: `def run() { a: 1 }.fetch(:missing, 99) { |key| key } end`,
			want:   NewSymbol("missing"),
		},
		{
			name:   "present key ignores default and block",
			source: `def run() { a: 1 }.fetch(:a, 99) { |key| :unused } end`,
			want:   NewInt(1),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			got := callFunc(t, script, "run", nil)
			if !got.Equal(tt.want) {
				t.Fatalf("fetch = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHashFetchErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name:    "missing symbol key without default or block raises",
			source:  `def run() { a: 1 }.fetch(:missing) end`,
			wantErr: "hash.fetch key not found: :missing",
		},
		{
			name:    "missing string key without default or block raises",
			source:  `def run() { a: 1 }.fetch("missing") end`,
			wantErr: `hash.fetch key not found: "missing"`,
		},
		{
			name:    "unsupported key type rejected",
			source:  `def run() { a: 1 }.fetch({ bad: 1 }) end`,
			wantErr: "hash.fetch key is unsupported hash key",
		},
		{
			name:    "too many positional arguments rejected",
			source:  `def run() { a: 1 }.fetch(:a, 1, 2) end`,
			wantErr: "hash.fetch expects key and optional default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.wantErr)
		})
	}
}

func TestHashHelpersSupportObjectReceiver(t *testing.T) {
	t.Parallel()
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

func TestHashRemapKeysTypedMappingRemapsLegacyReceiver(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run(values)
  values.remap_keys({ old: :new })
end
`)

	receiver := NewHash(map[string]Value{"old": NewInt(7)})
	got := callFunc(t, script, "run", []Value{receiver})
	renamed, ok, err := hashGet(got, NewSymbol("new"))
	if err != nil {
		t.Fatalf("hashGet(:new) error = %v, want nil", err)
	}
	if !ok || !renamed.Equal(NewInt(7)) {
		t.Fatalf("hash.remap_keys legacy receiver mapped :new = %v, %v; want 7, true", renamed, ok)
	}
	if old, ok, err := hashGet(got, NewString("old")); err != nil {
		t.Fatalf("hashGet(\"old\") error = %v, want nil", err)
	} else if ok {
		t.Fatalf("hash.remap_keys legacy receiver kept old key = %v, want missing", old)
	}
}

func TestHashLiteralHashRockets(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run(key)
  h = { :name => "symbol", "name" => "string", key => "expr" }
  [h[:name], h["name"], h[key]]
end`)
	got := callFunc(t, script, "run", []Value{NewInt(1)})
	compareArrays(t, got, []Value{NewString("symbol"), NewString("string"), NewString("expr")})
}

func TestTypedHashDisplayCollisionsKeepDistinctSemanticEntries(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run()
  left = { :a => 1, "a" => 2 }
  right = { :a => 9, "a" => 2 }
  {
    equal: left == right,
    json: JSON.stringify(left)
  }
end`)

	got := callFunc(t, script, "run", nil)
	if got.Kind() != KindHash {
		t.Fatalf("typed collision summary kind = %v, want hash", got.Kind())
	}
	compareHash(t, got.Hash(), map[string]Value{
		"equal": NewBool(false),
		"json":  NewString(`{"a":2,"a":1}`),
	})
}

func TestFloatHashKeysNormalizeSignedZero(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run()
  h = { 0.0 => "positive", -0.0 => "negative" }
  {
    size: h.size,
    positive: h[0.0],
    negative: h[-0.0]
  }
end`)

	got := callFunc(t, script, "run", nil)
	if got.Kind() != KindHash {
		t.Fatalf("signed zero summary kind = %v, want hash", got.Kind())
	}
	compareHash(t, got.Hash(), map[string]Value{
		"size":     NewInt(1),
		"positive": NewString("negative"),
		"negative": NewString("negative"),
	})
}

func TestHashRejectsUnsupportedKeyTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "literal", expr: `{ {a: 1} => 2 }`, want: "unsupported hash key type hash"},
		{name: "index assignment", expr: `h = {}; h[{a: 1}] = 2`, want: "unsupported hash key type hash"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tt.expr+"\nend")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.want)
		})
	}
}

func TestHashMembershipPredicatesAcceptAnyCandidateKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		key    string
		want   bool
	}{
		{name: "key? present symbol", method: "key?", key: ":a", want: true},
		{name: "key? string distinct from symbol", method: "key?", key: `"a"`, want: false},
		{name: "key? absent symbol", method: "key?", key: ":missing", want: false},
		{name: "key? integer candidate", method: "key?", key: "1", want: false},
		{name: "key? float candidate", method: "key?", key: "1.5", want: false},
		{name: "key? bool candidate", method: "key?", key: "true", want: false},
		{name: "key? nil candidate", method: "key?", key: "nil", want: false},
		{name: "key? array candidate", method: "key?", key: "[1]", want: false},
		{name: "has_key? present symbol", method: "has_key?", key: ":a", want: true},
		{name: "has_key? integer candidate", method: "has_key?", key: "1", want: false},
		{name: "include? present symbol", method: "include?", key: ":a", want: true},
		{name: "include? integer candidate", method: "include?", key: "1", want: false},
		{name: "include? array candidate", method: "include?", key: "[:a]", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			source := "def run() { a: 1 }." + tt.method + "(" + tt.key + ") end"
			script := compileScript(t, source)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindBool {
				t.Fatalf("expected bool, got %v", result.Kind())
			}
			if result.Bool() != tt.want {
				t.Fatalf("%s(%s) = %v, want %v", tt.method, tt.key, result.Bool(), tt.want)
			}
		})
	}
}

func TestHashMembershipPredicatesRejectWrongArity(t *testing.T) {
	t.Parallel()

	for _, method := range []string{"key?", "has_key?", "member?", "include?"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run() { a: 1 }."+method+"(:a, :b) end")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "expects exactly one key")
		})
	}
}

func TestHashMemberAliasMatchesKeyPredicate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  string
		want bool
	}{
		{name: "present symbol", key: ":a", want: true},
		{name: "string distinct from symbol", key: `"a"`, want: false},
		{name: "absent symbol", key: ":missing", want: false},
		{name: "integer candidate", key: "1", want: false},
		{name: "array candidate", key: "[:a]", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run() { a: 1 }.member?("+tt.key+") end")
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindBool {
				t.Fatalf("expected bool, got %v", result.Kind())
			}
			if result.Bool() != tt.want {
				t.Fatalf("member?(%s) = %v, want %v", tt.key, result.Bool(), tt.want)
			}
		})
	}
}

func TestHashValuePredicates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		hash   string
		value  string
		want   bool
	}{
		{name: "value? int present", method: "value?", hash: "{ a: 1, b: 2 }", value: "1", want: true},
		{name: "value? int absent", method: "value?", hash: "{ a: 1, b: 2 }", value: "3", want: false},
		{name: "value? string present", method: "value?", hash: `{ a: "x" }`, value: `"x"`, want: true},
		{name: "value? nil present", method: "value?", hash: "{ a: nil }", value: "nil", want: true},
		{name: "value? composite present", method: "value?", hash: "{ a: [1, 2] }", value: "[1, 2]", want: true},
		{name: "value? composite absent", method: "value?", hash: "{ a: [1, 2] }", value: "[1, 3]", want: false},
		{name: "value? nested hash present", method: "value?", hash: "{ a: { b: 1 } }", value: "{ b: 1 }", want: true},
		{name: "has_value? int present", method: "has_value?", hash: "{ a: 1 }", value: "1", want: true},
		{name: "has_value? int absent", method: "has_value?", hash: "{ a: 1 }", value: "2", want: false},
		{name: "has_value? distinguishes int and float", method: "has_value?", hash: "{ a: 1 }", value: "1.0", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run() "+tt.hash+"."+tt.method+"("+tt.value+") end")
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindBool {
				t.Fatalf("expected bool, got %v", result.Kind())
			}
			if result.Bool() != tt.want {
				t.Fatalf("%s(%s) = %v, want %v", tt.method, tt.value, result.Bool(), tt.want)
			}
		})
	}
}

func TestHashValuePredicatesRejectWrongArity(t *testing.T) {
	t.Parallel()

	for _, method := range []string{"value?", "has_value?"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run() { a: 1 }."+method+"(1, 2) end")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "expects exactly one value")
		})
	}
}

func TestHashStoreReturnsNewHash(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def add_key()
      original = { a: 1 }
      updated = original.store(:b, 2)
      { original: original, updated: updated }
    end

    def overwrite()
      { a: 1 }.store("a", 9)
    end

    def string_key()
      { a: 1 }.store("b", 2)
    end
    `)

	added := callFunc(t, script, "add_key", nil).Hash()
	original := added["original"]
	if original.Kind() != KindHash {
		t.Fatalf("original expected hash, got %v", original.Kind())
	}
	compareHash(t, original.Hash(), map[string]Value{"a": NewInt(1)})

	updated := added["updated"]
	if updated.Kind() != KindHash {
		t.Fatalf("updated expected hash, got %v", updated.Kind())
	}
	compareHash(t, updated.Hash(), map[string]Value{"a": NewInt(1), "b": NewInt(2)})

	overwritten := callFunc(t, script, "overwrite", nil)
	if overwritten.Kind() != KindHash {
		t.Fatalf("overwrite expected hash, got %v", overwritten.Kind())
	}
	compareHash(t, overwritten.Hash(), map[string]Value{"a": NewInt(9)})

	stringKey := callFunc(t, script, "string_key", nil)
	if stringKey.Kind() != KindHash {
		t.Fatalf("string_key expected hash, got %v", stringKey.Kind())
	}
	compareHash(t, stringKey.Hash(), map[string]Value{"a": NewInt(1), "b": NewInt(2)})
}

func TestHashStoreRejectsMisuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{name: "missing value", source: "def run() { a: 1 }.store(:b) end", wantErr: "hash.store expects a key and a value"},
		{name: "no arguments", source: "def run() { a: 1 }.store() end", wantErr: "hash.store expects a key and a value"},
		{name: "too many arguments", source: "def run() { a: 1 }.store(:b, 2, 3) end", wantErr: "hash.store expects a key and a value"},
		{name: "unsupported key", source: "def run() { a: 1 }.store({ bad: 1 }, 2) end", wantErr: "hash.store key is unsupported hash key"},
		{name: "keyword argument", source: "def run() { a: 1 }.store(b: 2) end", wantErr: "hash.store does not accept keyword arguments"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.wantErr)
		})
	}
}

func TestHashExceptIgnoresUnsupportedKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   map[string]Value
	}{
		{
			name:   "parenless invocation copies the receiver",
			source: `def run() ({ a: 1 }).except end`,
			want:   map[string]Value{"a": NewInt(1)},
		},
		{
			name:   "unsupported only preserves entries",
			source: `def run() { a: 1 }.except(1) end`,
			want:   map[string]Value{"a": NewInt(1)},
		},
		{
			name:   "mixed unsupported and supported excludes supported",
			source: `def run() { a: 1 }.except(1, :a) end`,
			want:   map[string]Value{},
		},
		{
			name:   "multiple unsupported keys are all ignored",
			source: `def run() { a: 1, b: 2 }.except([3], { c: 4 }) end`,
			want:   map[string]Value{"a": NewInt(1), "b": NewInt(2)},
		},
		{
			name:   "string and symbol keys still excluded alongside unsupported",
			source: `def run() { "a": 1, b: 2, c: 3 }.except("a", 5, :c) end`,
			want:   map[string]Value{"b": NewInt(2)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindHash {
				t.Fatalf("except expected hash, got %v", result.Kind())
			}
			compareHash(t, result.Hash(), tt.want)
		})
	}
}

func TestHashSliceIgnoresUnsupportedKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   map[string]Value
	}{
		{
			name:   "no arguments returns empty hash",
			source: `def run() ({ a: 1 }).slice() end`,
			want:   map[string]Value{},
		},
		{
			name:   "parenless invocation returns empty hash",
			source: `def run() ({ a: 1 }).slice end`,
			want:   map[string]Value{},
		},
		{
			name:   "unsupported only returns empty hash",
			source: `def run() { a: 1 }.slice(1) end`,
			want:   map[string]Value{},
		},
		{
			name:   "mixed unsupported and supported keeps supported",
			source: `def run() { a: 1, b: 2 }.slice(:a, 1) end`,
			want:   map[string]Value{"a": NewInt(1)},
		},
		{
			name:   "multiple unsupported keys are all ignored",
			source: `def run() { a: 1, b: 2 }.slice([3], { c: 4 }) end`,
			want:   map[string]Value{},
		},
		{
			name:   "string and symbol keys selected alongside unsupported",
			source: `def run() { "a": 1, b: 2, c: 3 }.slice("a", 5, :c) end`,
			want:   map[string]Value{"a": NewInt(1), "c": NewInt(3)},
		},
		{
			name:   "absent supported key is omitted",
			source: `def run() { a: 1 }.slice(:a, :missing) end`,
			want:   map[string]Value{"a": NewInt(1)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindHash {
				t.Fatalf("slice expected hash, got %v", result.Kind())
			}
			compareHash(t, result.Hash(), tt.want)
		})
	}
}

func TestReservedWordLabelsInHashesAndCallKwargs(t *testing.T) {
	t.Parallel()
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

// TestKeywordHashLabelsRoundTrip verifies that reserved-word tokens that the
// parser previously rejected as hash labels (begin, rescue, ensure, raise,
// export) now build hashes whose keys are read back as symbols, mirroring
// Ruby's uniform treatment of keyword-shaped labels.
func TestKeywordHashLabelsRoundTrip(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def payload()
      { begin: 1, rescue: 2, ensure: 3, raise: 4, export: 5 }
    end

    def read(key)
      payload()[key]
    end
    `)

	payload := callFunc(t, script, "payload", nil)
	if payload.Kind() != KindHash {
		t.Fatalf("expected hash result, got %v", payload.Kind())
	}
	compareHash(t, payload.Hash(), map[string]Value{
		"begin":  NewInt(1),
		"rescue": NewInt(2),
		"ensure": NewInt(3),
		"raise":  NewInt(4),
		"export": NewInt(5),
	})

	wantByKey := map[string]Value{
		"begin":  NewInt(1),
		"rescue": NewInt(2),
		"ensure": NewInt(3),
		"raise":  NewInt(4),
		"export": NewInt(5),
	}
	for key, want := range wantByKey {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, "read", []Value{NewSymbol(key)})
			if !got.Equal(want) {
				t.Fatalf("read(:%s) = %v, want %v", key, got, want)
			}
		})
	}
}

// TestHashValueOmissionReadsLocals verifies that the {name:} shorthand reads
// the local variable of the same name, producing the same hash as {name: name}.
func TestHashValueOmissionReadsLocals(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def build()
      name = "Ada"
      age = 36
      { name:, age:, role: "engineer" }
    end
  `)

	got := callFunc(t, script, "build", nil)
	if got.Kind() != KindHash {
		t.Fatalf("expected hash result, got %v", got.Kind())
	}
	compareHash(t, got.Hash(), map[string]Value{
		"name": NewString("Ada"),
		"age":  NewInt(36),
		"role": NewString("engineer"),
	})
}

// TestHashValueOmissionUndefinedLocal verifies that omitting the value for a
// label with no matching local falls through to the normal undefined-variable
// diagnostic rather than silently producing nil.
func TestHashValueOmissionUndefinedLocal(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def build()
      { name: }
    end
  `)

	requireCallErrorContains(t, script, "build", nil, CallOptions{}, "undefined variable name")
}
