package value_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/vibes/value"
)

// meteredContext returns a context whose charge hook accumulates billed bytes
// into total, and the pointer to read it from.
func meteredContext() (*value.EqualityContext, *int) {
	var ctx value.EqualityContext
	total := new(int)
	ctx.SetCharge(func(bytes int) error {
		*total += bytes
		return nil
	})
	return &ctx, total
}

// TestEqualityChargeBillsScalarPayloads pins the charge sites for #1135: a
// string or symbol comparison that passes the length screen bills its payload
// wherever the pair appears — top level, inside arrays, hashes, and objects —
// so the step quota can bound comparisons that reach big payloads through an
// aggregate.
func TestEqualityChargeBillsScalarPayloads(t *testing.T) {
	t.Parallel()

	payload := strings.Repeat("x", 1024)
	s := value.NewString(payload)
	sym := value.NewSymbol(payload)

	cases := []struct {
		name  string
		left  func() value.Value
		right func() value.Value
		want  int
	}{
		{"string_pair", func() value.Value { return s }, func() value.Value { return value.NewString(strings.Repeat("x", 1024)) }, 1024},
		{"symbol_pair", func() value.Value { return sym }, func() value.Value { return value.NewSymbol(strings.Repeat("x", 1024)) }, 1024},
		{"inside_array", func() value.Value { return value.NewArray([]value.Value{s}) }, func() value.Value { return value.NewArray([]value.Value{value.NewString(strings.Repeat("x", 1024))}) }, 1024},
		{"inside_untyped_hash", func() value.Value { return value.NewHash(map[string]value.Value{"k": s}) }, func() value.Value {
			return value.NewHash(map[string]value.Value{"k": value.NewString(strings.Repeat("x", 1024))})
		}, 1024 + 1},
		{"inside_object", func() value.Value { return value.NewObject(map[string]value.Value{"k": s}) }, func() value.Value {
			return value.NewObject(map[string]value.Value{"k": value.NewString(strings.Repeat("x", 1024))})
		}, 1024},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, total := meteredContext()
			if !ctx.Equal(tc.left(), tc.right()) {
				t.Fatalf("values must compare equal")
			}
			if ctx.Err() != nil {
				t.Fatalf("Err() = %v, want nil", ctx.Err())
			}
			if *total < tc.want {
				t.Fatalf("charged %d bytes, want at least %d", *total, tc.want)
			}
		})
	}
}

// TestEqualityChargeBillsTypedHashValues covers both typed-hash sub-paths:
// the linear probe at or below the small-hash limit and the map build above
// it. Both must bill the nested payloads they compare.
func TestEqualityChargeBillsTypedHashValues(t *testing.T) {
	t.Parallel()

	build := func(entries int) value.Value {
		h := value.NewTypedHash(entries)
		for i := range entries {
			key := value.NewSymbol(strings.Repeat("k", i+1))
			if err := h.HashSet(key, value.NewString(strings.Repeat("x", 512))); err != nil {
				t.Fatalf("HashSet: %v", err)
			}
		}
		return h
	}

	for _, entries := range []int{4, 12} {
		ctx, total := meteredContext()
		if !ctx.Equal(build(entries), build(entries)) {
			t.Fatalf("hashes with %d entries must compare equal", entries)
		}
		if want := entries * 512; *total < want {
			t.Fatalf("charged %d bytes for %d entries, want at least %d", *total, entries, want)
		}
	}
}

// TestEqualityChargeSkipsFreeAnswers pins the exemptions: a length mismatch
// answers without reading a byte, a kind mismatch answers from the kinds, and
// numeric pairs carry no payload — none may invoke the charge.
func TestEqualityChargeSkipsFreeAnswers(t *testing.T) {
	t.Parallel()

	big := value.NewString(strings.Repeat("x", 1024))
	cases := []struct {
		name        string
		left, right value.Value
	}{
		{"length_mismatch", big, value.NewString(strings.Repeat("x", 1023))},
		{"length_mismatch_in_array", value.NewArray([]value.Value{big}), value.NewArray([]value.Value{value.NewString("x")})},
		{"kind_mismatch", big, value.NewSymbol(strings.Repeat("x", 1024))},
		{"ints", value.NewInt(1), value.NewInt(1)},
		{"floats", value.NewFloat(1.5), value.NewFloat(1.5)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var ctx value.EqualityContext
			ctx.SetCharge(func(bytes int) error {
				t.Fatalf("charge invoked with %d bytes for a free answer", bytes)
				return nil
			})
			ctx.Equal(tc.left, tc.right)
			if ctx.Err() != nil {
				t.Fatalf("Err() = %v, want nil", ctx.Err())
			}
		})
	}
}

// TestEqualityChargeFailureIsSticky pins the failure contract: the first
// charge error makes the comparison answer false, surfaces through Err, and
// every later comparison on the same context returns immediately without
// invoking the charge again.
func TestEqualityChargeFailureIsSticky(t *testing.T) {
	t.Parallel()

	quotaErr := errors.New("quota exceeded")
	calls := 0
	var ctx value.EqualityContext
	ctx.SetCharge(func(int) error {
		calls++
		return quotaErr
	})

	s := value.NewString(strings.Repeat("x", 128))
	if ctx.Equal(s, value.NewString(strings.Repeat("x", 128))) {
		t.Fatal("a failed charge must answer false")
	}
	if !errors.Is(ctx.Err(), quotaErr) {
		t.Fatalf("Err() = %v, want %v", ctx.Err(), quotaErr)
	}
	if ctx.Equal(value.NewInt(1), value.NewInt(1)) {
		t.Fatal("comparisons after a charge failure must answer false")
	}
	if calls != 1 {
		t.Fatalf("charge invoked %d times, want 1 (sticky error must short-circuit)", calls)
	}
}

// TestEqualityChargeSharedDAGChargesOncePerPair pins the interaction with the
// visited set: a shared subtree is walked — and its payloads billed — once
// per distinct pair, so metering keeps the non-exponential walk the seen set
// provides.
func TestEqualityChargeSharedDAGChargesOncePerPair(t *testing.T) {
	t.Parallel()

	build := func() value.Value {
		leaf := value.NewArray([]value.Value{value.NewString(strings.Repeat("x", 256))})
		cur := leaf
		for range 16 {
			cur = value.NewArray([]value.Value{cur, cur})
		}
		return cur
	}

	ctx, total := meteredContext()
	if !ctx.Equal(build(), build()) {
		t.Fatal("shared DAGs must compare equal")
	}
	// One distinct pair per level: the leaf's 256 bytes are billed once, not
	// once per 2^16 paths.
	if *total != 256 {
		t.Fatalf("charged %d bytes, want exactly 256 (one visit per distinct pair)", *total)
	}
}

// TestEqualityChargeCyclesTerminate pins that metering does not disturb cycle
// handling: two cyclic structures compare with finitely many charge calls.
func TestEqualityChargeCyclesTerminate(t *testing.T) {
	t.Parallel()

	build := func() value.Value {
		inner := []value.Value{value.NewString(strings.Repeat("x", 64)), value.NewNil()}
		arr := value.NewArray(inner)
		inner[1] = arr
		return arr
	}

	ctx, total := meteredContext()
	if !ctx.Equal(build(), build()) {
		t.Fatal("equal cyclic structures must compare equal")
	}
	if *total == 0 || *total > 64*4 {
		t.Fatalf("charged %d bytes, want a small finite amount", *total)
	}
}

// TestEqualityChargeBillsStringKeys pins the related #1135 gap: hashing or
// comparing a string-like hash key reads its whole text, so key bytes are
// billed alongside value bytes on every hash-equality sub-path.
func TestEqualityChargeBillsStringKeys(t *testing.T) {
	t.Parallel()

	bigKey := strings.Repeat("k", 2048)
	build := func() value.Value {
		h := value.NewTypedHash(1)
		if err := h.HashSet(value.NewString(bigKey), value.NewInt(1)); err != nil {
			t.Fatalf("HashSet: %v", err)
		}
		return h
	}

	ctx, total := meteredContext()
	if !ctx.Equal(build(), build()) {
		t.Fatal("hashes must compare equal")
	}
	if *total < 2048 {
		t.Fatalf("charged %d bytes, want at least the key's %d", *total, 2048)
	}
}

// TestEqualityChargeBillsArrayKeys pins the recursive key charge: the
// typed-hash equality paths canonicalize an array key through
// NewHashLookupKey, copying its nested strings, so those bytes must be
// billed like a direct string key's.
func TestEqualityChargeBillsArrayKeys(t *testing.T) {
	t.Parallel()

	nested := strings.Repeat("k", 4096)
	build := func() value.Value {
		h := value.NewTypedHash(1)
		key := value.NewArray([]value.Value{value.NewString(nested)})
		if err := h.HashSet(key, value.NewInt(1)); err != nil {
			t.Fatalf("HashSet: %v", err)
		}
		return h
	}

	ctx, total := meteredContext()
	if !ctx.Equal(build(), build()) {
		t.Fatal("hashes with array keys must compare equal")
	}
	if *total < 4096 {
		t.Fatalf("charged %d bytes, want at least the nested key's %d", *total, 4096)
	}
}

// TestEqualityChargeBillsObjectKeys pins that object attribute names are
// billed like untyped hash keys: the comparison's map probe hashes the whole
// name, so two objects sharing a long attribute name must charge its bytes.
func TestEqualityChargeBillsObjectKeys(t *testing.T) {
	t.Parallel()

	name := strings.Repeat("k", 4096)
	build := func() value.Value {
		return value.NewObject(map[string]value.Value{name: value.NewInt(1)})
	}

	ctx, total := meteredContext()
	if !ctx.Equal(build(), build()) {
		t.Fatal("objects must compare equal")
	}
	if *total < 4096 {
		t.Fatalf("charged %d bytes, want at least the attribute name's %d", *total, 4096)
	}
}

// TestEqualityChargeBillsDisplayKeyCandidates pins the duplicate-display-key
// scan: a candidate key after the current entry renders through Inspect, so
// its payload must be charged before the rendering, even when it is not the
// outer entry — the mixed typed/untyped path reaches every later candidate
// per outer iteration.
func TestEqualityChargeBillsDisplayKeyCandidates(t *testing.T) {
	t.Parallel()

	nested := strings.Repeat("k", 8192)
	typed := value.NewTypedHash(2)
	if err := typed.HashSet(value.NewInt(1), value.NewInt(1)); err != nil {
		t.Fatalf("HashSet: %v", err)
	}
	if err := typed.HashSet(value.NewArray([]value.Value{value.NewString(nested)}), value.NewInt(2)); err != nil {
		t.Fatalf("HashSet: %v", err)
	}
	legacy := value.NewHash(map[string]value.Value{"a": value.NewInt(1), "b": value.NewInt(2)})

	ctx, total := meteredContext()
	ctx.Equal(typed, legacy)
	if ctx.Err() != nil {
		t.Fatalf("Err() = %v, want nil", ctx.Err())
	}
	if *total < 8192 {
		t.Fatalf("charged %d bytes, want at least the candidate key's %d", *total, 8192)
	}
}

// TestEqualityChargeBillsOverlappingResliceKeys pins the equality-side twin:
// the key-cost guard uses full slice-header identity, so a nested reslice of
// the parent's backing is billed like the distinct key it is rather than
// misread as a cycle.
func TestEqualityChargeBillsOverlappingResliceKeys(t *testing.T) {
	t.Parallel()

	payload := strings.Repeat("k", 32768)
	build := func() value.Value {
		elems := make([]value.Value, 2)
		elems[0] = value.NewString(payload)
		elems[1] = value.NewArray(elems[:1])
		h := value.NewTypedHash(1)
		if err := h.HashSet(value.NewArray(elems), value.NewInt(1)); err != nil {
			t.Fatalf("HashSet: %v", err)
		}
		return h
	}

	ctx, total := meteredContext()
	if !ctx.Equal(build(), build()) {
		t.Fatal("hashes with reslice keys must compare equal")
	}
	if *total < 2*len(payload) {
		t.Fatalf("charged %d bytes, want at least %d (both occurrences of the shared payload)", *total, 2*len(payload))
	}
}

// TestEqualityChargeIsDeterministic pins the traversal order: with Go's
// randomized map iteration, identical unequal hashes under the same quota
// alternated between answering false and exhausting the byte budget on the
// long equal entry. Sorted traversal makes both the total billed and the
// outcome identical on every run.
func TestEqualityChargeIsDeterministic(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 4096)
	build := func(short string) value.Value {
		return value.NewHash(map[string]value.Value{
			"a": value.NewString(long),
			"b": value.NewString(short),
		})
	}

	var firstTotal int
	for i := range 50 {
		ctx, total := meteredContext()
		if ctx.Equal(build("one"), build("two")) {
			t.Fatal("hashes with a differing entry must compare unequal")
		}
		if ctx.Err() != nil {
			t.Fatalf("Err() = %v, want nil", ctx.Err())
		}
		if i == 0 {
			firstTotal = *total
			continue
		}
		if *total != firstTotal {
			t.Fatalf("run %d charged %d bytes, run 0 charged %d; metered equality must be deterministic", i, *total, firstTotal)
		}
	}
}

// TestEqualityChargeBillsDeepArrayKeysPerLevel pins the equality twin of the
// per-level encoding charge: NewHashLookupKey's canonicalization copies the
// child encoding at every ancestor, so a depth-d chain must bill ~d times the
// leaf.
func TestEqualityChargeBillsDeepArrayKeysPerLevel(t *testing.T) {
	t.Parallel()

	payload := strings.Repeat("k", 4096)
	build := func(depth int) value.Value {
		key := value.NewArray([]value.Value{value.NewString(payload)})
		for range depth {
			key = value.NewArray([]value.Value{key})
		}
		h := value.NewTypedHash(1)
		if err := h.HashSet(key, value.NewInt(1)); err != nil {
			t.Fatalf("HashSet: %v", err)
		}
		return h
	}

	ctx, total := meteredContext()
	if !ctx.Equal(build(15), build(15)) {
		t.Fatal("hashes with deep keys must compare equal")
	}
	if want := 10 * len(payload); *total < want {
		t.Fatalf("charged %d bytes for a depth-16 chain, want at least %d (per-level copies)", *total, want)
	}
}

// TestEqualityScratchReserverValidatesSortAllocations pins the scratch hook:
// deterministic traversal allocates a key slice per map, the reserver sees
// the cumulative footprint before each allocation, and a reserver failure
// aborts the comparison through Err like a charge failure.
func TestEqualityScratchReserverValidatesSortAllocations(t *testing.T) {
	t.Parallel()

	build := func() value.Value {
		entries := make(map[string]value.Value, 12)
		for i := range 12 {
			entries[strings.Repeat("k", i+1)] = value.NewInt(int64(i))
		}
		return value.NewHash(entries)
	}

	// Deterministic sorting — and with it the scratch validation — engages
	// only on metered walks, so the contexts install both hooks, as the
	// runtime's do.
	var ctx value.EqualityContext
	ctx.SetCharge(func(int) error { return nil })
	seen := 0
	ctx.SetScratchReserver(func(bytes int) error {
		seen = bytes
		return nil
	})
	if !ctx.Equal(build(), build()) {
		t.Fatal("hashes must compare equal")
	}
	if seen < 12*24 {
		t.Fatalf("reserver saw %d bytes, want at least the key slice's %d", seen, 12*24)
	}

	var failing value.EqualityContext
	failing.SetCharge(func(int) error { return nil })
	boom := errors.New("no scratch headroom")
	failing.SetScratchReserver(func(int) error { return boom })
	if failing.Equal(build(), build()) {
		t.Fatal("a failed scratch reservation must answer false")
	}
	if !errors.Is(failing.Err(), boom) {
		t.Fatalf("Err() = %v, want %v", failing.Err(), boom)
	}
}

// TestEqualityChargeBillsRegexSources pins the regex leaf: comparing two
// independently backed equal-length pattern sources reads them in full, so
// the bytes must be billed like a string leaf's, with the same length
// screen.
func TestEqualityChargeBillsRegexSources(t *testing.T) {
	t.Parallel()

	source := strings.Repeat("a", 4096)
	build := func() value.Value {
		return value.NewRegex(value.Regex{Source: strings.Clone(source)})
	}

	ctx, total := meteredContext()
	if !ctx.Equal(value.NewArray([]value.Value{build()}), value.NewArray([]value.Value{build()})) {
		t.Fatal("equal regexes must compare equal")
	}
	if *total < 4096 {
		t.Fatalf("charged %d bytes, want at least the source's %d", *total, 4096)
	}
}

// TestEqualityScratchReleasesBetweenSiblings pins the live-scratch
// accounting: sibling maps in one walk allocate their key slices one after
// another, and the validator must see only the slices alive at each point —
// not the walk's lifetime total.
func TestEqualityScratchReleasesBetweenSiblings(t *testing.T) {
	t.Parallel()

	buildMap := func() value.Value {
		entries := make(map[string]value.Value, 8)
		for i := range 8 {
			entries[strings.Repeat("k", i+1)] = value.NewInt(int64(i))
		}
		return value.NewHash(entries)
	}
	build := func() value.Value {
		return value.NewArray([]value.Value{buildMap(), buildMap(), buildMap()})
	}

	var ctx value.EqualityContext
	ctx.SetCharge(func(int) error { return nil })
	maxSeen := 0
	ctx.SetScratchReserver(func(bytes int) error {
		maxSeen = max(maxSeen, bytes)
		return nil
	})
	if !ctx.Equal(build(), build()) {
		t.Fatal("arrays of sibling maps must compare equal")
	}
	perMap := 8 * 24
	if maxSeen > perMap {
		t.Fatalf("validator saw %d bytes, want at most one sibling's %d (dead slices must be released)", maxSeen, perMap)
	}
}

// TestMixedHashEqualityIsDeterministic pins the mixed legacy/typed path: a
// legacy hash's entries materialize in randomized map order, and under a
// quota covering one long comparison but not two, the same comparison must
// not alternate between false and a charge failure across runs.
func TestMixedHashEqualityIsDeterministic(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 4096)
	buildLegacy := func() value.Value {
		return value.NewHash(map[string]value.Value{
			"a": value.NewString(long),
			"b": value.NewString("short-one"),
		})
	}
	buildTyped := func() value.Value {
		h := value.NewTypedHash(2)
		if err := h.HashSet(value.NewString("a"), value.NewString(long)); err != nil {
			t.Fatalf("HashSet: %v", err)
		}
		if err := h.HashSet(value.NewString("b"), value.NewString("short-two")); err != nil {
			t.Fatalf("HashSet: %v", err)
		}
		return h
	}

	var firstTotal int
	for i := range 50 {
		ctx, total := meteredContext()
		if ctx.Equal(buildLegacy(), buildTyped()) {
			t.Fatal("hashes with a differing entry must compare unequal")
		}
		if ctx.Err() != nil {
			t.Fatalf("Err() = %v, want nil", ctx.Err())
		}
		if i == 0 {
			firstTotal = *total
			continue
		}
		if *total != firstTotal {
			t.Fatalf("run %d charged %d bytes, run 0 charged %d; mixed equality must be deterministic", i, *total, firstTotal)
		}
	}
}

// TestEqualityNilHookUnchanged pins that the zero context and the plain
// Value.Equal / Value.Eql entry points stay byte-identical in behavior with
// no hook installed.
func TestEqualityNilHookUnchanged(t *testing.T) {
	t.Parallel()

	s := value.NewString("hello")
	if !s.Equal(value.NewString("hello")) || s.Equal(value.NewString("world")) {
		t.Fatal("Value.Equal must behave as before")
	}
	if !value.NewInt(1).Equal(value.NewFloat(1.0)) {
		t.Fatal("cross-kind numeric equality must hold without a hook")
	}
	if value.NewInt(1).Eql(value.NewFloat(1.0)) {
		t.Fatal("Eql must stay kind-strict without a hook")
	}
	var ctx value.EqualityContext
	if !ctx.Equal(value.NewArray([]value.Value{s}), value.NewArray([]value.Value{value.NewString("hello")})) {
		t.Fatal("zero-value context must compare unmetered")
	}
	if ctx.Err() != nil {
		t.Fatalf("Err() = %v, want nil for unmetered context", ctx.Err())
	}
}

// TestEqualityChargeStopsAtFailedKeyCanonicalization pins the failure
// contract for a retained key that became unsupported after insertion (an
// inner key array mutated to hold an object): canonicalization stops at the
// failing element, so no ancestor copies the partial encoding and the charge
// must not grow with nesting depth — inflated ancestor copies turned the
// ordinary unequal answer into a spurious quota error.
func TestEqualityChargeStopsAtFailedKeyCanonicalization(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("k", 8*1024)
	build := func(depth int) value.Value {
		inner := value.NewArray([]value.Value{value.NewString(long)})
		key := inner
		for range depth {
			key = value.NewArray([]value.Value{key})
		}
		h := value.NewTypedHash(1)
		if err := h.HashSet(key, value.NewInt(1)); err != nil {
			t.Fatalf("HashSet: %v", err)
		}
		// The key becomes unsupported only after it is retained.
		inner.SetArrayElems(append(inner.Array(), value.NewObject(nil)))
		return h
	}

	charged := func(depth int) int {
		ctx, total := meteredContext()
		if ctx.Equal(build(depth), build(depth)) {
			t.Fatal("hashes with unsupported retained keys must compare unequal")
		}
		if err := ctx.Err(); err != nil {
			t.Fatalf("Err() = %v, want nil (unequal is the normal answer)", err)
		}
		return *total
	}

	atShallow := charged(6)
	atDeep := charged(18)
	if atShallow < len(long) {
		t.Fatalf("charged %d bytes, want at least the read prefix's %d", atShallow, len(long))
	}
	if atDeep >= atShallow*2 {
		t.Fatalf("charged %d bytes at depth 6 and %d at depth 18; ancestors never "+
			"copy a failed child encoding, so the charge must not scale with depth", atShallow, atDeep)
	}
}

// TestEqualityReservesRealizedDisplayKeyCapacity pins the composite-key
// rendering reservation on the mixed legacy/typed paths: the builder holding
// a rendered display key realizes the allocator's rounded capacity, not the
// projected length, so the reservation must cover the rounded size on both
// the sorted linear path (small hashes) and the map path, and a refused
// reservation must abort the comparison through Err.
func TestEqualityReservesRealizedDisplayKeyCapacity(t *testing.T) {
	t.Parallel()

	// 20 KiB of payload puts the rendering in a size-class range where the
	// realized capacity measurably exceeds the projected length, so reserving
	// the raw projection would under-account the retained builder.
	payload := strings.Repeat("k", 20*1024)
	compositeKey := func() value.Value {
		return value.NewArray([]value.Value{value.NewString(payload)})
	}
	projected := len(compositeKey().Inspect())
	var probe strings.Builder
	probe.Grow(projected)
	realized := probe.Cap()
	if realized <= projected {
		t.Fatalf("probe capacity %d must exceed the projection %d for the test to be meaningful", realized, projected)
	}
	// The rounder mirrors what the runtime installs: the capacity a builder
	// pregrown to the request actually realizes.
	liveRounder := func(n int) int {
		var b strings.Builder
		b.Grow(n)
		return b.Cap()
	}

	newContext := func() (*value.EqualityContext, *int) {
		var ctx value.EqualityContext
		ctx.SetCharge(func(int) error { return nil })
		maxSeen := new(int)
		ctx.SetScratchReserver(func(bytes int) error {
			*maxSeen = max(*maxSeen, bytes)
			return nil
		})
		ctx.SetScratchAllocRounder(liveRounder)
		return &ctx, maxSeen
	}

	buildTyped := func(stringKeys int) value.Value {
		h := value.NewTypedHash(stringKeys + 1)
		for i := range stringKeys {
			if err := h.HashSet(value.NewString(fmt.Sprintf("k%d", i)), value.NewInt(int64(i))); err != nil {
				t.Fatalf("HashSet: %v", err)
			}
		}
		if err := h.HashSet(compositeKey(), value.NewInt(99)); err != nil {
			t.Fatalf("HashSet: %v", err)
		}
		return h
	}
	buildLegacy := func(stringKeys int) value.Value {
		entries := make(map[string]value.Value, stringKeys+1)
		for i := range stringKeys + 1 {
			entries[fmt.Sprintf("k%d", i)] = value.NewInt(int64(i))
		}
		return value.NewHash(entries)
	}

	// Two entries per side stay under the small-hash limit: the sorted
	// linear path renders the composite key.
	ctx, maxSeen := newContext()
	if ctx.Equal(buildLegacy(1), buildTyped(1)) {
		t.Fatal("hashes with differing keys must compare unequal")
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if *maxSeen < realized {
		t.Fatalf("linear path reserved %d bytes, want at least the realized rendering capacity %d", *maxSeen, realized)
	}

	// Nine entries per side exceed the limit: the map path retains the
	// rendering for the whole comparison and must reserve it too.
	ctx, maxSeen = newContext()
	if ctx.Equal(buildLegacy(8), buildTyped(8)) {
		t.Fatal("hashes with differing keys must compare unequal")
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if *maxSeen < realized {
		t.Fatalf("map path reserved %d bytes, want at least the realized rendering capacity %d", *maxSeen, realized)
	}

	boom := errors.New("no scratch headroom")
	var failing value.EqualityContext
	failing.SetCharge(func(int) error { return nil })
	failing.SetScratchReserver(func(bytes int) error {
		if bytes >= realized {
			return boom
		}
		return nil
	})
	failing.SetScratchAllocRounder(liveRounder)
	if failing.Equal(buildLegacy(8), buildTyped(8)) {
		t.Fatal("a refused rendering reservation must answer false")
	}
	if !errors.Is(failing.Err(), boom) {
		t.Fatalf("Err() = %v, want %v", failing.Err(), boom)
	}
}
