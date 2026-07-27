package runtime

import (
	"context"
	"testing"
)

// Hash had map_with_index but no map, which is almost certainly an oversight --
// no API adds the indexed variant first. The absence forced every hash
// aggregation through to_a and positional pair indexing, losing the names.
func TestHashMapYieldsEntriesLikeRuby(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			// A two-parameter block auto-splats the [key, value] pair, matching
			// Ruby's Hash#map.
			name:   "two parameter block receives key and value",
			source: `h = {alice: 90, bob: 72}` + "\n" + `h.map { |k, v| v }.inspect`,
			want:   "[90, 72]",
		},
		{
			// A one-parameter block receives the pair whole, also matching Ruby.
			name:   "one parameter block receives the pair",
			source: `h = {alice: 90}` + "\n" + `h.map { |pair| pair }.inspect`,
			want:   "[[:alice, 90]]",
		},
		{
			name:   "block may use both key and value",
			source: `h = {a: 1, b: 2}` + "\n" + `h.map { |k, v| "#{k}=#{v}" }.inspect`,
			want:   `["a=1", "b=2"]`,
		},
		{
			name:   "empty hash maps to an empty array",
			source: `({}).map { |k, v| v }.inspect`,
			want:   "[]",
		},
		{
			name:   "result composes with array methods",
			source: `h = {a: 1, b: 2, c: 3}` + "\n" + `h.map { |k, v| v }.sum.to_s`,
			want:   "6",
		},
		{
			// The indexed variant keeps working alongside it.
			name:   "map_with_index is unaffected",
			source: `h = {a: 1, b: 2}` + "\n" + `h.map_with_index { |pair, i| i }.inspect`,
			want:   "[0, 1]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.source+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got.String(), tc.want)
			}
		})
	}
}

// A host-built hash keeps legacy untyped entries and takes the other branch, so
// it needs its own coverage.
func TestHashMapOnHostBuiltReceiver(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run(h)
      h.map { |k, v| v }.sum
    end
    `)
	receiver := NewHash(map[string]Value{"a": NewInt(1), "b": NewInt(2), "c": NewInt(3)})
	got, err := script.Call(context.Background(), "run", []Value{receiver}, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "6" {
		t.Fatalf("host-built hash map summed to %s, want 6", got.String())
	}
}

// map must respect the step quota per entry, like the other block drivers.
func TestHashMapChargesStepsPerEntry(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 40, MemoryQuotaBytes: 8 << 20}, `
    def run(h)
      h.map { |k, v| v }
    end
    `)
	entries := map[string]Value{}
	for i := range 200 {
		entries[hostHashKeyForMap(i)] = NewInt(int64(i))
	}
	if _, err := script.Call(context.Background(), "run", []Value{NewHash(entries)}, CallOptions{}); err == nil {
		t.Fatalf("expected the step quota to stop a 200-entry map")
	}
}

func hostHashKeyForMap(i int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz"
	return string([]byte{'k', alphabet[i%26], alphabet[(i/26)%26]})
}
