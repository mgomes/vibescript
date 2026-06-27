package runtime

import "testing"

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
