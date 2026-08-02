package value_test

import (
	"errors"
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
