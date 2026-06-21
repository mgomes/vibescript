package runtime

import (
	"fmt"
	"math"
	"math/big"
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
// defaults to 0; floats are rejected to match Ruby raising TypeError.
func roundDigitsArg(method string, args []Value) (int, error) {
	switch len(args) {
	case 0:
		return 0, nil
	case 1:
		if args[0].Kind() != KindInt {
			return 0, fmt.Errorf("%s precision must be an Integer", method)
		}
		n := args[0].Int()
		if n < math.MinInt32 || n > math.MaxInt32 {
			return 0, fmt.Errorf("%s precision out of range", method)
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

// accuratePow10 reports whether math.Pow(10, ndigits) is exact enough to scale a
// double without introducing decimal error. It mirrors Ruby's ACCURATE_POW10:
// past DBL_DIG digits the power of ten is no longer exactly representable, so
// the rational fallback must be used instead.
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
func floatRound(num float64, ndigits int, mode roundMode, method string) (Value, error) {
	if ndigits > 0 {
		return NewFloat(floatRoundDigits(num, ndigits, mode)), nil
	}
	if ndigits == 0 {
		whole, err := floatRoundToInt(num, mode, method)
		if err != nil {
			return NewNil(), err
		}
		return NewInt(whole), nil
	}

	return floatBucketNegative(num, ndigits, mode, method)
}

// floatBucketNegative implements Ruby's Float#round/#floor/#ceil for negative
// ndigits. Ruby first collapses the float to an (arbitrary precision) integer
// and then buckets that integer to the requested power of ten: round truncates
// toward zero (Float#to_i), floor takes the lower whole value, and ceil takes
// the upper one. Vibescript integers are int64, so the *final* bucket must fit
// int64, but the intermediate whole value need not: a huge float such as 9.3e18
// floors to 0 at precision -20 even though the whole value exceeds int64.
// Routing through math/big keeps the intermediate value exact and avoids the
// binary scaling error that direct float bucketing injects for large magnitudes.
func floatBucketNegative(num float64, ndigits int, mode roundMode, method string) (Value, error) {
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
	result, err := bigIntRound(bigWhole, ndigits, mode, method)
	if err != nil {
		return NewNil(), err
	}
	return NewInt(result), nil
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
		if !accuratePow10(ndigits) {
			return floatRoundByRational(num, ndigits, mode)
		}
		return floatFloorDigits(num, ndigits)
	case roundCeil:
		if num < 0 && floatRoundUnderflow(ndigits, binexp) {
			return math.Copysign(0, num)
		}
		if !accuratePow10(ndigits) {
			return floatRoundByRational(num, ndigits, mode)
		}
		s := math.Pow(10, float64(ndigits))
		return math.Ceil(num*s) / s
	default:
		if floatRoundUnderflow(ndigits, binexp) {
			return math.Copysign(0, num)
		}
		if !accuratePow10(ndigits) {
			return floatRoundByRational(num, ndigits, mode)
		}
		s := math.Pow(10, float64(ndigits))
		return roundHalfUp(num, s) / s
	}
}

// floatFloorDigits rounds num down to ndigits fractional digits using the same
// correction Ruby applies in rb_float_floor: scale, floor, then nudge up by one
// unit unless that overshoots the original value. This keeps decimal-looking
// inputs such as 1.005 from losing an extra unit to binary representation error.
func floatFloorDigits(num float64, ndigits int) float64 {
	s := math.Pow(10, float64(ndigits))
	mul := math.Floor(num * s)
	res := (mul + 1) / s
	if res > num {
		res = mul / s
	}
	return res
}

// floatRoundByRational rounds a finite float to ndigits fractional digits using
// exact rational arithmetic, mirroring Ruby's *_by_rational fallbacks. It is the
// path Ruby takes once 10^ndigits is no longer exactly representable as a double
// (ndigits >= DBL_DIG); scaling by such a power in binary would inject error and
// can yield Inf/Inf == NaN, so the result is computed from the float's exact
// value instead. The caller guarantees ndigits is positive and that the value is
// neither past the overflow threshold nor collapsed by the underflow guard, so
// 10^ndigits stays small enough to compute precisely.
func floatRoundByRational(num float64, ndigits int, mode roundMode) float64 {
	value := new(big.Rat).SetFloat64(num)
	if value == nil {
		return num // non-finite values never reach the rational path
	}
	scale := new(big.Rat).SetInt(pow10BigInt(ndigits))
	scaled := value.Mul(value, scale)

	rounded := ratToIntForMode(scaled, mode)
	result := new(big.Rat).SetInt(rounded)
	result.Quo(result, scale)

	f, _ := result.Float64()
	return f
}

// ratToIntForMode rounds a rational to an integer in the requested direction.
// roundNearest rounds halves away from zero, matching Ruby's default.
func ratToIntForMode(r *big.Rat, mode roundMode) *big.Int {
	switch mode {
	case roundFloor:
		return ratFloor(r)
	case roundCeil:
		return ratCeil(r)
	default:
		return ratRoundHalfAwayFromZero(r)
	}
}

// ratFloor returns the greatest integer not greater than r.
func ratFloor(r *big.Rat) *big.Int {
	q := new(big.Int)
	m := new(big.Int)
	// Euclidean division keeps the remainder non-negative, so q is the floor for
	// the always-positive denominator of a normalized big.Rat.
	q.DivMod(r.Num(), r.Denom(), m)
	return q
}

// ratCeil returns the least integer not less than r.
func ratCeil(r *big.Rat) *big.Int {
	q := new(big.Int)
	m := new(big.Int)
	q.DivMod(r.Num(), r.Denom(), m)
	if m.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
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

// bigIntRound buckets an arbitrary-precision integer to a power of ten for
// negative ndigits, mirroring Ruby's rb_int_floor/rb_int_ceil/rb_int_round, then
// converts the exact result to an int64. Vibescript integers are int64, so a
// bucket that lands outside that range reports an overflow instead of widening
// like Ruby's bignums; the intermediate value, however, may exceed int64.
func bigIntRound(n *big.Int, ndigits int, mode roundMode, method string) (int64, error) {
	if ndigits >= 0 || n.Sign() == 0 {
		return bigToInt64Checked(n, method)
	}

	digits := -ndigits
	// When 10^digits has strictly more decimal digits than |n|, it exceeds |n|,
	// so the toward-zero quotient is 0 and the result is fully determined by the
	// rounding direction. Resolving it here avoids materializing 10^digits with
	// math/big, which for an extreme precision such as round(-1000000000) would
	// allocate a billion-digit number and hang or exhaust memory before any
	// normal limit applied.
	if digits > decimalDigitCount(n) {
		return bigIntRoundBeyondMagnitude(n, digits, mode, method)
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
	return bigToInt64Checked(base, method)
}

// bigIntRoundBeyondMagnitude buckets n to 10^digits when that bucket strictly
// exceeds |n|, so the toward-zero base is 0 and only the away-from-zero target
// (+/-10^digits) can be nonzero. The caller guarantees digits >
// decimalDigitCount(n), which also means a half-way value can never reach the
// bucket, so round-to-nearest collapses to zero. This avoids ever building
// 10^digits when digits is astronomically large.
func bigIntRoundBeyondMagnitude(n *big.Int, digits int, mode roundMode, method string) (int64, error) {
	switch mode {
	case roundFloor:
		if n.Sign() > 0 {
			return 0, nil
		}
		return negPow10Int64Checked(digits, method)
	case roundCeil:
		if n.Sign() < 0 {
			return 0, nil
		}
		return posPow10Int64Checked(digits, method)
	default:
		// 10^digits > 10*|n| > 2*|n|, so n never reaches the half-way mark and
		// rounds toward zero.
		return 0, nil
	}
}

// posPow10Int64Checked returns 10^digits as an int64, reporting an overflow when
// the bucket exceeds the int64 range.
func posPow10Int64Checked(digits int, method string) (int64, error) {
	p, ok := pow10Int64(digits)
	if !ok {
		return 0, int64RangeError(method)
	}
	return p, nil
}

// negPow10Int64Checked returns -10^digits as an int64, reporting an overflow
// when the bucket exceeds the int64 range.
func negPow10Int64Checked(digits int, method string) (int64, error) {
	p, ok := pow10Int64(digits)
	if !ok {
		return 0, int64RangeError(method)
	}
	return -p, nil
}

// decimalDigitCount returns the number of base-10 digits in |n|, treating zero
// as a single digit.
func decimalDigitCount(n *big.Int) int {
	if n.Sign() == 0 {
		return 1
	}
	return len(new(big.Int).Abs(n).Text(10))
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

// floatRoundToInt rounds a float to the nearest whole number in the requested
// direction and converts it to an int64, applying the shared overflow check.
func floatRoundToInt(num float64, mode roundMode, method string) (int64, error) {
	var whole float64
	switch mode {
	case roundFloor:
		whole = math.Floor(num)
	case roundCeil:
		whole = math.Ceil(num)
	default:
		whole = roundHalfUp(num, 1.0)
	}
	return floatToInt64Checked(whole, method)
}

// intRound implements Ruby's Integer#round/#floor/#ceil. Non-negative ndigits
// leave the value unchanged; negative ndigits bucket it to the matching power
// of ten. Unlike Ruby's arbitrary-precision integers, results that exceed the
// int64 range report an overflow rather than widening.
func intRound(n int64, ndigits int, mode roundMode, method string) (int64, error) {
	if ndigits >= 0 {
		return n, nil
	}
	if n == 0 {
		return 0, nil
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
