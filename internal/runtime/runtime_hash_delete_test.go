package runtime

import (
	"context"
	"testing"
)

func TestHashDelete(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def delete_symbol(record)
      record.delete(:a)
    end

    def delete_string(record)
      record.delete("a")
    end

    def delete_missing(record)
      record.delete(:z)
    end

    def delete_missing_with_block(record)
      record.delete(:z) { |key| key }
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
			result := callFunc(t, script, tt.function, []Value{NewHash(tt.arg)})
			if result.Kind() != KindHash {
				t.Fatalf("expected hash result, got %v", result.Kind())
			}
			res := result.Hash()
			if res["hash"].Kind() != KindHash {
				t.Fatalf("expected hash entry, got %v", res["hash"].Kind())
			}
			compareHash(t, res["hash"].Hash(), tt.wantHash)
			if diff := valueDiff(tt.wantDeleted, res["deleted"]); diff != "" {
				t.Fatalf("deleted mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestHashDeleteIsNonMutating(t *testing.T) {
	t.Parallel()
	// delete mirrors store: it returns a new hash and leaves the receiver
	// untouched, matching Vibescript's non-mutating collection model.
	script := compileScript(t, `
    def delete_preserves_source(record)
      removed = record.delete(:a)
      { source: record, removed: removed }
    end
    `)

	result := callFunc(t, script, "delete_preserves_source",
		[]Value{NewHash(map[string]Value{"a": NewInt(1), "b": NewInt(2)})}).Hash()
	compareHash(t, result["source"].Hash(), map[string]Value{"a": NewInt(1), "b": NewInt(2)})
	removed := result["removed"].Hash()
	compareHash(t, removed["hash"].Hash(), map[string]Value{"b": NewInt(2)})
	if diff := valueDiff(NewInt(1), removed["deleted"]); diff != "" {
		t.Fatalf("deleted mismatch (-want +got):\n%s", diff)
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
      record.delete(1)
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
		"hash.delete key must be symbol or string")
	requireCallErrorContains(t, script, "keyword", base, CallOptions{},
		"hash.delete does not accept keyword arguments")
}

// TestHashDeleteRejectsWhenCopyExceedsQuota proves delete preflights the result
// copy against the memory quota before reserving it, matching store, replace, and
// slice. delete returns a new hash, so it allocates a near-full copy on both the
// present-key path (len(base)-1) and the miss path (len(base)). Without the
// preflight those allocations would run before the statement-level memory check,
// letting a delete on a large hash near the quota transiently exceed it. The quota
// is sized to admit the receiver baseline but fall short of the projected copy, so
// the preflight is the sole reason each call is rejected.
func TestHashDeleteRejectsWhenCopyExceedsQuota(t *testing.T) {
	t.Parallel()

	const count = 5_000
	receiver := largeHashReceiver(count)

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	projectedBase := probe.projectedHashBaseBytes(receiver, nil, nil, NewNil())

	// The present path copies len(base)-1 entries; size the quota to admit the
	// baseline plus all but one of those entries, so the copy's last entry is what
	// pushes past the quota. The miss path copies one more entry still, so the same
	// quota rejects it too.
	presentEntries := count - 1
	quota := projectedBase + (presentEntries-1)*estimatedMapEntryStructuralBytes
	if quota <= projectedBase {
		t.Fatalf("test setup expects a quota above the receiver baseline, got %d <= %d", quota, projectedBase)
	}

	t.Run("present key", func(t *testing.T) {
		t.Parallel()
		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		_, err := callHashMember(t, exec, receiver, "delete", []Value{NewString("k0")}, NewNil())
		requireErrorIs(t, err, errMemoryQuotaExceeded)
	})

	t.Run("missing key", func(t *testing.T) {
		t.Parallel()
		exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
		_, err := callHashMember(t, exec, receiver, "delete", []Value{NewString("absent")}, NewNil())
		requireErrorIs(t, err, errMemoryQuotaExceeded)
	})
}
