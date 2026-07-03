package value_test

import (
	"strings"
	"testing"

	"github.com/mgomes/vibescript/vibes/value"
)

func orderedKeys(t *testing.T, hash value.Value) []value.Value {
	t.Helper()
	entries := hash.HashEntries()
	keys := make([]value.Value, len(entries))
	for i, entry := range entries {
		keys[i] = entry.Key
	}
	return keys
}

func requireKeyOrder(t *testing.T, hash value.Value, want []value.Value) {
	t.Helper()
	keys := orderedKeys(t, hash)
	if len(keys) != len(want) {
		t.Fatalf("key count = %d, want %d", len(keys), len(want))
	}
	for i, key := range keys {
		if !key.Equal(want[i]) {
			t.Fatalf("key[%d] = %v, want %v", i, key, want[i])
		}
	}
}

func TestHashSetPreservesInsertionOrder(t *testing.T) {
	t.Parallel()

	t.Run("new_keys_append", func(t *testing.T) {
		t.Parallel()
		hash := value.NewHash(map[string]value.Value{})
		for _, name := range []string{"z", "b", "a"} {
			if err := hash.HashSet(value.NewSymbol(name), value.NewInt(1)); err != nil {
				t.Fatalf("HashSet(%s) error = %v, want nil", name, err)
			}
		}
		requireKeyOrder(t, hash, []value.Value{value.NewSymbol("z"), value.NewSymbol("b"), value.NewSymbol("a")})
	})

	t.Run("overwrite_keeps_position", func(t *testing.T) {
		t.Parallel()
		hash := value.NewTypedHash(2)
		if err := hash.HashSet(value.NewSymbol("b"), value.NewInt(2)); err != nil {
			t.Fatalf("HashSet(b) error = %v, want nil", err)
		}
		if err := hash.HashSet(value.NewSymbol("a"), value.NewInt(1)); err != nil {
			t.Fatalf("HashSet(a) error = %v, want nil", err)
		}
		if err := hash.HashSet(value.NewSymbol("b"), value.NewInt(9)); err != nil {
			t.Fatalf("HashSet(b overwrite) error = %v, want nil", err)
		}
		requireKeyOrder(t, hash, []value.Value{value.NewSymbol("b"), value.NewSymbol("a")})
		updated, ok, err := hash.HashGet(value.NewSymbol("b"))
		if err != nil || !ok || !updated.Equal(value.NewInt(9)) {
			t.Fatalf("HashGet(b) = %v, %v, %v; want 9, true, nil", updated, ok, err)
		}
	})

	t.Run("legacy_promotion_seeds_sorted_then_appends", func(t *testing.T) {
		t.Parallel()
		hash := value.NewHash(map[string]value.Value{
			"c": value.NewInt(3),
			"a": value.NewInt(1),
			"b": value.NewInt(2),
		})
		if err := hash.HashSet(value.NewSymbol("aa"), value.NewInt(4)); err != nil {
			t.Fatalf("HashSet(aa) error = %v, want nil", err)
		}
		requireKeyOrder(t, hash, []value.Value{
			value.NewString("a"),
			value.NewString("b"),
			value.NewString("c"),
			value.NewSymbol("aa"),
		})
	})

	t.Run("mixed_key_kinds_keep_insertion_order", func(t *testing.T) {
		t.Parallel()
		hash := value.NewTypedHash(3)
		keys := []value.Value{value.NewInt(2), value.NewString("a"), value.NewSymbol("a"), value.NewBool(true)}
		for i, key := range keys {
			if err := hash.HashSet(key, value.NewInt(int64(i))); err != nil {
				t.Fatalf("HashSet(%v) error = %v, want nil", key, err)
			}
		}
		requireKeyOrder(t, hash, keys)
	})
}

func TestHashOrderCapacity(t *testing.T) {
	t.Parallel()

	hash := value.NewTypedHash(0)
	if got := value.HashOrderCapacity(hash); got != 0 {
		t.Fatalf("empty typed hash order capacity = %d, want 0", got)
	}
	if err := hash.HashSet(value.NewSymbol("a"), value.NewInt(1)); err != nil {
		t.Fatalf("HashSet error = %v, want nil", err)
	}
	if got := value.HashOrderCapacity(hash); got < 1 {
		t.Fatalf("order capacity after insert = %d, want >= 1", got)
	}
	if got := value.HashOrderCapacity(value.NewHash(map[string]value.Value{"a": value.NewInt(1)})); got != 0 {
		t.Fatalf("legacy hash order capacity = %d, want 0", got)
	}
	if got := value.HashOrderCapacity(value.NewInt(1)); got != 0 {
		t.Fatalf("non-hash order capacity = %d, want 0", got)
	}
}

func TestHashRenderingFollowsInsertionOrder(t *testing.T) {
	t.Parallel()

	hash := value.NewTypedHash(3)
	for _, name := range []string{"e", "b", "a", "d", "c"} {
		if err := hash.HashSet(value.NewSymbol(name), value.NewInt(1)); err != nil {
			t.Fatalf("HashSet(%s) error = %v, want nil", name, err)
		}
	}
	const want = "{e: 1, b: 1, a: 1, d: 1, c: 1}"
	if got := hash.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	if got := hash.Inspect(); got != want {
		t.Fatalf("Inspect() = %q, want %q", got, want)
	}

	// A bounded render truncates a deterministic insertion-ordered prefix.
	bounded, err := hash.StringBounded(9)
	if err == nil {
		t.Fatalf("StringBounded(9) error = nil, want truncation")
	}
	if !strings.HasPrefix(want, bounded) {
		t.Fatalf("StringBounded(9) = %q, want a prefix of %q", bounded, want)
	}
}

func TestHashEntriesFallBackWhenOrderUntracked(t *testing.T) {
	t.Parallel()

	// A legacy hash never tracks order; entries still surface exactly once
	// each even though their order is Go map order.
	hash := value.NewHash(map[string]value.Value{
		"b": value.NewInt(2),
		"a": value.NewInt(1),
	})
	entries := hash.HashEntries()
	if len(entries) != 2 {
		t.Fatalf("legacy entries = %d, want 2", len(entries))
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		seen[entry.Key.String()] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("legacy entries missing keys: %v", seen)
	}
}
