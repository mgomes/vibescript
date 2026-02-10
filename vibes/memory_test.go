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
