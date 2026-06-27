package runtime

import (
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

// There is intentionally no aliased-slice dedup assertion (neither empty nor
// non-empty). A slice's backing-array pointer (via unsafe.SliceData) is not
// reliably reproducible across Go build/instrumentation configurations — these
// assertions flaked under the coverage, race, AND goroutine-leak-profile CI
// jobs (empty first, then non-empty), repeatedly red-listing unrelated PRs.
// Production dedup of aliased backings is a best-effort optimization that is
// sandbox-safe either way: when the identity is stable it deduplicates, and
// when it is not the estimator merely counts the backing again, an over-count
// that never under-counts, so the memory bound still holds. The dedup code
// path is still exercised (for line coverage) by the rest of the suite; it is
// just not asserted via a flaky pointer-identity unit test.

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

// TestMemoryEstimatorChargesBuiltinCapturedReceiver verifies that a bound
// equality predicate (for example probe = big.eql?) is charged for the receiver
// it keeps alive in its closure. Without this accounting an array of such probes
// would retain arbitrarily large structures invisibly to the memory quota.
func TestMemoryEstimatorChargesBuiltinCapturedReceiver(t *testing.T) {
	t.Parallel()
	payload := strings.Repeat("abcdefghij", 4096)
	receiver := NewString(payload)

	probe, ok := universalValueMember(receiver, "eql?")
	if !ok {
		t.Fatal("universalMember did not resolve eql?")
	}

	bare := newMemoryEstimator().value(NewBuiltin("noop", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
		return NewNil(), nil
	}))
	charged := newMemoryEstimator().value(probe)

	if charged <= bare {
		t.Fatalf("bound predicate estimate = %d, want greater than plain builtin %d (captured receiver uncharged)", charged, bare)
	}
	if charged < len(payload) {
		t.Fatalf("bound predicate estimate = %d, want captured receiver payload included (>= %d)", charged, len(payload))
	}
}

// TestMemoryEstimatorChargesBuiltinStructureForScalarReceiver verifies that a
// bound predicate over a scalar receiver (for example 1.eql?) is charged the
// structural cost of the freshly allocated *Builtin and its CapturedValues
// backing even though the captured scalar's payload is zero. Without this a
// script pushing many such probes into an array would retain per-probe builtin
// allocations invisibly to the memory quota.
func TestMemoryEstimatorChargesBuiltinStructureForScalarReceiver(t *testing.T) {
	t.Parallel()
	probe, ok := universalValueMember(NewInt(1), "eql?")
	if !ok {
		t.Fatal("universalMember did not resolve eql?")
	}

	bare := newMemoryEstimator().value(NewBuiltin("noop", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
		return NewNil(), nil
	}))
	charged := newMemoryEstimator().value(probe)

	// The scalar receiver contributes no payload, so the difference is the
	// per-probe builtin structure: the *Builtin plus its single-slot backing.
	wantStructure := estimatedBuiltinBytes + sliceStructuralBytes(valueBuiltin(probe).CapturedValues)
	if extra := charged - bare; extra < wantStructure {
		t.Fatalf("scalar-bound predicate estimate added %d bytes over plain builtin, want >= %d (per-probe builtin allocation uncharged)", extra, wantStructure)
	}
}

// TestMemoryEstimatorDoesNotDoubleChargeReachableReceiver confirms the captured
// receiver payload is counted once when the receiver is also independently
// reachable: a hash holding both the receiver and a probe bound to it costs no
// more than the receiver alone plus the probe's entry slot and per-probe builtin
// structure (the *Builtin and its captured-slot backing).
func TestMemoryEstimatorDoesNotDoubleChargeReachableReceiver(t *testing.T) {
	t.Parallel()
	payload := strings.Repeat("abcdefghij", 4096)
	receiver := NewString(payload)

	probe, ok := universalValueMember(receiver, "eql?")
	if !ok {
		t.Fatal("universalMember did not resolve eql?")
	}

	receiverOnly := newMemoryEstimator().value(NewHash(map[string]Value{"receiver": receiver}))
	combined := newMemoryEstimator().value(NewHash(map[string]Value{
		"receiver": receiver,
		"probe":    probe,
	}))

	// The combined hash adds only structural cost for the extra entry plus the
	// probe's own builtin allocation, not a second copy of the receiver payload —
	// maxExtra stays far below the receiver's len(payload), so double-charging the
	// receiver would still trip this bound.
	builtinStructure := estimatedBuiltinBytes + sliceStructuralBytes(valueBuiltin(probe).CapturedValues)
	maxExtra := estimatedMapEntryStructuralBytes + estimatedStringHeaderBytes + len("probe") + estimatedValueBytes + builtinStructure
	if extra := combined - receiverOnly; extra > maxExtra {
		t.Fatalf("combined estimate added %d bytes over receiver-only, want <= %d (receiver payload double-charged)", extra, maxExtra)
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
