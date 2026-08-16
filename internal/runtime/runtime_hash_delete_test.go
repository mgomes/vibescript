package runtime

import (
	"context"
	"testing"
)

func TestHashDelete(t *testing.T) {
	t.Parallel()
	// Ruby contract: delete removes the entry from the receiver in place and
	// returns the removed value (or the block result / nil on a miss).
	script := compileScript(t, `
    def delete_symbol(record)
      { removed: record.delete(:a), record: record }
    end

    def delete_string(record)
      { removed: record.delete("a"), record: record }
    end

    def delete_missing(record)
      { removed: record.delete(:z), record: record }
    end

    def delete_missing_with_block(record)
      { removed: record.delete(:z) { |key| key }, record: record }
    end
    `)

	tests := []struct {
		name        string
		function    string
		arg         map[string]Value
		wantHash    map[string]Value
		wantDeleted Value
	}{
		{
			name:        "removes a symbol key and reports its value",
			function:    "delete_symbol",
			arg:         map[string]Value{"a": NewInt(1), "b": NewInt(2)},
			wantHash:    map[string]Value{"b": NewInt(2)},
			wantDeleted: NewInt(1),
		},
		{
			name:        "string key normalizes to the same entry as a symbol",
			function:    "delete_string",
			arg:         map[string]Value{"a": NewInt(1), "b": NewInt(2)},
			wantHash:    map[string]Value{"b": NewInt(2)},
			wantDeleted: NewInt(1),
		},
		{
			name:        "reports nil and leaves the hash unchanged on a miss",
			function:    "delete_missing",
			arg:         map[string]Value{"a": NewInt(1)},
			wantHash:    map[string]Value{"a": NewInt(1)},
			wantDeleted: NewNil(),
		},
		{
			name:        "invokes the block with the key on a miss",
			function:    "delete_missing_with_block",
			arg:         map[string]Value{"a": NewInt(1)},
			wantHash:    map[string]Value{"a": NewInt(1)},
			wantDeleted: NewSymbol("z"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := callFunc(t, script, tt.function, []Value{NewHash(tt.arg)}).Hash()
			if res["record"].Kind() != KindHash {
				t.Fatalf("expected hash entry, got %v", res["record"].Kind())
			}
			compareHash(t, res["record"].Hash(), tt.wantHash)
			if diff := valueDiff(tt.wantDeleted, res["removed"]); diff != "" {
				t.Fatalf("removed mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestHashDeleteUpdatesItsRoot(t *testing.T) {
	t.Parallel()
	// delete updates the local it addresses, and a binding taken before the
	// call keeps the value it was given.
	script := compileScript(t, `
    def delete_mutates_source(record)
      other = record
      removed = record.delete(:a)
      { source: record, other: other, removed: removed }
    end
    `)

	result := callFunc(t, script, "delete_mutates_source",
		[]Value{NewHash(map[string]Value{"a": NewInt(1), "b": NewInt(2)})}).Hash()
	compareHash(t, result["source"].Hash(), map[string]Value{"b": NewInt(2)})
	compareHash(t, result["other"].Hash(), map[string]Value{"a": NewInt(1), "b": NewInt(2)})
	if diff := valueDiff(NewInt(1), result["removed"]); diff != "" {
		t.Fatalf("removed mismatch (-want +got):\n%s", diff)
	}
}

func TestHashDeleteErrors(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def no_args(record)
      record.delete()
    end

    def too_many(record)
      record.delete(:a, :b)
    end

    def invalid_key(record)
      record.delete({ bad: 1 })
    end

    def keyword(record)
      record.delete(foo: 1)
    end
    `)

	base := []Value{NewHash(map[string]Value{"a": NewInt(1)})}
	requireCallErrorContains(t, script, "no_args", base, CallOptions{},
		"hash.delete expects a key")
	requireCallErrorContains(t, script, "too_many", base, CallOptions{},
		"hash.delete expects a key")
	requireCallErrorContains(t, script, "invalid_key", base, CallOptions{},
		"hash.delete key is an unsupported hash key")
	requireCallErrorContains(t, script, "keyword", base, CallOptions{},
		"hash.delete does not accept keyword arguments")
}

// TestHashDeleteAllocatesNoReceiverCopy pins that the in-place delete no
// longer copies the receiver: under a quota with no headroom beyond the live
// call roots it still succeeds on both the present-key path (shrinking the
// receiver) and the miss path (leaving it untouched).
func TestHashDeleteAllocatesNoReceiverCopy(t *testing.T) {
	t.Parallel()

	const count = 5_000

	t.Run("present key", func(t *testing.T) {
		t.Parallel()
		receiver := largeHashReceiver(count)
		probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
		quota := probe.projectedHashBaseBytes(receiver, []Value{NewString("k0")}, nil, NewNil())
		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		got, err := callHashMember(t, exec, receiver, "delete", []Value{NewString("k0")}, NewNil())
		if err != nil {
			t.Fatalf("delete under a copy-tight quota = %v, want success (no receiver copy)", err)
		}
		if got.Kind() != KindInt || got.Int() != 0 {
			t.Fatalf("delete returned %v, want the removed value 0", got)
		}
		if receiver.HashLen() != count-1 {
			t.Fatalf("receiver holds %d entries after delete, want %d", receiver.HashLen(), count-1)
		}
	})

	t.Run("missing key", func(t *testing.T) {
		t.Parallel()
		receiver := largeHashReceiver(count)
		probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
		quota := probe.projectedHashBaseBytes(receiver, []Value{NewString("absent")}, nil, NewNil())
		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		got, err := callHashMember(t, exec, receiver, "delete", []Value{NewString("absent")}, NewNil())
		if err != nil {
			t.Fatalf("delete miss under a copy-tight quota = %v, want success (no receiver copy)", err)
		}
		if got.Kind() != KindNil {
			t.Fatalf("delete miss returned %v, want nil", got)
		}
		if receiver.HashLen() != count {
			t.Fatalf("receiver holds %d entries after a miss, want %d", receiver.HashLen(), count)
		}
	})
}
