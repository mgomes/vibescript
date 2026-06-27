package runtime

import (
	"context"
	"testing"
)

func TestArrayToHash(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def symbol_keys()
      [[:a, 1], [:b, 2]].to_h
    end

    def string_keys()
      [["a", 1], ["b", 2]].to_h
    end

    def mixed_keys()
      [[:a, 1], ["b", 2]].to_h
    end

    def duplicate_keys_keep_last()
      [[:a, 1], [:a, 2], [:a, 3]].to_h
    end

    def empty_array()
      [].to_h
    end

    def block_maps_pairs()
      [:a, :b].to_h { |s| [s, 1] }
    end

    def block_overrides_existing_pairs()
      [[:ignored, 0]].to_h { |pair| [:kept, 9] }
    end
    `)

	tests := []struct {
		name string
		fn   string
		want map[string]Value
	}{
		{
			name: "symbol keys",
			fn:   "symbol_keys",
			want: map[string]Value{"a": NewInt(1), "b": NewInt(2)},
		},
		{
			name: "string keys convert through the hash key rules",
			fn:   "string_keys",
			want: map[string]Value{"a": NewInt(1), "b": NewInt(2)},
		},
		{
			name: "symbol and string keys share the same key space",
			fn:   "mixed_keys",
			want: map[string]Value{"a": NewInt(1), "b": NewInt(2)},
		},
		{
			name: "duplicate keys keep the last pair like Ruby",
			fn:   "duplicate_keys_keep_last",
			want: map[string]Value{"a": NewInt(3)},
		},
		{
			name: "empty array converts to an empty hash",
			fn:   "empty_array",
			want: map[string]Value{},
		},
		{
			name: "block maps each element to a pair",
			fn:   "block_maps_pairs",
			want: map[string]Value{"a": NewInt(1), "b": NewInt(1)},
		},
		{
			name: "block result is used instead of the element",
			fn:   "block_overrides_existing_pairs",
			want: map[string]Value{"kept": NewInt(9)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tt.fn, nil)
			if got.Kind() != KindHash {
				t.Fatalf("expected hash, got %v", got.Kind())
			}
			compareHash(t, got.Hash(), tt.want)
		})
	}
}

func TestArrayToHashRejectsMisuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name:    "non-array element",
			source:  "def run() [:a, :b].to_h end",
			wantErr: "array.to_h expects an array of two-element pairs",
		},
		{
			name:    "pair too short",
			source:  "def run() [[:a]].to_h end",
			wantErr: "array.to_h pair must have exactly two elements",
		},
		{
			name:    "pair too long",
			source:  "def run() [[:a, 1, 2]].to_h end",
			wantErr: "array.to_h pair must have exactly two elements",
		},
		{
			name:    "unsupported key type",
			source:  "def run() [[1, 2]].to_h end",
			wantErr: "array.to_h pair key must be symbol or string",
		},
		{
			name:    "block returns a non-pair",
			source:  "def run() [:a].to_h { |s| s } end",
			wantErr: "array.to_h expects an array of two-element pairs",
		},
		{
			name:    "positional argument",
			source:  "def run() [[:a, 1]].to_h(2) end",
			wantErr: "array.to_h does not take arguments",
		},
		{
			name:    "keyword argument",
			source:  "def run() [[:a, 1]].to_h(depth: 2) end",
			wantErr: "array.to_h does not take keyword arguments",
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

func TestHashToArray(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def pairs()
      { a: 1, b: 2 }.to_a
    end

    def deterministic_order()
      { c: 3, a: 1, b: 2 }.to_a
    end

    def nested_values_kept()
      { a: [1, 2], b: { c: 3 } }.to_a
    end

    def empty_hash()
      {}.to_a
    end
    `)

	tests := []struct {
		name string
		fn   string
		want []Value
	}{
		{
			name: "key-value pairs",
			fn:   "pairs",
			want: []Value{
				NewArray([]Value{NewSymbol("a"), NewInt(1)}),
				NewArray([]Value{NewSymbol("b"), NewInt(2)}),
			},
		},
		{
			name: "pairs follow deterministic sorted-key order",
			fn:   "deterministic_order",
			want: []Value{
				NewArray([]Value{NewSymbol("a"), NewInt(1)}),
				NewArray([]Value{NewSymbol("b"), NewInt(2)}),
				NewArray([]Value{NewSymbol("c"), NewInt(3)}),
			},
		},
		{
			name: "nested values are preserved as-is",
			fn:   "nested_values_kept",
			want: []Value{
				NewArray([]Value{NewSymbol("a"), NewArray([]Value{NewInt(1), NewInt(2)})}),
				NewArray([]Value{NewSymbol("b"), NewHash(map[string]Value{"c": NewInt(3)})}),
			},
		},
		{
			name: "empty hash converts to an empty array",
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

func TestHashToArrayRejectsMisuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name:    "positional argument",
			source:  "def run() { a: 1 }.to_a(1) end",
			wantErr: "hash.to_a does not take arguments",
		},
		{
			name:    "keyword argument",
			source:  "def run() { a: 1 }.to_a(depth: 2) end",
			wantErr: "hash.to_a does not take keyword arguments",
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

// TestArrayToHashRoundTrip verifies Hash#to_a and Array#to_h are inverses for a
// hash with the same symbol/string key model, matching Ruby's round-trip.
func TestArrayToHashRoundTrip(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def round_trip()
      { a: 1, b: 2, c: 3 }.to_a.to_h
    end
    `)

	got := callFunc(t, script, "round_trip", nil)
	if got.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", got.Kind())
	}
	compareHash(t, got.Hash(), map[string]Value{"a": NewInt(1), "b": NewInt(2), "c": NewInt(3)})
}

// TestArrayToHashHonorsMemoryQuota verifies the output map projection trips the
// quota before the hash materializes, mirroring the other map-producing helpers.
func TestArrayToHashHonorsMemoryQuota(t *testing.T) {
	t.Parallel()

	// An array of distinct string-keyed pairs converts into a map roughly the size
	// of the input. Sizing the quota to admit the bound input with a slim margin
	// forces the limit to trip on the freshly built map rather than on argument
	// binding.
	const count = 4000
	pairs := make([]Value, count)
	for i := range count {
		key := NewString(string(rune('a'+i%26)) + string(rune('0'+i/26%10)) + string(rune('0'+i/260)))
		pairs[i] = NewArray([]Value{key, NewInt(int64(i))})
	}
	pairsArr := NewArray(pairs)

	inputBytes := newMemoryEstimator().value(pairsArr)
	quota := inputBytes + inputBytes/4

	cfg := Config{StepQuota: 1_000_000, MemoryQuotaBytes: quota}

	fits := compileScriptWithConfig(t, cfg, `def run(a); a.size; end`)
	if _, err := fits.Call(context.Background(), "run", []Value{pairsArr}, CallOptions{}); err != nil {
		t.Fatalf("input should fit under quota %d: %v", quota, err)
	}

	converts := compileScriptWithConfig(t, cfg, `def run(a); a.to_h; end`)
	requireCallRuntimeErrorType(t, converts, "run", []Value{pairsArr}, CallOptions{}, runtimeErrorTypeLimit)
}
