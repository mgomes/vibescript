package vibes

import "testing"

func TestMemoryEstimatorDeduplicatesAliasedEmptySlices(t *testing.T) {
	backing := make([]Value, 0, 8)
	aliasA := backing
	aliasB := backing

	est := newMemoryEstimator()
	first := est.slice(aliasA)
	second := est.slice(aliasB)

	if first == 0 {
		t.Fatalf("expected first alias to contribute memory")
	}
	if second != 0 {
		t.Fatalf("expected aliased empty slice to be deduplicated, got %d", second)
	}
}

func TestMemoryEstimatorDoesNotDeduplicateIndependentZeroCapSlices(t *testing.T) {
	firstSlice := make([]Value, 0)
	secondSlice := make([]Value, 0)

	est := newMemoryEstimator()
	first := est.slice(firstSlice)
	second := est.slice(secondSlice)

	if first == 0 || second == 0 {
		t.Fatalf("expected both independent zero-cap slices to contribute memory, got %d and %d", first, second)
	}
}
