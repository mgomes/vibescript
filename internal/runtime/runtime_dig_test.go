package runtime

import (
	"context"
	"testing"
)

// TestDigPaths exercises Array#dig, Hash#dig, and mixed hash/array dig paths.
// The script returns the dug value verbatim so each case asserts the exact
// shape, including nil for any missing key or out-of-range index.
func TestDigPaths(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def array_dig()
      [[1, 2], [3, 4]].dig(1, 0)
    end

    def array_dig_single()
      [10, 20, 30].dig(1)
    end

    def array_dig_oob()
      [[1, 2], [3, 4]].dig(5, 0)
    end

    def array_dig_negative()
      [1, 2, 3].dig(-1)
    end

    def array_dig_whole_float()
      [[1, 2, 3]].dig(0, 2.0)
    end

    def hash_dig()
      { a: { b: { c: 7 } } }.dig(:a, :b, :c)
    end

    def hash_dig_string_keys()
      { "a": { "b": 9 } }.dig("a", "b")
    end

    def hash_dig_miss()
      { a: { b: 1 } }.dig(:a, :z)
    end

    def hash_dig_int_key()
      { a: { b: 1 } }.dig(:a, 0)
    end

    def hash_dig_int_first_key()
      { a: 1 }.dig(1)
    end

    def hash_dig_through_scalar()
      { a: { b: 1 } }.dig(:a, :b, :length)
    end

    def hash_into_array()
      { a: [10, 20] }.dig(:a, 1)
    end

    def array_into_hash()
      [{ name: "x" }, { name: "y" }].dig(1, :name)
    end

    def deep_mixed()
      { rows: [{ cells: [1, 2, 3] }] }.dig(:rows, 0, :cells, 2)
    end
    `)

	tests := []struct {
		name     string
		function string
		want     Value
	}{
		{"array two levels", "array_dig", NewInt(3)},
		{"array single index", "array_dig_single", NewInt(20)},
		{"array out of bounds yields nil", "array_dig_oob", NewNil()},
		{"array negative index yields nil", "array_dig_negative", NewNil()},
		{"array whole float index", "array_dig_whole_float", NewInt(3)},
		{"hash three levels", "hash_dig", NewInt(7)},
		{"hash string keys", "hash_dig_string_keys", NewInt(9)},
		{"hash missing key yields nil", "hash_dig_miss", NewNil()},
		{"hash integer key yields nil", "hash_dig_int_key", NewNil()},
		{"hash integer first key yields nil", "hash_dig_int_first_key", NewNil()},
		{"hash through scalar yields nil", "hash_dig_through_scalar", NewNil()},
		{"hash into array", "hash_into_array", NewInt(20)},
		{"array into hash", "array_into_hash", NewString("y")},
		{"deep mixed path", "deep_mixed", NewInt(3)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tt.function, nil)
			if diff := valueDiff(tt.want, got); diff != "" {
				t.Fatalf("dig result mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestDigErrors covers the argument and type rejections shared by Array#dig and
// Hash#dig: an empty path, and indexing an array with a non-integer component.
func TestDigErrors(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def array_dig_empty()
      [1, 2, 3].dig()
    end

    def hash_dig_empty()
      { a: 1 }.dig()
    end

    def array_dig_string_index()
      [[1, 2]].dig(0, "x")
    end

    def array_dig_fractional_index()
      [[1, 2]].dig(0, 1.5)
    end

    def hash_into_array_string_index()
      { a: [10, 20] }.dig(:a, "1")
    end
    `)

	tests := []struct {
		name     string
		function string
		want     string
	}{
		{"array empty path", "array_dig_empty", "array.dig expects at least one index"},
		{"hash empty path", "hash_dig_empty", "hash.dig expects at least one key"},
		{"array string index", "array_dig_string_index", "array.dig array index must be integer"},
		{"array fractional index", "array_dig_fractional_index", "array.dig array index must be integer"},
		{"hash path string array index", "hash_into_array_string_index", "hash.dig array index must be integer"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			requireCallErrorContains(t, script, tt.function, nil, CallOptions{}, tt.want)
		})
	}
}

// TestDigPathUnit exercises digPath directly to cover container kinds that are
// awkward to construct from script source, including digging through nil and a
// scalar receiver.
func TestDigPathUnit(t *testing.T) {
	t.Parallel()
	nested := NewHash(map[string]Value{
		"a": NewArray([]Value{NewInt(10), NewHash(map[string]Value{"b": NewInt(99)})}),
	})

	tests := []struct {
		name    string
		current Value
		args    []Value
		want    Value
		wantErr string
	}{
		{
			name:    "descend hash then array then hash",
			current: nested,
			args:    []Value{NewSymbol("a"), NewInt(1), NewSymbol("b")},
			want:    NewInt(99),
		},
		{
			name:    "missing first key yields nil",
			current: nested,
			args:    []Value{NewSymbol("missing")},
			want:    NewNil(),
		},
		{
			name:    "dig through nil yields nil",
			current: NewNil(),
			args:    []Value{NewSymbol("a")},
			want:    NewNil(),
		},
		{
			name:    "dig through scalar yields nil",
			current: NewInt(5),
			args:    []Value{NewInt(0)},
			want:    NewNil(),
		},
		{
			name:    "array non-integer index errors",
			current: NewArray([]Value{NewInt(1)}),
			args:    []Value{NewString("0")},
			wantErr: "test.dig array index must be integer",
		},
	}

	exec := &Execution{ctx: context.Background()}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := exec.digPath("test.dig", tt.current, tt.args)
			if tt.wantErr != "" {
				requireErrorContains(t, err, tt.wantErr)
				return
			}
			if err != nil {
				t.Fatalf("digPath returned error: %v", err)
			}
			if diff := valueDiff(tt.want, got); diff != "" {
				t.Fatalf("digPath mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
