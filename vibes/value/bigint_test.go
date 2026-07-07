package value_test

import (
	"math"
	"math/big"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/vibes/value"
)

func bigFromString(t *testing.T, s string) *big.Int {
	t.Helper()
	bi, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("bad big literal %q", s)
	}
	return bi
}

func TestNewBigIntNormalizesToCompact(t *testing.T) {
	cases := []int64{0, 1, -1, math.MaxInt64, math.MinInt64}
	for _, n := range cases {
		v := value.NewBigInt(big.NewInt(n))
		if v.IsBigInt() {
			t.Fatalf("NewBigInt(%d) kept a big payload; want compact", n)
		}
		if v.Int() != n {
			t.Fatalf("NewBigInt(%d).Int() = %d", n, v.Int())
		}
		if !v.Equal(value.NewInt(n)) {
			t.Fatalf("NewBigInt(%d) != NewInt(%d)", n, n)
		}
	}
}

func TestNewBigIntNilYieldsZero(t *testing.T) {
	v := value.NewBigInt(nil)
	if v.Kind() != value.KindInt || v.Int() != 0 || v.IsBigInt() {
		t.Fatalf("NewBigInt(nil) = kind %v value %d big %v; want compact 0", v.Kind(), v.Int(), v.IsBigInt())
	}
}

func TestNewBigIntOutOfRangeKeepsBigPayload(t *testing.T) {
	in := bigFromString(t, "9223372036854775808") // 2^63
	v := value.NewBigInt(in)
	if !v.IsBigInt() {
		t.Fatalf("2^63 should carry a big payload")
	}
	if got := v.Int(); got != 0 {
		t.Fatalf("Int() on big payload = %d; want the 0 wrong-kind fallback", got)
	}
	if got := v.BigInt(); got.Cmp(in) != 0 {
		t.Fatalf("BigInt() = %s; want %s", got, in)
	}
}

func TestNewBigIntCopiesInput(t *testing.T) {
	in := bigFromString(t, "18446744073709551616") // 2^64
	v := value.NewBigInt(in)
	in.SetInt64(7)
	if got := v.BigInt(); got.String() != "18446744073709551616" {
		t.Fatalf("mutating the input changed the value: %s", got)
	}
}

func TestBigIntAccessorCopiesOut(t *testing.T) {
	v := value.NewBigInt(bigFromString(t, "18446744073709551616"))
	out := v.BigInt()
	out.SetInt64(7)
	if got := v.BigInt(); got.String() != "18446744073709551616" {
		t.Fatalf("mutating BigInt() result changed the value: %s", got)
	}
}

func TestBigIntAccessorCoversCompactValues(t *testing.T) {
	v := value.NewInt(-42)
	if got := v.BigInt(); got.Int64() != -42 {
		t.Fatalf("BigInt() on compact = %s", got)
	}
	if value.NewString("x").BigInt() != nil {
		t.Fatalf("BigInt() on non-int should be nil")
	}
}

func TestCompactInt(t *testing.T) {
	if n, ok := value.NewInt(5).CompactInt(); !ok || n != 5 {
		t.Fatalf("CompactInt on compact = (%d, %v)", n, ok)
	}
	if _, ok := value.NewBigInt(bigFromString(t, "18446744073709551616")).CompactInt(); ok {
		t.Fatalf("CompactInt on big payload should report !ok")
	}
	if _, ok := value.NewFloat(1).CompactInt(); ok {
		t.Fatalf("CompactInt on float should report !ok")
	}
}

func TestAdoptBigIntNormalizes(t *testing.T) {
	if value.AdoptBigInt(big.NewInt(9)).IsBigInt() {
		t.Fatalf("AdoptBigInt(9) should normalize to compact")
	}
	if !value.AdoptBigInt(bigFromString(t, "18446744073709551616")).IsBigInt() {
		t.Fatalf("AdoptBigInt(2^64) should keep the payload")
	}
}

func TestNewValueRoundTripsBigPayload(t *testing.T) {
	v := value.NewBigInt(bigFromString(t, "18446744073709551616"))
	data := v.Data()
	bi, ok := data.(*big.Int)
	if !ok {
		t.Fatalf("Data() on big payload = %T; want *big.Int", data)
	}
	round := value.NewValue(value.KindInt, bi)
	if !round.Equal(v) || !round.IsBigInt() {
		t.Fatalf("NewValue round trip lost the big payload")
	}
	// NewValue must renormalize so the canonical invariant holds even for a
	// payload that fits int64.
	if value.NewValue(value.KindInt, big.NewInt(3)).IsBigInt() {
		t.Fatalf("NewValue(KindInt, big 3) should normalize to compact")
	}
}

func TestBigIntStringAndInspect(t *testing.T) {
	pos := value.NewBigInt(bigFromString(t, "340282366920938463463374607431768211456"))
	neg := value.NewBigInt(bigFromString(t, "-340282366920938463463374607431768211456"))
	if pos.String() != "340282366920938463463374607431768211456" {
		t.Fatalf("String() = %q", pos.String())
	}
	if neg.Inspect() != "-340282366920938463463374607431768211456" {
		t.Fatalf("Inspect() = %q", neg.Inspect())
	}
	if got := pos.StringByteLen(); got != len(pos.String()) {
		t.Fatalf("StringByteLen = %d; want %d", got, len(pos.String()))
	}
}

func TestBigIntFloatConversion(t *testing.T) {
	v := value.NewBigInt(bigFromString(t, "100000000000000000000")) // 10^20
	if got := v.Float(); got != 1e20 {
		t.Fatalf("Float() = %v; want 1e20", got)
	}
	// Beyond float64 range: Ruby's Integer#to_f yields Infinity.
	huge := new(big.Int).Exp(big.NewInt(10), big.NewInt(400), nil)
	if got := value.NewBigInt(huge).Float(); !math.IsInf(got, 1) {
		t.Fatalf("Float() of 10^400 = %v; want +Inf", got)
	}
	if got := value.NewBigInt(new(big.Int).Neg(huge)).Float(); !math.IsInf(got, -1) {
		t.Fatalf("Float() of -10^400 = %v; want -Inf", got)
	}
}

func TestBigIntEqualityAndEql(t *testing.T) {
	a := value.NewBigInt(bigFromString(t, "18446744073709551616"))
	b := value.NewBigInt(bigFromString(t, "18446744073709551616"))
	c := value.NewBigInt(bigFromString(t, "18446744073709551617"))
	if !a.Equal(b) || !a.Eql(b) {
		t.Fatalf("equal big values must be == and eql?")
	}
	if a.Equal(c) {
		t.Fatalf("distinct big values must not be equal")
	}
	// Canonical invariant: big never equals compact, even a compact zero
	// (a big payload's Int() falls back to 0).
	if a.Equal(value.NewInt(0)) || value.NewInt(0).Equal(a) {
		t.Fatalf("big payload compared equal to compact 0")
	}
	if a.Equal(value.NewFloat(18446744073709551616.0)) {
		t.Fatalf("int must not equal float (no cross-kind ==)")
	}
}

func TestBigIntIdentity(t *testing.T) {
	a := value.NewBigInt(bigFromString(t, "18446744073709551616"))
	b := value.NewBigInt(bigFromString(t, "18446744073709551616"))
	if a.Identical(b) {
		t.Fatalf("independently built big values must not be identical (Ruby bignums are separate objects)")
	}
	if !a.Identical(a) {
		t.Fatalf("a big value must be identical to itself")
	}
	if !value.NewInt(5).Identical(value.NewInt(5)) {
		t.Fatalf("compact ints keep value identity")
	}
	if a.Identical(value.NewInt(0)) {
		t.Fatalf("big payload must not be identical to compact 0")
	}
}

func TestBigIntHashKeys(t *testing.T) {
	a := value.NewBigInt(bigFromString(t, "18446744073709551616"))
	b := value.NewBigInt(bigFromString(t, "18446744073709551616"))
	zero := value.NewInt(0)

	keyA, err := value.NewHashLookupKey(a)
	if err != nil {
		t.Fatalf("NewHashLookupKey(big): %v", err)
	}
	keyB, err := value.NewHashLookupKey(b)
	if err != nil {
		t.Fatalf("NewHashLookupKey(big): %v", err)
	}
	keyZero, err := value.NewHashLookupKey(zero)
	if err != nil {
		t.Fatalf("NewHashLookupKey(0): %v", err)
	}
	if keyA != keyB {
		t.Fatalf("equal big values must produce equal lookup keys")
	}
	if keyA == keyZero {
		t.Fatalf("big lookup key collided with compact 0")
	}
	if got := keyA.ExtraPayloadBytes(); got != len("10000000000000000") {
		t.Fatalf("ExtraPayloadBytes = %d; want canonical hex length", got)
	}
	if got := keyZero.ExtraPayloadBytes(); got != 0 {
		t.Fatalf("compact key ExtraPayloadBytes = %d; want 0", got)
	}

	canonA, err := value.HashKey(a)
	if err != nil {
		t.Fatalf("HashKey(big): %v", err)
	}
	// Big keys canonicalize in hexadecimal (linear in words, unlike decimal)
	// under their own prefix, which no other key space produces.
	if canonA != "bigint:10000000000000000" {
		t.Fatalf("HashKey(big) = %q", canonA)
	}

	h := value.NewTypedHash(0)
	if err := h.HashSet(a, value.NewInt(1)); err != nil {
		t.Fatalf("HashSet(big): %v", err)
	}
	got, ok, err := h.HashGet(b)
	if err != nil || !ok || got.Int() != 1 {
		t.Fatalf("HashGet(equal big) = (%v, %v, %v); want (1, true, nil)", got, ok, err)
	}
	if _, ok, _ := h.HashGet(zero); ok {
		t.Fatalf("compact 0 must not find the big key's entry")
	}

	// Crafted probes: no other key space may reach the big key's entry or
	// canonical form. A string spelling the canonical text (or the old
	// decimal spelling), the hex digits as a string, and a compact int whose
	// decimal matches the hex text must all stay distinct.
	for _, probe := range []value.Value{
		value.NewString("bigint:10000000000000000"),
		value.NewString("int:18446744073709551616"),
		value.NewString("10000000000000000"),
		value.NewInt(10000000000000000),
	} {
		if _, ok, _ := h.HashGet(probe); ok {
			t.Fatalf("crafted probe %s reached the big key's entry", probe.Inspect())
		}
		canonProbe, err := value.HashKey(probe)
		if err != nil {
			t.Fatalf("HashKey(probe): %v", err)
		}
		if canonProbe == canonA {
			t.Fatalf("crafted probe %s collided with the big canonical form %q", probe.Inspect(), canonA)
		}
	}
}

func TestValueToInt64RejectsBigWithoutTruncation(t *testing.T) {
	_, err := value.ValueToInt64(value.NewBigInt(bigFromString(t, "18446744073709551616")))
	if err == nil {
		t.Fatalf("ValueToInt64(big) must error")
	}
	if !strings.Contains(err.Error(), "must fit in a 64-bit integer") {
		t.Fatalf("error = %q; want the 64-bit family message", err)
	}
}

func TestBigIntStringBoundedFastReject(t *testing.T) {
	// ~33,220 decimal digits; a tiny limit must reject without paying for the
	// base conversion (this is behavioral: the partial output stays empty
	// instead of carrying limit bytes of converted digits).
	huge := new(big.Int).Lsh(big.NewInt(1), 110340)
	v := value.NewBigInt(huge)
	got, err := v.StringBounded(16)
	if err == nil {
		t.Fatalf("StringBounded on a 33k-digit value must truncate")
	}
	if got != "" {
		t.Fatalf("fast-rejected rendering should return empty partial output, got %d bytes", len(got))
	}
}
