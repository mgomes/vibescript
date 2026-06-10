package runtime

import (
	"strings"
	"testing"
)

func TestMemoryEstimatorDeduplicatesAliasedEmptySlices(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	firstSlice := make([]Value, 0)
	secondSlice := make([]Value, 0)

	est := newMemoryEstimator()
	first := est.slice(firstSlice)
	second := est.slice(secondSlice)

	if first == 0 || second == 0 {
		t.Fatalf("expected both independent zero-cap slices to contribute memory, got %d and %d", first, second)
	}
}

func TestMemoryEstimatorDeduplicatesAliasedStringPayload(t *testing.T) {
	t.Parallel()
	payload := strings.Repeat("abcdefghij", 200)

	est := newMemoryEstimator()
	first := est.value(NewString(payload))
	second := est.value(NewString(payload))

	wantSecond := estimatedValueBytes + estimatedStringHeaderBytes
	if second != wantSecond {
		t.Fatalf("expected second aliased string to only add descriptor cost %d, got %d (first=%d)", wantSecond, second, first)
	}
}

func TestEnvStaticBindingsAccountedWithoutWalk(t *testing.T) {
	t.Parallel()
	payload := strings.Repeat("a", 16384)

	staticEnv := newEnv(nil)
	staticEnv.DefineStatic("big", NewString(payload))
	staticSize := newMemoryEstimator().env(staticEnv)
	if staticSize >= len(payload) {
		t.Fatalf("static binding size = %d, want payload excluded (< %d)", staticSize, len(payload))
	}
	want := estimatedEnvBytes + estimatedMapBaseBytes + staticEntryBytes("big")
	if staticSize != want {
		t.Fatalf("static env size = %d, want %d", staticSize, want)
	}

	dynamicEnv := newEnv(nil)
	dynamicEnv.Define("big", NewString(payload))
	if dynamicSize := newMemoryEstimator().env(dynamicEnv); dynamicSize < len(payload) {
		t.Fatalf("dynamic binding size = %d, want payload included (>= %d)", dynamicSize, len(payload))
	}
}

func TestEnvAssignDemotesStaticBindings(t *testing.T) {
	t.Parallel()
	payload := strings.Repeat("b", 16384)
	env := newEnv(nil)
	env.DefineStatic("name", NewInt(1))
	if env.staticBytes != staticEntryBytes("name") {
		t.Fatalf("staticBytes = %d, want %d", env.staticBytes, staticEntryBytes("name"))
	}

	env.Assign("name", NewString(payload))
	if env.staticBytes != 0 {
		t.Fatalf("staticBytes after demotion = %d, want 0", env.staticBytes)
	}
	val, ok := env.Get("name")
	if !ok || !val.Equal(NewString(payload)) {
		t.Fatalf("Get after demotion = (%v, %v), want reassigned value", val, ok)
	}
	if size := newMemoryEstimator().env(env); size < len(payload) {
		t.Fatalf("estimate after demotion = %d, want payload counted (>= %d)", size, len(payload))
	}
}

func TestEnvDefineShadowsStaticBinding(t *testing.T) {
	t.Parallel()
	env := newEnv(nil)
	env.DefineStatic("f", NewInt(1))
	env.Define("f", NewInt(2))
	if env.staticBytes != 0 {
		t.Fatalf("staticBytes after Define = %d, want 0", env.staticBytes)
	}
	val, ok := env.Get("f")
	if !ok || !val.Equal(NewInt(2)) {
		t.Fatalf("Get = (%v, %v), want dynamic shadow", val, ok)
	}
}
