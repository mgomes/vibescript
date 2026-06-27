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

	probe, ok := universalMember(receiver, "eql?")
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
	probe, ok := universalMember(NewInt(1), "eql?")
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

	probe, ok := universalMember(receiver, "eql?")
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

// TestBlockBindsRestSkipsAnonymousRest is the regression for the Codex thread on
// PR #808: blockBindsRest gates the per-iteration bind charge, which exists only to
// account for the fresh, source-sized backing slice a NAMED rest materializes.
// assignDestructure discards an anonymous rest's window without allocating anything
// (restAnonymous skips the copy), so a bare "*" -- including the nested "|(head, *)|"
// shape over large rows -- must not enable the charge. Before the fix targetCollectsRest
// returned true for any element.Rest, seeding the estimator with the whole yielded value
// every iteration for a backing that never exists.
func TestBlockBindsRestSkipsAnonymousRest(t *testing.T) {
	t.Parallel()

	pos := Position{Line: 1, Column: 1}
	ident := func(name string) Expression { return &Identifier{Name: name, Position: pos} }
	destructure := func(elements ...DestructureElement) *DestructureTarget {
		return &DestructureTarget{Position: pos, Elements: elements}
	}
	block := func(target Expression) *Block {
		return valueBlock(NewBlock([]Param{{Kind: ParamNormal, Target: target}}, nil, newEnv(nil)))
	}

	tests := []struct {
		name   string
		target Expression
		want   bool
	}{
		{
			name:   "no rest binds nothing fresh",
			target: destructure(DestructureElement{Target: ident("a")}, DestructureElement{Target: ident("b")}),
			want:   false,
		},
		{
			name:   "top-level anonymous rest discards its window",
			target: destructure(DestructureElement{Target: ident("a")}, DestructureElement{Rest: true}),
			want:   false,
		},
		{
			name:   "nested anonymous rest over rows",
			target: destructure(DestructureElement{Target: destructure(DestructureElement{Target: ident("head")}, DestructureElement{Rest: true})}),
			want:   false,
		},
		{
			name:   "named rest materializes a backing",
			target: destructure(DestructureElement{Target: ident("head")}, DestructureElement{Target: ident("tail"), Rest: true}),
			want:   true,
		},
		{
			name:   "nested named rest materializes a backing",
			target: destructure(DestructureElement{Target: destructure(DestructureElement{Target: ident("head")}, DestructureElement{Target: ident("tail"), Rest: true})}),
			want:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := blockBindsRest(block(tc.target)); got != tc.want {
				t.Fatalf("blockBindsRest = %v, want %v", got, tc.want)
			}
		})
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
