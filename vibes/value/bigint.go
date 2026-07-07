package value

import "math/big"

// Vibescript exposes a single script-visible integer type (KindInt) with two
// internal representations:
//
//   - Compact: values that fit in int64 store their bits in Value.scalar with a
//     nil Value.data. This is the only representation such values ever use, so
//     the hot arithmetic path stays allocation-free.
//   - Big: values outside the int64 range carry a *big.Int payload in
//     Value.data.
//
// The canonical invariant: a big payload NEVER holds a value that fits in
// int64. Every producer (arithmetic, literals, conversions, host constructors)
// normalizes a result back to the compact form when it fits, so the two
// representations partition the integers into disjoint value spaces. Equality,
// hashing, and hash-key canonicalization rely on that disjointness: a compact
// int and a big payload can never be equal, so each representation compares
// within itself.
//
// Big payloads are immutable by construction: no code may mutate a *big.Int
// after it is wrapped in a Value. Constructors copy defensively where the
// caller retains the input.

// NewBigInt returns an integer Value holding i's value. The input is copied,
// so later mutations of i do not affect the returned Value. A value that fits
// in int64 is normalized to the same compact representation NewInt produces
// (see the canonical invariant above), so NewBigInt(big.NewInt(1)) and
// NewInt(1) are indistinguishable. A nil input yields the integer 0.
func NewBigInt(i *big.Int) Value {
	if i == nil {
		return NewInt(0)
	}
	if i.IsInt64() {
		return NewInt(i.Int64())
	}
	return Value{kind: KindInt, data: new(big.Int).Set(i)}
}

// AdoptBigInt wraps i in an integer Value without copying, taking ownership:
// the caller must not retain or mutate i afterwards, since big payloads are
// immutable once wrapped. Like NewBigInt it normalizes an int64-range value to
// the compact representation. It exists so the interpreter's arithmetic can
// promote freshly computed results without a defensive copy.
// It is intended for the interpreter's internal use; hosts should use
// NewBigInt, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func AdoptBigInt(i *big.Int) Value {
	if i == nil {
		return NewInt(0)
	}
	if i.IsInt64() {
		return NewInt(i.Int64())
	}
	return Value{kind: KindInt, data: i}
}

// IsBigInt reports whether v is an integer whose value lies outside the int64
// range (and therefore carries a big-integer payload). Int64-range integers
// always use the compact representation, so IsBigInt is equivalent to "this
// integer does not fit in an int64".
func (v Value) IsBigInt() bool {
	if v.kind != KindInt || v.data == nil {
		return false
	}
	_, ok := v.data.(*big.Int)
	return ok
}

// BigInt returns the integer content of v as a *big.Int for every KindInt
// value, compact or big. The result is a fresh copy the caller owns; mutating
// it never affects v. It returns nil when v is not an integer.
func (v Value) BigInt() *big.Int {
	if v.kind != KindInt {
		return nil
	}
	if bi, ok := v.data.(*big.Int); ok {
		return new(big.Int).Set(bi)
	}
	return big.NewInt(v.Int())
}

// CompactInt returns v's integer content and true when v is an integer that
// fits in int64 (the compact representation). It returns (0, false) for big
// integers and non-integers, letting callers that require an int64 distinguish
// a genuine zero from an out-of-range value, which Int alone cannot.
// It is intended for the interpreter's internal use; hosts should combine
// IsBigInt with Int, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func (v Value) CompactInt() (int64, bool) {
	if v.kind != KindInt {
		return 0, false
	}
	if v.data == nil {
		return int64(v.scalar), true
	}
	if i, ok := v.data.(int64); ok {
		return i, true
	}
	return 0, false
}

// EitherIntPayload reports whether either operand carries any payload beyond
// the compact scalar. Callers have already established both are KindInt, so a
// nil payload means the compact representation; the test is two nil compares,
// keeping the interpreter's compact arithmetic fast path free of type
// assertions. A true result sends the caller to the full big-integer probes.
// It is intended for the interpreter's internal use; hosts should use
// IsBigInt, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func EitherIntPayload(a, b Value) bool {
	return a.data != nil || b.data != nil
}

// BigIntPayload returns the big-integer payload of v without copying, and
// whether v carries one. Callers must treat the result as immutable.
// It is intended for the interpreter's internal use; hosts should use BigInt,
// and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func BigIntPayload(v Value) (*big.Int, bool) {
	// The nil probe short-circuits the compact representation (data is always
	// nil there) before the type assertion, keeping hot walks over compact
	// ints payload-free.
	if v.kind != KindInt || v.data == nil {
		return nil, false
	}
	bi, ok := v.data.(*big.Int)
	return bi, ok
}

// bigIntDecimalLenLowerBound returns a cheap lower bound on the byte length of
// bi's decimal rendering (sign included) computed from the bit length alone.
// The constant 30102/100000 is slightly below log10(2), so the bound never
// exceeds the true length; it is used to reject oversized renderings before
// paying for the (superlinear) base conversion.
func bigIntDecimalLenLowerBound(bi *big.Int) int {
	bits := bi.BitLen()
	if bits == 0 {
		return 1
	}
	digits := (bits-1)*30102/100000 + 1
	if bi.Sign() < 0 {
		digits++
	}
	return digits
}

// bigIntDecimalLenUpperBound returns a cheap upper bound on the byte length of
// bi's decimal rendering (sign included). The constant 30103/100000 is
// slightly above log10(2), so the bound never falls short of the true length;
// it sizes conservative charges for rendering work before the conversion runs.
func bigIntDecimalLenUpperBound(bi *big.Int) int {
	bits := bi.BitLen()
	if bits == 0 {
		return 1
	}
	digits := bits*30103/100000 + 1
	if bi.Sign() < 0 {
		digits++
	}
	return digits
}

// BigIntDecimalLenUpperBound is bigIntDecimalLenUpperBound for a Value known to
// carry a big payload; it returns 0 for every other value. The runtime uses it
// to preflight rendering and conversion work against its quotas before calling
// the superlinear base conversion.
// It is intended for the interpreter's internal use; hosts should not call
// it, and it carries no compatibility promise (see
// docs/embedding-api-stability.md).
func BigIntDecimalLenUpperBound(v Value) int {
	bi, ok := BigIntPayload(v)
	if !ok {
		return 0
	}
	return bigIntDecimalLenUpperBound(bi)
}

// bigIntRenderStepDigits is the number of decimal digits one sandbox step
// covers when a bounded projection charges for a big integer's base
// conversion. Charging digits/8 steps before calling big.Int.Text keeps the
// (superlinear) conversion inside the step quota: under the conventional 50k
// quota a rendering beyond ~400k digits trips before any conversion work runs.
const bigIntRenderStepDigits = 8

// chargeBigIntRenderSteps invokes step once per bigIntRenderStepDigits of v's
// projected decimal length when v carries a big payload, and is a no-op
// otherwise. Bounded projections call it BEFORE the base conversion so a step
// quota bounds the conversion's superlinear CPU rather than observing it after
// the fact.
func chargeBigIntRenderSteps(v Value, step func() error) error {
	bi, ok := BigIntPayload(v)
	if !ok {
		return nil
	}
	for range bigIntDecimalLenUpperBound(bi) / bigIntRenderStepDigits {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// bigIntRenderExceedsLimit reports whether v carries a big payload whose
// decimal rendering provably exceeds limit bytes, judged from the cheap
// bit-length lower bound alone. Bounded renderers use it to refuse an
// oversized rendering without paying for the base conversion; a false result
// means the rendering may or may not fit and must be measured exactly.
func bigIntRenderExceedsLimit(v Value, limit int) bool {
	if limit <= 0 {
		return false
	}
	bi, ok := BigIntPayload(v)
	if !ok {
		return false
	}
	return bigIntDecimalLenLowerBound(bi) > limit
}
