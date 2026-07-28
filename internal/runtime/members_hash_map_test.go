package runtime

import (
	"context"
	"fmt"
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

// The build accumulator records the growing result privately, so memory checks
// run inside a block call could not see it: a block allocating a large
// temporary was measured against a baseline omitting everything the loop had
// already retained, and the two passed separately though they coexisted.
func TestHashMapChargesRetainedOutputDuringBlockCalls(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000_000, MemoryQuotaBytes: 1024 * 1024}, `
    def run(h)
      h.map { |k, v|
        if v > 25
          ("y" * 600000).length.to_s
        else
          "x" * 20000
        end
      }
    end
    `)
	// Keys sort in value order, so the entries returning the large temporary
	// come last -- after the earlier results have accumulated. Without that the
	// peak never coexists and the test would not discriminate.
	entries := map[string]Value{}
	for i := range 30 {
		entries[fmt.Sprintf("k%03d", i)] = NewInt(int64(i))
	}
	if _, err := script.Call(context.Background(), "run", []Value{NewHash(entries)}, CallOptions{}); err == nil {
		t.Fatalf("retained output plus an in-block temporary exceeded the quota but was accepted")
	}
}

// The reservation is released as the loop ends, so an ordinary build is not
// rejected by scratch left behind.
func TestHashMapDoesNotOverReserve(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000_000, MemoryQuotaBytes: 8 << 20}, `
    def run(h)
      h.map { |k, v| v * 2 }
    end
    `)
	entries := map[string]Value{}
	for i := range 500 {
		entries[fmt.Sprintf("k%03d", i)] = NewInt(int64(i))
	}
	got, err := script.Call(context.Background(), "run", []Value{NewHash(entries)}, CallOptions{})
	if err != nil {
		t.Fatalf("a 500-entry map that fits was rejected: %v", err)
	}
	if len(got.Array()) != 500 {
		t.Fatalf("result has %d elements, want 500", len(got.Array()))
	}
}

// Numbered implicit parameters count toward the block's arity, so { _2 } is an
// arity-2 block and receives the value. Yielding the pair unconditionally
// bound _1 to the whole pair and left _2 nil.
func TestHashMapHonorsBlockArity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{`({a: 1}).map { _2 }.inspect`, "[1]"},
		{`({a: 1}).map { _1 }.inspect`, "[[:a, 1]]"},
		{`({a: 1, b: 2}).map { |k, v| v }.inspect`, "[1, 2]"},
		{`({a: 1}).map { |pair| pair }.inspect`, "[[:a, 1]]"},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %s, want %s", tc.expr, got.String(), tc.want)
			}
		})
	}
}

// A block returning scalars leaves the accumulator's element payload at zero,
// so reserving only that never grew the reservation -- while the preallocated
// result backing still stayed live alongside an in-block temporary.
func TestHashMapReservesTheResultBacking(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 100_000_000, MemoryQuotaBytes: 512 * 1024}, `
    def run(h)
      h.map { |k, v|
        if v > 40000
          ("y" * 400000).length
        else
          v
        end
      }
    end
    `)
	entries := map[string]Value{}
	for i := range 45000 {
		entries[fmt.Sprintf("k%06d", i)] = NewInt(int64(i))
	}
	if _, err := script.Call(context.Background(), "run", []Value{NewHash(entries)}, CallOptions{}); err == nil {
		t.Fatalf("the result backing plus an in-block temporary exceeded the quota but was accepted")
	}
}
