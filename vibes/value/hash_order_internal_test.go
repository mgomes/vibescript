package value

import "testing"

// TestForEachTypedEntryFallsBackWithoutOrder pins the defensive map-order walk
// for a typed hash whose order record does not cover its entries. HashSet, the
// sole typed-entry writer, always keeps the two in sync, so this state is only
// constructible inside the package; the fallback keeps such a hash renderable
// instead of dropping entries.
func TestForEachTypedEntryFallsBackWithoutOrder(t *testing.T) {
	t.Parallel()

	keyA, err := NewHashLookupKey(NewSymbol("a"))
	if err != nil {
		t.Fatalf("NewHashLookupKey(a) error = %v, want nil", err)
	}
	keyB, err := NewHashLookupKey(NewSymbol("b"))
	if err != nil {
		t.Fatalf("NewHashLookupKey(b) error = %v, want nil", err)
	}
	hd := &hashData{typedEntries: map[HashLookupKey]HashEntry{
		keyA: {Key: NewSymbol("a"), Value: NewInt(1)},
		keyB: {Key: NewSymbol("b"), Value: NewInt(2)},
	}}

	seen := map[string]bool{}
	if err := hd.forEachTypedEntry(func(entry HashEntry) error {
		seen[entry.Key.String()] = true
		return nil
	}); err != nil {
		t.Fatalf("forEachTypedEntry error = %v, want nil", err)
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("fallback walk missed entries: %v", seen)
	}

	hash := Value{kind: KindHash, data: hd}
	entries := hash.HashEntriesInto(nil)
	if len(entries) != 2 {
		t.Fatalf("HashEntriesInto fallback entries = %d, want 2", len(entries))
	}
}
