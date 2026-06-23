package runtime

import (
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

func TestMemoryEstimatorLayoutConstantsMatchRuntimeTypes(t *testing.T) {
	t.Parallel()

	if got, want := estimatedValueBytes, int(unsafe.Sizeof(Value{})); got != want {
		t.Fatalf("estimatedValueBytes = %d, want runtime Value size %d", got, want)
	}
	if got, want := estimatedEnvBytes, int(unsafe.Sizeof(Env{})); got != want {
		t.Fatalf("estimatedEnvBytes = %d, want runtime Env size %d", got, want)
	}
}

// requireStableSliceIdentity skips assertions about pointer-identity-based
// slice dedup when running under coverage instrumentation. The estimator keys
// alias dedup on a slice's backing-array pointer (via unsafe.SliceData); that
// dedup is correct in normal runs and in production, but coverage
// instrumentation perturbs the observed identity so the assertion is
// unreliable there. The dedup code itself is still exercised (for line
// coverage) by the rest of the suite.
func requireStableSliceIdentity(t *testing.T) {
	t.Helper()
	if testing.CoverMode() != "" {
		t.Skip("slice backing-pointer identity is unreliable under coverage instrumentation")
	}
}

func TestMemoryEstimatorDeduplicatesAliasedEmptySlices(t *testing.T) {
	t.Parallel()
	requireStableSliceIdentity(t)
	// An empty slice that retained capacity still owns a real cap-sized backing,
	// so aliases sharing it must be deduplicated (counted once).
	backing := make([]Value, 8)
	empty := backing[:0]
	aliasA := empty
	aliasB := empty

	est := newMemoryEstimator()
	first := est.slice(aliasA)
	second := est.slice(aliasB)
	runtime.KeepAlive(backing)

	if first == 0 {
		t.Fatalf("expected first alias to contribute memory")
	}
	if second != 0 {
		t.Fatalf("expected aliased empty slice to be deduplicated, got %d", second)
	}
}

func TestMemoryEstimatorDeduplicatesAliasedNonEmptySlices(t *testing.T) {
	t.Parallel()
	requireStableSliceIdentity(t)
	// Non-empty aliased slices share a stable backing pointer, so the second
	// estimate of the same backing is fully deduplicated to zero.
	backing := []Value{NewInt(1), NewInt(2), NewInt(3)}
	aliasA := backing
	aliasB := backing

	est := newMemoryEstimator()
	first := est.slice(aliasA)
	second := est.slice(aliasB)
	runtime.KeepAlive(backing)

	if first == 0 {
		t.Fatalf("expected first alias to contribute memory")
	}
	if second != 0 {
		t.Fatalf("expected aliased non-empty slice to be deduplicated, got %d", second)
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

func TestMemoryEstimatorResetAllowsReuse(t *testing.T) {
	t.Parallel()
	payload := NewHash(map[string]Value{"id": NewInt(1)})
	est := newMemoryEstimator()

	first := est.value(payload)
	aliased := est.value(payload)
	est.reset()
	reused := est.value(payload)

	if aliased >= first {
		t.Fatalf("aliased estimate = %d, want less than first estimate %d", aliased, first)
	}
	if reused != first {
		t.Fatalf("reused estimate = %d, want first estimate %d", reused, first)
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

func TestEnvInlineBindingsUseEnvStorageInEstimate(t *testing.T) {
	t.Parallel()

	env := newEnv(nil)
	emptySize := newMemoryEstimator().env(env)
	if emptySize != estimatedEnvBytes {
		t.Fatalf("empty inline env size = %d, want Env layout size %d", emptySize, estimatedEnvBytes)
	}

	env.Define("count", NewInt(1))
	inlineSize := newMemoryEstimator().env(env)
	if env.values != nil {
		t.Fatalf("single inline binding allocated values map")
	}
	if want := estimatedEnvBytes + len("count"); inlineSize != want {
		t.Fatalf("inline env size = %d, want %d", inlineSize, want)
	}

	env.Define("name", NewString("Ada"))
	withPayload := newMemoryEstimator().env(env)
	if withPayload <= inlineSize {
		t.Fatalf("inline env with string payload = %d, want greater than int-only size %d", withPayload, inlineSize)
	}
}

func TestEnvAssignDemotesStaticBindings(t *testing.T) {
	t.Parallel()
	payload := strings.Repeat("b", 16384)
	env := newEnv(nil)
	env.DefineStatic("name", NewInt(1))
	if env.staticBytes != int32(staticEntryBytes("name")) {
		t.Fatalf("staticBytes = %d, want %d", env.staticBytes, staticEntryBytes("name"))
	}

	env.Assign("name", NewString(payload))
	if env.staticBytes != 0 {
		t.Fatalf("staticBytes after demotion = %d, want 0", env.staticBytes)
	}
	if env.statics != nil {
		t.Fatalf("statics after demotion = %#v, want nil", env.statics)
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
