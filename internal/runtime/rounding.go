package runtime

import (
	"fmt"
	"math"
	"math/big"

	"github.com/mgomes/vibescript/vibes/value"
)

// roundMode selects the direction used by the numeric round/floor/ceil
// helpers. roundNearest rounds half away from zero, matching Ruby's default
// rounding mode.
type roundMode int

const (
	roundNearest roundMode = iota
	roundFloor
	roundCeil
)

// roundModeFor maps a member name ("round", "floor", or "ceil") to its
// roundMode. Callers only pass those three names.
func roundModeFor(property string) roundMode {
	switch property {
	case "floor":
		return roundFloor
	case "ceil":
		return roundCeil
	default:
		return roundNearest
	}
}

// roundDigitsArg validates the optional precision argument shared by the
// numeric round/floor/ceil helpers. Ruby accepts a single Integer ndigits that
// defaults to 0; floats are rejected to match Ruby raising TypeError. Ruby reads
// ndigits through NUM2INT, so a precision outside the 32-bit signed range raises
// RangeError ("too big/small to convert to int") rather than acting as a no-op;
// matching that boundary keeps high-precision rounding faithful to Ruby while
// also keeping the returned int safe on platforms where int is 32 bits.
func roundDigitsArg(method string, args []Value) (int, error) {
	switch len(args) {
	case 0:
		return 0, nil
	case 1:
		if args[0].Kind() != KindInt {
			return 0, fmt.Errorf("%s precision must be an Integer", method)
		}
		n, compact := args[0].CompactInt()
		if !compact {
			// A big precision is out of NUM2INT range either way; report the
			// matching RangeError direction without rendering the value.
			if args[0].BigInt().Sign() > 0 {
				return 0, fmt.Errorf("%s precision too big to convert to int", method)
			}
			return 0, fmt.Errorf("%s precision too small to convert to int", method)
		}
		if n > math.MaxInt32 {
			return 0, fmt.Errorf("%s precision %d too big to convert to int", method, n)
		}
		if n < math.MinInt32 {
			return 0, fmt.Errorf("%s precision %d too small to convert to int", method, n)
		}
		return int(n), nil
	default:
		return 0, fmt.Errorf("%s expects at most one precision argument", method)
	}
}

// dblDig mirrors the C DBL_DIG constant for IEEE 754 doubles: the number of
// decimal digits a double can represent without loss. It bounds the
// overflow/underflow guards and the accurate-scaling cutoff ported from Ruby's
// numeric.c.
const dblDig = 15

// floatRoundDig mirrors the float_dig enum (DBL_DIG + 2) local to Ruby's
// float_round_overflow. Once ndigits plus the value's decimal exponent reaches
// this many digits, scaling by 10^ndigits is already an integer, so rounding
// cannot change the value.
const floatRoundDig = dblDig + 2

// accuratePow10 reports whether 10^ndigits is exactly representable as a double,
// so Float#round can scale and unscale without the multiply injecting decimal
// error. It mirrors the ndigits > 14 cutoff in Ruby's flo_round: from DBL_DIG
// digits on, the power of ten is no longer exact and round must use the rational
// fallback. Float#floor/#ceil never need this because their direction-preserving
// correction tolerates the scaling, matching rb_float_floor/rb_float_ceil.
func accuratePow10(ndigits int) bool {
	return ndigits < dblDig
}

// floatRoundOverflow reports whether ndigits is large enough that rounding
// cannot change the value, so the receiver is returned unchanged. It mirrors
// Ruby's float_round_overflow.
func floatRoundOverflow(ndigits, binexp int) bool {
	if binexp > 0 {
		return ndigits >= floatRoundDig-binexp/4
	}
	return ndigits >= floatRoundDig-(binexp/3-1)
}

// floatRoundUnderflow reports whether ndigits is negative enough that rounding
// collapses the value to zero. It mirrors Ruby's float_round_underflow.
func floatRoundUnderflow(ndigits, binexp int) bool {
	if binexp > 0 {
		return ndigits < -(binexp/3 + 1)
	}
	return ndigits < -(binexp / 4)
}

// roundHalfUp scales x by s, rounds half away from zero, and corrects for the
// floating-point error introduced by the scaling so that decimal halves round
// as a person would expect. It mirrors Ruby's round_half_up.
func roundHalfUp(x, s float64) float64 {
	f := math.Round(x * s)
	if s == 1.0 {
		return f
	}
	if x > 0 {
		if (f+0.5)/s <= x {
			f++
		}
		return f
	}
	if (f-0.5)/s >= x {
		f--
	}
	return f
}

// floatRound implements Ruby's Float#round/#floor/#ceil. Positive ndigits keep
// the value a Float; zero or negative ndigits return an Integer, preserving the
// int64 overflow checks when converting the rounded value back to an integer.
func floatRound(exec *Execution, num float64, ndigits int, mode roundMode, method string) (Value, error) {
	if ndigits > 0 {
		return NewFloat(floatRoundDigits(num, ndigits, mode)), nil
	}
	if ndigits == 0 {
		if math.IsNaN(num) || math.IsInf(num, 0) {
			return NewNil(), int64RangeError(method)
		}
		var whole float64
		switch mode {
		case roundFloor:
			whole = math.Floor(num)
		case roundCeil:
			whole = math.Ceil(num)
		default:
			whole = roundHalfUp(num, 1.0)
		}
		// Whole values beyond int64 promote to big integers, matching Ruby's
		// Float#round/#floor/#ceil returning bignums.
		return floatWholeToIntValue(whole, method)
	}

	return floatBucketNegative(exec, num, ndigits, mode, method)
}

// floatBucketNegative implements Ruby's Float#round/#floor/#ceil for negative
// ndigits. Ruby first collapses the float to an (arbitrary precision) integer
// and then buckets that integer to the requested power of ten: round truncates
// toward zero (Float#to_i), floor takes the lower whole value, and ceil takes
// the upper one. Buckets beyond int64 promote to big integers (Ruby's results
// are bignums there). Routing through math/big keeps the intermediate value
// exact and avoids the binary scaling error that direct float bucketing
// injects for large magnitudes.
func floatBucketNegative(exec *Execution, num float64, ndigits int, mode roundMode, method string) (Value, error) {
	if math.IsNaN(num) || math.IsInf(num, 0) {
		return NewNil(), int64RangeError(method)
	}
	var whole float64
	switch mode {
	case roundFloor:
		whole = math.Floor(num)
	case roundCeil:
		whole = math.Ceil(num)
	default:
		whole = math.Trunc(num) // Float#to_i truncates toward zero
	}
	bigWhole, acc := big.NewFloat(whole).Int(nil)
	if acc != big.Exact {
		// math.Floor/Ceil/Trunc already produced an integral value, so the
		// conversion is always exact; guard defensively rather than silently
		// dropping a fractional part.
		return NewNil(), int64RangeError(method)
	}
	return bigIntRoundValue(exec, bigWhole, ndigits, mode)
}

// floatRoundDigits rounds a float to ndigits fractional digits (ndigits > 0),
// returning a float. It honors Ruby's overflow/underflow guards so extreme
// precisions behave like Ruby rather than overflowing the scaling, and falls
// back to exact rational arithmetic once the power of ten is no longer
// representable so representation error never decides the decimal result. Zero
// and negative ndigits are handled by floatRound via the integer bucket path.
func floatRoundDigits(num float64, ndigits int, mode roundMode) float64 {
	if num == 0 || math.IsInf(num, 0) || math.IsNaN(num) {
		return num
	}
	_, binexp := math.Frexp(num)
	if floatRoundOverflow(ndigits, binexp) {
		return num
	}
	switch mode {
	case roundFloor:
		// Only positive values underflow toward zero when floored: a negative
		// value floors away from zero to the first representable magnitude.
		if num > 0 && floatRoundUnderflow(ndigits, binexp) {
			return math.Copysign(0, num)
		}
		// Ruby's rb_float_floor never takes a rational path: it always scales by
		// the power of ten and corrects with floatFloorDigits, so flooring at high
		// precision (e.g. ndigits == DBL_DIG) keeps the receiver's decimal value
		// rather than dropping a unit to the exact-binary rational result.
		return floatFloorDigits(num, ndigits)
	case roundCeil:
		// Ceil of a tiny negative collapses to zero; Ruby yields +0.0, not the
		// -0.0 that carrying the receiver's sign would produce.
		if num < 0 && floatRoundUnderflow(ndigits, binexp) {
			return 0.0
		}
		// Like rb_float_ceil, ceil also scales by the power of ten directly for
		// every positive precision instead of falling back to exact rationals.
		return floatCeilDigits(num, ndigits)
	default:
		// A value that underflows to zero rounds to +0.0, matching Ruby, which
		// does not carry the sign of a tiny negative into the zero result.
		if floatRoundUnderflow(ndigits, binexp) {
			return 0.0
		}
		if !accuratePow10(ndigits) {
			return floatRoundByRational(num, ndigits)
		}
		s := pow10Float(ndigits)
		return roundHalfUp(num, s) / s
	}
}

// floatFloorDigits rounds num down to ndigits fractional digits using the same
// correction Ruby applies in rb_float_floor: scale, floor, then nudge up by one
// unit unless that overshoots the original value. This keeps decimal-looking
// inputs such as 1.005 from losing an extra unit to binary representation error.
func floatFloorDigits(num float64, ndigits int) float64 {
	s := pow10Float(ndigits)
	mul := math.Floor(num * s)
	res := (mul + 1) / s
	if res > num {
		res = mul / s
	}
	return res
}

// floatCeilDigits rounds num up to ndigits fractional digits, mirroring Ruby's
// rb_float_ceil exactly: scale by the power of ten, ceil, then unscale. Unlike
// rb_float_floor, the ceil path applies no extra unit correction, so this must
// not add one or it would diverge from Ruby on values such as
// (-45.0320962888666).ceil(14).
func floatCeilDigits(num float64, ndigits int) float64 {
	s := pow10Float(ndigits)
	return math.Ceil(num*s) / s
}

// floatRoundByRational rounds a finite float to ndigits fractional digits using
// exact rational arithmetic, mirroring Ruby's rb_flo_round_by_rational. Ruby only
// takes this fallback for Float#round (never floor/ceil), once 10^ndigits is no
// longer exactly representable as a double (ndigits >= DBL_DIG); scaling by such a
// power in binary would inject error and can yield Inf/Inf == NaN, so the result
// is computed from the float's exact value instead and rounded half away from
// zero. The caller guarantees ndigits is positive and that the value is neither
// past the overflow threshold nor collapsed by the underflow guard, so 10^ndigits
// stays small enough to compute precisely.
func floatRoundByRational(num float64, ndigits int) float64 {
	value := new(big.Rat).SetFloat64(num)
	if value == nil {
		return num // non-finite values never reach the rational path
	}
	scale := new(big.Rat).SetInt(pow10BigInt(ndigits))
	scaled := value.Mul(value, scale)

	rounded := ratRoundHalfAwayFromZero(scaled)
	result := new(big.Rat).SetInt(rounded)
	result.Quo(result, scale)

	f, _ := result.Float64()
	return f
}

// ratRoundHalfAwayFromZero rounds r to the nearest integer, breaking halves away
// from zero to match Ruby's default rounding mode.
func ratRoundHalfAwayFromZero(r *big.Rat) *big.Int {
	abs := new(big.Rat).Abs(r)
	q := new(big.Int)
	m := new(big.Int)
	q.DivMod(abs.Num(), abs.Denom(), m)
	twiceRem := new(big.Int).Lsh(m, 1) // 2 * remainder
	if twiceRem.Cmp(abs.Denom()) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if r.Sign() < 0 {
		q.Neg(q)
	}
	return q
}

// bigIntRoundValue buckets an arbitrary-precision integer to a power of ten
// for negative ndigits, mirroring Ruby's rb_int_floor/rb_int_ceil/rb_int_round.
// Results promote to big-integer Values (normalizing back to compact when they
// fit int64), matching Ruby's widening. exec, when non-nil, preflights a
// bucket materialization whose size is driven by ndigits rather than by the
// receiver (see bigIntRoundBeyondMagnitudeValue).
func bigIntRoundValue(exec *Execution, n *big.Int, ndigits int, mode roundMode) (Value, error) {
	if ndigits >= 0 || n.Sign() == 0 {
		return value.NewBigInt(n), nil
	}

	if ndigits == math.MinInt {
		// -ndigits would overflow where int is 32-bit (ndigits == math.MinInt32).
		// The magnitude dwarfs any representable value, so resolve it via the
		// beyond-magnitude path instead of negating.
		return bigIntRoundBeyondMagnitudeValue(exec, n, math.MaxInt, mode)
	}
	digits := -ndigits
	// When 10^digits has strictly more decimal digits than |n|, it exceeds |n|,
	// so the toward-zero quotient is 0 and the result is fully determined by the
	// rounding direction. Resolving it here avoids materializing 10^digits with
	// math/big unless the rounding direction genuinely produces it, and that
	// path preflights the allocation (see bigIntRoundBeyondMagnitudeValue).
	if digits > decimalDigitCount(n) {
		return bigIntRoundBeyondMagnitudeValue(exec, n, digits, mode)
	}

	p := pow10BigInt(digits)
	base := new(big.Int)
	r := new(big.Int)
	base.QuoRem(n, p, r) // truncated quotient; r shares the sign of n
	base.Mul(base, p)

	switch mode {
	case roundFloor:
		if n.Sign() < 0 && r.Sign() != 0 {
			base.Sub(base, p)
		}
	case roundCeil:
		if n.Sign() > 0 && r.Sign() != 0 {
			base.Add(base, p)
		}
	default:
		mag := new(big.Int).Abs(r)
		mag.Lsh(mag, 1) // 2 * |remainder|
		if mag.Cmp(p) >= 0 {
			if n.Sign() > 0 {
				base.Add(base, p)
			} else {
				base.Sub(base, p)
			}
		}
	}
	return value.AdoptBigInt(base), nil
}

// bigIntRoundBeyondMagnitudeValue buckets n to 10^digits when that bucket
// strictly exceeds |n|, so the toward-zero base is 0 and only the
// away-from-zero target (+/-10^digits) can be nonzero. The caller guarantees
// digits > decimalDigitCount(n), which also means a half-way value can never
// reach the bucket, so round-to-nearest collapses to zero. 10^digits is sized
// by the caller-supplied precision rather than by any existing value, so its
// materialization is preflighted against the quotas before it is built: a
// ceil(-1000000000) rejects in O(1) instead of allocating a billion-digit
// number.
func bigIntRoundBeyondMagnitudeValue(exec *Execution, n *big.Int, digits int, mode roundMode) (Value, error) {
	switch mode {
	case roundFloor:
		if n.Sign() > 0 {
			return NewInt(0), nil
		}
		p, err := pow10ValueChecked(exec, digits)
		if err != nil {
			return NewNil(), err
		}
		return negIntValueBig(p), nil
	case roundCeil:
		if n.Sign() < 0 {
			return NewInt(0), nil
		}
		return pow10ValueChecked(exec, digits)
	default:
		// 10^digits > 10*|n| > 2*|n|, so n never reaches the half-way mark and
		// rounds toward zero.
		return NewInt(0), nil
	}
}

// pow10ValueChecked materializes 10^digits as an integer Value after
// preflighting its projected size (roughly digits x log2(10) bits) against the
// memory quota and charging steps proportional to its word count.
func pow10ValueChecked(exec *Execution, digits int) (Value, error) {
	if p, ok := pow10Int64(digits); ok {
		return NewInt(p), nil
	}
	projWords := math.MaxInt / (estimatedBigIntWordBytes * 2)
	if digits < math.MaxInt/4 {
		projWords = (digits*10/3)/64 + 1
	}
	if exec != nil {
		if projWords > bigIntMulPreflightWords {
			if err := exec.checkProjectedBigIntBytes(projWords); err != nil {
				return NewNil(), err
			}
		}
		if err := exec.stepN(1 + projWords/bigIntStepWordsPerStep); err != nil {
			return NewNil(), err
		}
	}
	return value.AdoptBigInt(pow10BigInt(digits)), nil
}

// decimalDigitCount returns the number of base-10 digits in |n|, treating zero
// as a single digit. It derives the count from the bit length in O(1): the
// digit count of x is floor(log10 x) + 1, and log10 x lies in
// [(bits-1)·log10 2, bits·log10 2), so the bit-length bounds pin the count
// exactly for almost every value. When the bounds straddle a power of ten (a
// band the float epsilons keep at most a few candidates wide), the count is
// resolved with direct comparisons against 10^k instead of rendering the full
// decimal text — the base conversion is superlinear and used to run on every
// negative-precision rounding of a big receiver.
func decimalDigitCount(n *big.Int) int {
	if n.Sign() == 0 {
		return 1
	}
	bits := n.BitLen()
	const log10of2 = 0.30102999566398119
	// Loosen each bound by a hair so float rounding can only widen the band,
	// never exclude the true count.
	low := int(float64(bits-1)*log10of2-1e-9) + 1
	high := int(float64(bits)*log10of2+1e-9) + 1
	if low == high {
		return low
	}
	digits := low
	for digits < high {
		// |n| >= 10^digits means the count is at least digits+1.
		if n.CmpAbs(pow10BigInt(digits)) < 0 {
			break
		}
		digits++
	}
	return digits
}

// bigToInt64Checked converts n to an int64, reporting an overflow error when it
// does not fit.
func bigToInt64Checked(n *big.Int, method string) (int64, error) {
	if !n.IsInt64() {
		return 0, int64RangeError(method)
	}
	return n.Int64(), nil
}

// pow10BigInt returns 10^n as a *big.Int for n >= 0.
func pow10BigInt(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// pow10Float returns the IEEE-754 double nearest to 10^n for n >= 0, returning
// +Inf once 10^n exceeds the double range. It mirrors the correctly-rounded
// libm pow(10, n) Ruby relies on, which Go's math.Pow does not match for n >= 23
// (e.g. math.Pow(10, 305) lands a unit-in-the-last-place high). Using a faithful
// scale keeps high-precision Float#round/#floor/#ceil aligned with Ruby. The
// float-rounding overflow guard bounds n well under the magnitude where 10^n
// would be expensive to build, so the exact big.Int never grows unbounded.
func pow10Float(n int) float64 {
	// new(big.Float).SetInt holds 10^n exactly (its precision grows to the
	// integer's bit length), so Float64 performs a single correct rounding to the
	// nearest double rather than compounding intermediate error.
	f, _ := new(big.Float).SetInt(pow10BigInt(n)).Float64()
	return f
}

// floatWholeToIntValue converts an integral float into an integer Value,
// promoting finite magnitudes beyond int64 to big integers exactly as Ruby's
// Float#to_i/#floor/#ceil/#round return bignums. NaN and the infinities keep
// the historical rejection (Ruby raises FloatDomainError for them).
func floatWholeToIntValue(whole float64, method string) (Value, error) {
	if n, err := floatToInt64Checked(whole, method); err == nil {
		return NewInt(n), nil
	}
	if math.IsNaN(whole) || math.IsInf(whole, 0) {
		return NewNil(), int64RangeError(method)
	}
	bi, acc := big.NewFloat(whole).Int(nil)
	if acc != big.Exact {
		// The caller passes an integral value, so the conversion is always
		// exact; guard defensively rather than silently dropping a fraction.
		return NewNil(), int64RangeError(method)
	}
	return value.AdoptBigInt(bi), nil
}

// intRoundPromoting implements Ruby's Integer#round/#floor/#ceil for both
// integer representations: compact receivers take the exact int64 bucketing
// fast path, and results or receivers beyond int64 route through the
// arbitrary-precision bucketing, widening exactly as Ruby does.
func intRoundPromoting(exec *Execution, receiver Value, ndigits int, mode roundMode, method string) (Value, error) {
	if ndigits >= 0 {
		return receiver, nil
	}
	if n, ok := receiver.CompactInt(); ok {
		if result, err := intRound(n, ndigits, mode, method); err == nil {
			return NewInt(result), nil
		}
		// The bucket landed outside int64; recompute in arbitrary precision.
	}
	if err := exec.chargeBigIntReceiverSteps(receiver); err != nil {
		return NewNil(), err
	}
	return bigIntRoundValue(exec, bigIntOperand(receiver), ndigits, mode)
}

// intRound implements Ruby's Integer#round/#floor/#ceil for int64 receivers.
// Non-negative ndigits leave the value unchanged; negative ndigits bucket it
// to the matching power of ten. Results that exceed the int64 range report an
// overflow, which intRoundPromoting turns into an arbitrary-precision retry.
func intRound(n int64, ndigits int, mode roundMode, method string) (int64, error) {
	if ndigits >= 0 {
		return n, nil
	}
	if n == 0 {
		return 0, nil
	}

	if ndigits == math.MinInt {
		// -ndigits would overflow where int is 32-bit; the magnitude dwarfs any
		// int64 value, so resolve it via the beyond-magnitude path.
		return intRoundBeyondInt64(n, math.MaxInt, mode, method)
	}
	digits := -ndigits
	p, ok := pow10Int64(digits)
	if !ok {
		// 10^digits exceeds the int64 range, so n (which fits) is strictly
		// smaller in magnitude. The toward-zero multiple is therefore 0, and
		// any rounding that moves away from zero lands on ±10^digits, which
		// cannot be represented.
		return intRoundBeyondInt64(n, digits, mode, method)
	}

	q := n / p
	r := n % p // shares the sign of n
	base := q * p
	switch mode {
	case roundFloor:
		if n < 0 && r != 0 {
			return subInt64ForRound(base, p, method)
		}
		return base, nil
	case roundCeil:
		if n > 0 && r != 0 {
			return addInt64ForRound(base, p, method)
		}
		return base, nil
	default:
		mag := r
		if mag < 0 {
			mag = -mag
		}
		if uint64(mag)*2 < uint64(p) {
			return base, nil
		}
		if n > 0 {
			return addInt64ForRound(base, p, method)
		}
		return subInt64ForRound(base, p, method)
	}
}

// intRoundBeyondInt64 handles negative-precision integer rounding when
// 10^digits overflows int64. Only a zero result is representable; a result of
// ±10^digits reports an overflow.
func intRoundBeyondInt64(n int64, digits int, mode roundMode, method string) (int64, error) {
	switch mode {
	case roundFloor:
		if n > 0 {
			return 0, nil
		}
		return 0, int64RangeError(method)
	case roundCeil:
		if n < 0 {
			return 0, nil
		}
		return 0, int64RangeError(method)
	default:
		mag := uint64(n)
		if n < 0 {
			mag = -mag
		}
		// Round half away from zero: |n| rounds up to 10^digits when it reaches
		// half the bucket. Comparing against 10^digits/2 (always exact, since
		// 10^digits is even for digits >= 1) avoids overflowing |n|*2. Buckets
		// past 10^20 exceed uint64, so any value stays below half.
		bucket, ok := pow10Uint64(digits)
		if !ok || mag < bucket/2 {
			return 0, nil
		}
		return 0, int64RangeError(method)
	}
}

func addInt64ForRound(left, right int64, method string) (int64, error) {
	sum, ok := addInt64Checked(left, right)
	if !ok {
		return 0, int64RangeError(method)
	}
	return sum, nil
}

func subInt64ForRound(left, right int64, method string) (int64, error) {
	diff, ok := subInt64Checked(left, right)
	if !ok {
		return 0, int64RangeError(method)
	}
	return diff, nil
}

// pow10Int64 returns 10^n as an int64 and reports whether it fits.
func pow10Int64(n int) (int64, bool) {
	result := int64(1)
	for range n {
		next, ok := mulInt64Checked(result, 10)
		if !ok {
			return 0, false
		}
		result = next
	}
	return result, true
}

// pow10Uint64 returns 10^n as a uint64 and reports whether it fits, extending
// the representable range one decimal place past pow10Int64.
func pow10Uint64(n int) (uint64, bool) {
	result := uint64(1)
	for range n {
		if result > math.MaxUint64/10 {
			return 0, false
		}
		result *= 10
	}
	return result, true
}
