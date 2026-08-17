package value_test

import (
	"strings"
	"sync"
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
		requireKeyOrder(t, hash, []value.Value{value.NewString("z"), value.NewString("b"), value.NewString("a")})
	})

	t.Run("overwrite_keeps_position", func(t *testing.T) {
		t.Parallel()
		hash := value.NewHashWithCapacity(2)
		if err := hash.HashSet(value.NewSymbol("b"), value.NewInt(2)); err != nil {
			t.Fatalf("HashSet(b) error = %v, want nil", err)
		}
		if err := hash.HashSet(value.NewSymbol("a"), value.NewInt(1)); err != nil {
			t.Fatalf("HashSet(a) error = %v, want nil", err)
		}
		if err := hash.HashSet(value.NewSymbol("b"), value.NewInt(9)); err != nil {
			t.Fatalf("HashSet(b overwrite) error = %v, want nil", err)
		}
		requireKeyOrder(t, hash, []value.Value{value.NewString("b"), value.NewString("a")})
		updated, ok, err := hash.HashGet(value.NewSymbol("b"))
		if err != nil || !ok || !updated.Equal(value.NewInt(9)) {
			t.Fatalf("HashGet(b) = %v, %v, %v; want 9, true, nil", updated, ok, err)
		}
	})

	t.Run("bare_map_seeds_sorted_then_appends", func(t *testing.T) {
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
			value.NewString("aa"),
		})
	})

	t.Run("symbol_and_string_spellings_share_one_slot", func(t *testing.T) {
		t.Parallel()
		hash := value.NewHashWithCapacity(2)
		if err := hash.HashSet(value.NewSymbol("a"), value.NewInt(1)); err != nil {
			t.Fatalf("HashSet(:a) error = %v, want nil", err)
		}
		if err := hash.HashSet(value.NewString("b"), value.NewInt(2)); err != nil {
			t.Fatalf("HashSet(\"b\") error = %v, want nil", err)
		}
		if err := hash.HashSet(value.NewString("a"), value.NewInt(9)); err != nil {
			t.Fatalf("HashSet(\"a\") error = %v, want nil", err)
		}
		requireKeyOrder(t, hash, []value.Value{value.NewString("a"), value.NewString("b")})
		updated, ok, err := hash.HashGet(value.NewSymbol("a"))
		if err != nil || !ok || !updated.Equal(value.NewInt(9)) {
			t.Fatalf("HashGet(:a) = %v, %v, %v; want 9, true, nil", updated, ok, err)
		}
	})
}

func TestHashOrderCapacity(t *testing.T) {
	t.Parallel()

	orderOnly := value.NewHash(map[string]value.Value{})
	orderOnly.ReserveHashOrder(3)
	if got := value.HashOrderCapacity(orderOnly); got != 3 {
		t.Fatalf("ReserveHashOrder() order capacity = %d, want 3", got)
	}
	if got := value.HashEntryCapacity(orderOnly); got != 0 {
		t.Fatalf("ReserveHashOrder() entry capacity = %d, want 0", got)
	}

	reserved := value.NewHash(map[string]value.Value{})
	reserved.ReserveHashCapacity(3)
	if got := value.HashEntryCapacity(reserved); got != 3 {
		t.Fatalf("ReserveHashCapacity() entry capacity = %d, want 3", got)
	}
	if got := value.HashOrderCapacity(reserved); got != 3 {
		t.Fatalf("ReserveHashCapacity() order capacity = %d, want 3", got)
	}

	newReserved := value.NewHashWithCapacity(3)
	if got := value.HashEntryCapacity(newReserved); got != 3 {
		t.Fatalf("NewHashWithCapacity(3) entry capacity = %d, want 3", got)
	}
	if got := value.HashOrderCapacity(newReserved); got != 3 {
		t.Fatalf("NewHashWithCapacity(3) order capacity = %d, want 3", got)
	}

	grown := value.NewHashWithCapacity(0)
	if err := grown.HashSet(value.NewSymbol("a"), value.NewInt(1)); err != nil {
		t.Fatalf("HashSet(a) error = %v, want nil", err)
	}
	grown.ReserveHashCapacity(3)
	if got := value.HashEntryCapacity(grown); got != 3 {
		t.Fatalf("ReserveHashCapacity() grown entry capacity = %d, want 3", got)
	}
	if got, ok, err := grown.HashGet(value.NewSymbol("a")); err != nil || !ok || !got.Equal(value.NewInt(1)) {
		t.Fatalf("HashGet(a) after ReserveHashCapacity() = %v, %v, %v; want 1, true, nil", got, ok, err)
	}

	hash := value.NewHashWithCapacity(0)
	if got := value.HashOrderCapacity(hash); got != 0 {
		t.Fatalf("empty typed hash order capacity = %d, want 0", got)
	}
	if err := hash.HashSet(value.NewSymbol("a"), value.NewInt(1)); err != nil {
		t.Fatalf("HashSet error = %v, want nil", err)
	}
	if got := value.HashOrderCapacity(hash); got < 1 {
		t.Fatalf("order capacity after insert = %d, want >= 1", got)
	}
	if got := value.HashOrderCapacity(value.NewHash(map[string]value.Value{"a": value.NewInt(1)})); got != 1 {
		t.Fatalf("bare-map hash order capacity = %d, want 1", got)
	}
	if got := value.HashOrderCapacity(value.NewInt(1)); got != 0 {
		t.Fatalf("non-hash order capacity = %d, want 0", got)
	}
}

func TestHashRenderingFollowsInsertionOrder(t *testing.T) {
	t.Parallel()

	hash := value.NewHashWithCapacity(3)
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

func TestBareMapHashIteratesSorted(t *testing.T) {
	t.Parallel()

	// A bare host map records no insertion order, so it iterates by sorted key
	// rather than exposing Go's randomized map traversal.
	hash := value.NewHash(map[string]value.Value{
		"b": value.NewInt(2),
		"a": value.NewInt(1),
	})
	requireKeyOrder(t, hash, []value.Value{value.NewString("a"), value.NewString("b")})
}

// A host holding the live map from Hash() can swap one key for another, leaving
// the entry count unchanged while the recorded order names a key the map no
// longer holds. Iteration must notice and fall back to sorted keys rather than
// emitting the departed key with a zero value and dropping the new one.
func TestHostKeySwapThroughLiveMapFallsBackToSortedKeys(t *testing.T) {
	t.Parallel()

	hash := value.NewHash(map[string]value.Value{"a": value.NewInt(1)})
	live := hash.Hash()
	delete(live, "a")
	live["b"] = value.NewInt(2)

	entries := hash.HashEntries()
	if len(entries) != 1 {
		t.Fatalf("entries after key swap = %d, want 1", len(entries))
	}
	if got := entries[0].Key.String(); got != "b" {
		t.Fatalf("entry key after swap = %q, want %q", got, "b")
	}
	if got := entries[0].Value; !got.Equal(value.NewInt(2)) {
		t.Fatalf("entry value after swap = %s, want 2", got.Inspect())
	}
}

func TestHostDeleteReinsertThenDirectInsertFallsBackToSortedKeys(t *testing.T) {
	t.Parallel()

	hash := value.NewHash(map[string]value.Value{})
	if err := hash.HashSet(value.NewString("a"), value.NewInt(1)); err != nil {
		t.Fatalf("HashSet(a) error = %v, want nil", err)
	}
	if err := hash.HashSet(value.NewString("b"), value.NewInt(2)); err != nil {
		t.Fatalf("HashSet(b) error = %v, want nil", err)
	}
	live := hash.Hash()
	delete(live, "a")
	if err := hash.HashSet(value.NewString("a"), value.NewInt(1)); err != nil {
		t.Fatalf("HashSet(a) after delete error = %v, want nil", err)
	}
	live["c"] = value.NewInt(3)

	requireKeyOrder(t, hash, []value.Value{
		value.NewString("a"),
		value.NewString("b"),
		value.NewString("c"),
	})
}

func TestHashIterationScratchBytesOnlyWhenOrderFallsBack(t *testing.T) {
	t.Parallel()

	hash := value.NewHash(map[string]value.Value{})
	for i := range 20 {
		name := string(rune('a' + i))
		if err := hash.HashSet(value.NewString(name), value.NewInt(1)); err != nil {
			t.Fatalf("HashSet(%s) error = %v, want nil", name, err)
		}
	}
	if got := value.HashIterationScratchBytes(hash); got != 0 {
		t.Fatalf("HashIterationScratchBytes(ordered) = %d, want 0", got)
	}
	live := hash.Hash()
	delete(live, "a")
	live["zz"] = value.NewInt(2)
	if got := value.HashIterationScratchBytes(hash); got <= 0 {
		t.Fatalf("HashIterationScratchBytes(invalidated) = %d, want > 0", got)
	}
}

func TestNewHashWithOrderDuplicateNamesFallsBackToSortedKeys(t *testing.T) {
	t.Parallel()

	entries := map[string]value.Value{
		"a": value.NewInt(1),
		"b": value.NewInt(2),
		"c": value.NewInt(3),
	}
	order := []value.Value{value.NewString("a"), value.NewString("b"), value.NewString("a")}
	hash := value.NewHashWithOrder(entries, order)
	if hash.HashUsesRecordedOrder() {
		t.Fatal("NewHashWithOrder(duplicate names) used the adopted order, want sorted fallback")
	}
	requireKeyOrder(t, hash, []value.Value{
		value.NewString("a"),
		value.NewString("b"),
		value.NewString("c"),
	})
}

func TestHashReadDoesNotLeaveLaterInsertsUntrusted(t *testing.T) {
	t.Parallel()

	hash := value.NewHash(map[string]value.Value{})
	if err := hash.HashSet(value.NewString("a"), value.NewInt(1)); err != nil {
		t.Fatalf("HashSet(a) error = %v, want nil", err)
	}
	_ = hash.Hash()
	if err := hash.HashSet(value.NewString("b"), value.NewInt(2)); err != nil {
		t.Fatalf("HashSet(b) after Hash() error = %v, want nil", err)
	}
	if !hash.HashUsesRecordedOrder() {
		t.Fatal("Hash() then HashSet(b) dropped the recorded order, want insertion order kept")
	}
	requireKeyOrder(t, hash, []value.Value{value.NewString("a"), value.NewString("b")})
}

func TestConcurrentHashReadsAreRaceFree(t *testing.T) {
	t.Parallel()

	hash := value.NewHash(map[string]value.Value{"a": value.NewInt(1)})
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if got := hash.Hash(); got["a"].Int() != 1 {
				t.Errorf("Hash()[a] = %v, want 1", got["a"])
			}
		})
	}
	wg.Wait()
}

func TestHostKeySwapFallbackSortsTheEntryBuffer(t *testing.T) {
	t.Parallel()

	hash := value.NewHash(map[string]value.Value{})
	for _, name := range []string{"m", "k", "z"} {
		if err := hash.HashSet(value.NewString(name), value.NewInt(1)); err != nil {
			t.Fatalf("HashSet(%s) error = %v, want nil", name, err)
		}
	}
	live := hash.Hash()
	delete(live, "m")
	live["a"] = value.NewInt(2)
	live["c"] = value.NewInt(3)

	var buf [8]value.HashEntry
	entries := hash.HashEntriesInto(buf[:])
	requireKeyOrder(t, hash, []value.Value{
		value.NewString("a"),
		value.NewString("c"),
		value.NewString("k"),
		value.NewString("z"),
	})
	if len(entries) != 4 {
		t.Fatalf("HashEntriesInto after key swap = %d entries, want 4", len(entries))
	}
	if got, want := entries[0].Key.String(), "a"; got != want {
		t.Fatalf("HashEntriesInto[0] = %q, want %q", got, want)
	}
}
