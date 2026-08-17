package value

import "testing"

// TestHashIterationFallsBackToSortedKeysWithoutOrder pins the defensive walk
// for a hash whose recorded order does not cover its entries. HashSet keeps the
// two in sync, so this state is only reachable through a host mutating the live
// map Hash() hands out; the fallback keeps such a hash deterministic instead of
// exposing Go's randomized map traversal.
func TestHashIterationFallsBackToSortedKeysWithoutOrder(t *testing.T) {
	t.Parallel()

	hd := &hashData{entries: map[string]Value{
		"b": NewInt(2),
		"a": NewInt(1),
	}}
	hash := Value{kind: KindHash, data: hd}

	entries := hash.HashEntriesInto(nil)
	if len(entries) != 2 {
		t.Fatalf("HashEntriesInto fallback entries = %d, want 2", len(entries))
	}
	if got := entries[0].Key.String(); got != "a" {
		t.Fatalf("fallback first key = %q, want %q", got, "a")
	}
	if got := entries[1].Key.String(); got != "b" {
		t.Fatalf("fallback second key = %q, want %q", got, "b")
	}
}

func TestInspectAndEqualDoNotMarkHashOrderUntrusted(t *testing.T) {
	t.Parallel()

	hash := NewHash(map[string]Value{})
	if err := hash.HashSet(NewString("a"), NewInt(1)); err != nil {
		t.Fatalf("HashSet(a) error = %v, want nil", err)
	}
	_ = hash.Inspect()
	if !hash.Equal(hash) {
		t.Fatal("hash.Equal(hash) = false, want true")
	}
	hd, ok := hash.data.(*hashData)
	if !ok {
		t.Fatalf("hash.data type = %T, want *hashData", hash.data)
	}
	if hd.orderUntrusted.Load() {
		t.Fatal("Inspect/Equal marked order untrusted, want HashEntryMap read")
	}
	if err := hash.HashSet(NewString("b"), NewInt(2)); err != nil {
		t.Fatalf("HashSet(b) error = %v, want nil", err)
	}
	if !hash.HashUsesRecordedOrder() {
		t.Fatal("HashUsesRecordedOrder() after Inspect/Equal = false, want true")
	}
}
