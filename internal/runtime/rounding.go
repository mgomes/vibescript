package runtime

import (
	"fmt"
	"math"
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

// dblDig mirrors the C DBL_DIG constant for IEEE 754 doubles and bounds the
// overflow/underflow guards ported from Ruby's numeric.c.
const dblDig = 15

// floatRoundOverflow reports whether ndigits is large enough that rounding
// cannot change the value, so the receiver is returned unchanged. It mirrors
// Ruby's float_round_overflow.
func floatRoundOverflow(ndigits, binexp int) bool {
	if binexp > 0 {
		return ndigits >= (dblDig+2)-binexp/4
	}
	return ndigits >= (dblDig+2)-(binexp/3-1)
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

	// Negative ndigits. round buckets in a single step so the half decision is
	// made at the requested precision, matching Ruby; floor and ceil first
	// collapse to a whole number and then bucket that integer.
	if mode == roundNearest {
		bucketed := floatRoundDigits(num, ndigits, mode)
		asInt, err := floatToInt64Checked(bucketed, method)
		if err != nil {
			return NewNil(), err
		}
		return NewInt(asInt), nil
	}
	whole, err := floatRoundToInt(num, mode, method)
	if err != nil {
		return NewNil(), err
	}
	result, err := intRound(whole, ndigits, mode, method)
	if err != nil {
		return NewNil(), err
	}
	return NewInt(result), nil
}

// floatRoundDigits rounds a float to ndigits fractional digits (ndigits > 0),
// returning a float. It honors Ruby's overflow/underflow guards so extreme
// precisions behave like Ruby rather than overflowing the scaling.
func floatRoundDigits(num float64, ndigits int, mode roundMode) float64 {
	if num == 0 || math.IsInf(num, 0) || math.IsNaN(num) {
		return num
	}
	_, binexp := math.Frexp(num)
	if floatRoundOverflow(ndigits, binexp) {
		return num
	}
	s := math.Pow(10, float64(ndigits))
	switch mode {
	case roundFloor:
		return math.Floor(num*s) / s
	case roundCeil:
		return math.Ceil(num*s) / s
	default:
		if floatRoundUnderflow(ndigits, binexp) {
			return math.Copysign(0, num)
		}
		return roundHalfUp(num, s) / s
	}
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
