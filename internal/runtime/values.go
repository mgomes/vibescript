package runtime

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"reflect"
	"time"
)

const (
	maxFlattenDepth      = 1024
	nanosecondsPerSecond = int64(time.Second)
)

func valueToHashKey(val Value) (string, error) {
	switch val.Kind() {
	case KindSymbol:
		return val.String(), nil
	case KindString:
		return val.String(), nil
	default:
		return "", fmt.Errorf("unsupported hash key type %v", val.Kind())
	}
}

func valueToInt(val Value) (int, error) {
	switch val.Kind() {
	case KindInt:
		return int(val.Int()), nil
	case KindFloat:
		return int(val.Float()), nil
	default:
		return 0, fmt.Errorf("index must be integer")
	}
}

// errNegativeCount signals that a count argument was numeric but negative.
// Callers detect it with errors.Is to emit a method-specific message.
var errNegativeCount = errors.New("count must not be negative")

// errWidthNotInteger signals that a width argument was not a numeric value that
// could represent an integer. Callers detect it with errors.Is to emit a
// method-specific message.
var errWidthNotInteger = errors.New("width must be integer")

// errWidthOutOfRange signals that a width argument was a finite Float whose
// truncated value falls outside the native int range, or a non-finite Float
// (NaN/Inf). Callers detect it with errors.Is to emit a method-specific message
// mirroring Ruby's RangeError for such widths.
var errWidthOutOfRange = errors.New("width is out of range")

// valueToPadWidth converts a numeric width argument to an int, truncating
// fractional Floats toward zero like Ruby's to_int. Unlike valueToCount it
// permits negative widths because padding helpers treat a width at or below the
// receiver length as a no-op rather than an error. Non-finite Floats and Floats
// whose truncated magnitude exceeds the int range return errWidthOutOfRange so
// callers do not silently wrap a huge width into an in-range int (for example
// 1e20 collapsing to math.MinInt) and bypass the projected-size guard.
// Non-numeric values return errWidthNotInteger.
func valueToPadWidth(val Value) (int, error) {
	switch val.Kind() {
	case KindInt:
		return int(val.Int()), nil
	case KindFloat:
		f := val.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, errWidthOutOfRange
		}
		// Truncate toward zero first, matching Ruby's to_int, then verify the
		// result is representable as an int. float64(math.MaxInt) rounds up to
		// 2^63, so a strict `>` check would let exactly 2^63 through and then
		// int(2^63) overflows to math.MinInt; reject `>= float64(math.MaxInt)`
		// instead. float64(math.MinInt) is exactly -2^63, so `<` is correct.
		t := math.Trunc(f)
		if t >= float64(math.MaxInt) || t < float64(math.MinInt) {
			return 0, errWidthOutOfRange
		}
		return int(t), nil
	default:
		return 0, errWidthNotInteger
	}
}

// valueToCount converts a numeric count argument to a non-negative int,
// truncating positive fractional values toward zero like Ruby's to_int. It
// inspects the original numeric value's sign before truncating so that
// fractional negatives such as -0.5 are rejected rather than silently
// collapsing to 0. Numeric negatives return errNegativeCount; non-numeric
// values, NaN, and values outside the int range return a generic error.
func valueToCount(val Value) (int, error) {
	switch val.Kind() {
	case KindInt:
		if val.Int() < 0 {
			return 0, errNegativeCount
		}
		return int(val.Int()), nil
	case KindFloat:
		f := val.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) || f > math.MaxInt || f < math.MinInt {
			return 0, fmt.Errorf("count must be integer")
		}
		// Truncate toward zero first, matching Ruby's to_int (and Array#first/
		// #last), then reject only a negative integer. A fraction in (-1, 0)
		// therefore becomes 0 rather than an error.
		n := int(f)
		if n < 0 {
			return 0, errNegativeCount
		}
		return n, nil
	default:
		return 0, fmt.Errorf("count must be integer")
	}
}

func sortComparisonResult(val Value) (int, error) {
	switch val.Kind() {
	case KindInt:
		switch {
		case val.Int() < 0:
			return -1, nil
		case val.Int() > 0:
			return 1, nil
		default:
			return 0, nil
		}
	case KindFloat:
		switch {
		case val.Float() < 0:
			return -1, nil
		case val.Float() > 0:
			return 1, nil
		default:
			return 0, nil
		}
	default:
		return 0, fmt.Errorf("comparator must be numeric")
	}
}

func arraySortCompareValues(left, right Value) (int, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		switch {
		case left.Int() < right.Int():
			return -1, nil
		case left.Int() > right.Int():
			return 1, nil
		default:
			return 0, nil
		}
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		switch {
		case left.Float() < right.Float():
			return -1, nil
		case left.Float() > right.Float():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindString && right.Kind() == KindString:
		switch {
		case left.String() < right.String():
			return -1, nil
		case left.String() > right.String():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindSymbol && right.Kind() == KindSymbol:
		switch {
		case left.String() < right.String():
			return -1, nil
		case left.String() > right.String():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindBool && right.Kind() == KindBool:
		switch {
		case !left.Bool() && right.Bool():
			return -1, nil
		case left.Bool() && !right.Bool():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindDuration && right.Kind() == KindDuration:
		switch {
		case left.Duration().Seconds() < right.Duration().Seconds():
			return -1, nil
		case left.Duration().Seconds() > right.Duration().Seconds():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindTime && right.Kind() == KindTime:
		switch {
		case left.Time().Before(right.Time()):
			return -1, nil
		case left.Time().After(right.Time()):
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindMoney && right.Kind() == KindMoney:
		if left.Money().Currency() != right.Money().Currency() {
			return 0, fmt.Errorf("money currency mismatch for comparison")
		}
		switch {
		case left.Money().Cents() < right.Money().Cents():
			return -1, nil
		case left.Money().Cents() > right.Money().Cents():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindNil && right.Kind() == KindNil:
		return 0, nil
	default:
		return 0, fmt.Errorf("values are not comparable")
	}
}

// flattenValues recursively flattens nested arrays up to the specified depth.
// depth=-1 means flatten completely (no limit).
// depth=0 means don't flatten at all.
// depth=1 means flatten one level, etc.
type flattenState struct {
	arrays map[sliceIdentity]struct{}
	depth  int
}

func flattenValues(values []Value, depth int) ([]Value, error) {
	return flattenValuesWithState(values, depth, &flattenState{
		arrays: make(map[sliceIdentity]struct{}),
	})
}

func flattenValuesWithState(values []Value, depth int, state *flattenState) ([]Value, error) {
	if state.depth >= maxFlattenDepth {
		return nil, fmt.Errorf("array.flatten exceeded maximum depth")
	}

	id := sliceIdentity{
		Ptr: reflect.ValueOf(values).Pointer(),
		Len: len(values),
		Cap: cap(values),
	}
	if id.Ptr != 0 {
		if _, visiting := state.arrays[id]; visiting {
			return nil, fmt.Errorf("array.flatten does not support cyclic structures")
		}
		state.arrays[id] = struct{}{}
		defer delete(state.arrays, id)
	}

	state.depth++
	defer func() {
		state.depth--
	}()

	out := make([]Value, 0, len(values))
	for _, v := range values {
		if v.Kind() == KindArray && depth != 0 {
			nextDepth := depth
			if nextDepth > 0 {
				nextDepth--
			}
			flattened, err := flattenValuesWithState(v.Array(), nextDepth, state)
			if err != nil {
				return nil, err
			}
			out = append(out, flattened...)
		} else {
			out = append(out, v)
		}
	}
	return out, nil
}

func floatToInt64Checked(v float64, method string) (int64, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("%s result out of int64 range", method)
	}
	// float64(math.MaxInt64) rounds to 2^63, so use >= 2^63 as the true upper bound.
	if v < float64(math.MinInt64) || v >= math.Exp2(63) {
		return 0, fmt.Errorf("%s result out of int64 range", method)
	}
	return int64(v), nil
}

func int64RangeError(method string) error {
	return fmt.Errorf("%s result out of int64 range", method)
}

func addInt64Checked(left, right int64) (int64, bool) {
	sum := left + right
	if (left > 0 && right > 0 && sum < 0) || (left < 0 && right < 0 && sum >= 0) {
		return 0, false
	}
	return sum, true
}

func subInt64Checked(left, right int64) (int64, bool) {
	diff := left - right
	if (left^right)&(left^diff) < 0 {
		return 0, false
	}
	return diff, true
}

func mulInt64Checked(left, right int64) (int64, bool) {
	if left == 0 || right == 0 {
		return 0, true
	}
	negative := (left < 0) != (right < 0)
	lMag := uint64(left)
	if left < 0 {
		lMag = -lMag
	}
	rMag := uint64(right)
	if right < 0 {
		rMag = -rMag
	}
	hi, lo := bits.Mul64(lMag, rMag)
	if hi != 0 {
		return 0, false
	}
	if negative {
		minMagnitude := uint64(math.MaxInt64) + 1
		if lo > minMagnitude {
			return 0, false
		}
		if lo == minMagnitude {
			return math.MinInt64, true
		}
		return -int64(lo), true
	}
	if lo > uint64(math.MaxInt64) {
		return 0, false
	}
	return int64(lo), true
}

func floorDivIntChecked(left, right int64) (int64, bool) {
	if left == math.MinInt64 && right == -1 {
		return 0, false
	}
	return floorDivInt(left, right), true
}

func divInt64Checked(left, right int64) (int64, bool) {
	if left == math.MinInt64 && right == -1 {
		return 0, false
	}
	return left / right, true
}

func durationSecondsToTimeDuration(seconds int64, method string) (time.Duration, error) {
	if seconds > math.MaxInt64/nanosecondsPerSecond || seconds < math.MinInt64/nanosecondsPerSecond {
		return 0, int64RangeError(method)
	}
	return time.Duration(seconds) * time.Second, nil
}

func addValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		sum, ok := addInt64Checked(left.Int(), right.Int())
		if !ok {
			return NewNil(), int64RangeError("integer addition")
		}
		return NewInt(sum), nil
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		return NewFloat(left.Float() + right.Float()), nil
	case left.Kind() == KindTime && right.Kind() == KindDuration:
		delta, err := durationSecondsToTimeDuration(right.Duration().Seconds(), "time addition")
		if err != nil {
			return NewNil(), err
		}
		return NewTime(left.Time().Add(delta)), nil
	case right.Kind() == KindTime && left.Kind() == KindDuration:
		delta, err := durationSecondsToTimeDuration(left.Duration().Seconds(), "time addition")
		if err != nil {
			return NewNil(), err
		}
		return NewTime(right.Time().Add(delta)), nil
	case left.Kind() == KindDuration && right.Kind() == KindDuration:
		sum, ok := addInt64Checked(left.Duration().Seconds(), right.Duration().Seconds())
		if !ok {
			return NewNil(), int64RangeError("duration addition")
		}
		return NewDuration(durationFromSeconds(sum)), nil
	case left.Kind() == KindDuration && (right.Kind() == KindInt || right.Kind() == KindFloat):
		secs, err := valueToInt64(right)
		if err != nil {
			return NewNil(), fmt.Errorf("unsupported addition operands")
		}
		sum, ok := addInt64Checked(left.Duration().Seconds(), secs)
		if !ok {
			return NewNil(), int64RangeError("duration addition")
		}
		return NewDuration(durationFromSeconds(sum)), nil
	case right.Kind() == KindDuration && (left.Kind() == KindInt || left.Kind() == KindFloat):
		secs, err := valueToInt64(left)
		if err != nil {
			return NewNil(), fmt.Errorf("unsupported addition operands")
		}
		sum, ok := addInt64Checked(right.Duration().Seconds(), secs)
		if !ok {
			return NewNil(), int64RangeError("duration addition")
		}
		return NewDuration(durationFromSeconds(sum)), nil
	case left.Kind() == KindArray && right.Kind() == KindArray:
		lArr := left.Array()
		rArr := right.Array()
		out := make([]Value, len(lArr)+len(rArr))
		copy(out, lArr)
		copy(out[len(lArr):], rArr)
		return NewArray(out), nil
	case left.Kind() == KindString || right.Kind() == KindString:
		return NewString(left.String() + right.String()), nil
	case left.Kind() == KindMoney && right.Kind() == KindMoney:
		sum, err := left.Money().Add(right.Money())
		if err != nil {
			return NewNil(), err
		}
		return NewMoney(sum), nil
	default:
		return NewNil(), fmt.Errorf("unsupported addition operands")
	}
}

func subtractValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		diff, ok := subInt64Checked(left.Int(), right.Int())
		if !ok {
			return NewNil(), int64RangeError("integer subtraction")
		}
		return NewInt(diff), nil
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		return NewFloat(left.Float() - right.Float()), nil
	case left.Kind() == KindTime && right.Kind() == KindDuration:
		delta, err := durationSecondsToTimeDuration(right.Duration().Seconds(), "time subtraction")
		if err != nil {
			return NewNil(), err
		}
		return NewTime(left.Time().Add(-delta)), nil
	case left.Kind() == KindTime && right.Kind() == KindTime:
		diff := left.Time().Sub(right.Time())
		return NewDuration(durationFromSeconds(int64(diff / time.Second))), nil
	case left.Kind() == KindDuration && right.Kind() == KindDuration:
		diff, ok := subInt64Checked(left.Duration().Seconds(), right.Duration().Seconds())
		if !ok {
			return NewNil(), int64RangeError("duration subtraction")
		}
		return NewDuration(durationFromSeconds(diff)), nil
	case left.Kind() == KindDuration && (right.Kind() == KindInt || right.Kind() == KindFloat):
		secs, err := valueToInt64(right)
		if err != nil {
			return NewNil(), fmt.Errorf("unsupported subtraction operands")
		}
		diff, ok := subInt64Checked(left.Duration().Seconds(), secs)
		if !ok {
			return NewNil(), int64RangeError("duration subtraction")
		}
		return NewDuration(durationFromSeconds(diff)), nil
	case left.Kind() == KindArray && right.Kind() == KindArray:
		lArr := left.Array()
		rArr := right.Array()
		return NewArray(subtractArrayValues(lArr, rArr)), nil
	case left.Kind() == KindMoney && right.Kind() == KindMoney:
		diff, err := left.Money().Sub(right.Money())
		if err != nil {
			return NewNil(), err
		}
		return NewMoney(diff), nil
	default:
		return NewNil(), fmt.Errorf("unsupported subtraction operands")
	}
}

func multiplyValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		product, ok := mulInt64Checked(left.Int(), right.Int())
		if !ok {
			return NewNil(), int64RangeError("integer multiplication")
		}
		return NewInt(product), nil
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		return NewFloat(left.Float() * right.Float()), nil
	case left.Kind() == KindDuration && (right.Kind() == KindInt || right.Kind() == KindFloat):
		secs, err := valueToInt64(right)
		if err != nil {
			return NewNil(), fmt.Errorf("unsupported multiplication operands")
		}
		product, ok := mulInt64Checked(left.Duration().Seconds(), secs)
		if !ok {
			return NewNil(), int64RangeError("duration multiplication")
		}
		return NewDuration(durationFromSeconds(product)), nil
	case right.Kind() == KindDuration && (left.Kind() == KindInt || left.Kind() == KindFloat):
		secs, err := valueToInt64(left)
		if err != nil {
			return NewNil(), fmt.Errorf("unsupported multiplication operands")
		}
		product, ok := mulInt64Checked(right.Duration().Seconds(), secs)
		if !ok {
			return NewNil(), int64RangeError("duration multiplication")
		}
		return NewDuration(durationFromSeconds(product)), nil
	case left.Kind() == KindMoney && right.Kind() == KindInt:
		product, err := left.Money().MulInt(right.Int())
		if err != nil {
			return NewNil(), err
		}
		return NewMoney(product), nil
	case left.Kind() == KindInt && right.Kind() == KindMoney:
		product, err := right.Money().MulInt(left.Int())
		if err != nil {
			return NewNil(), err
		}
		return NewMoney(product), nil
	default:
		return NewNil(), fmt.Errorf("unsupported multiplication operands")
	}
}

func powerValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt && right.Int() >= 0:
		result, ok := powInt64Checked(left.Int(), right.Int())
		if !ok {
			return NewNil(), int64RangeError("integer exponentiation")
		}
		return NewInt(result), nil
	case isNumericValue(left) && isNumericValue(right):
		result := math.Pow(left.Float(), right.Float())
		if math.IsInf(result, 0) || math.IsNaN(result) {
			return NewNil(), errors.New("float exponentiation result is not finite")
		}
		return NewFloat(result), nil
	default:
		return NewNil(), fmt.Errorf("unsupported exponentiation operands")
	}
}

func powInt64Checked(base, exponent int64) (int64, bool) {
	result := int64(1)
	factor := base
	for exponent > 0 {
		if exponent%2 == 1 {
			var ok bool
			result, ok = mulInt64Checked(result, factor)
			if !ok {
				return 0, false
			}
		}
		exponent /= 2
		if exponent == 0 {
			break
		}
		var ok bool
		factor, ok = mulInt64Checked(factor, factor)
		if !ok {
			return 0, false
		}
	}
	return result, true
}

func isNumericValue(val Value) bool {
	return val.Kind() == KindInt || val.Kind() == KindFloat
}

func divideValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		if right.Int() == 0 {
			return NewNil(), errors.New("division by zero")
		}
		quotient, ok := floorDivIntChecked(left.Int(), right.Int())
		if !ok {
			return NewNil(), int64RangeError("integer division")
		}
		return NewInt(quotient), nil
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		if right.Float() == 0 {
			return NewNil(), errors.New("division by zero")
		}
		return NewFloat(left.Float() / right.Float()), nil
	case left.Kind() == KindDuration && right.Kind() == KindDuration:
		if right.Duration().Seconds() == 0 {
			return NewNil(), errors.New("division by zero")
		}
		return NewFloat(float64(left.Duration().Seconds()) / float64(right.Duration().Seconds())), nil
	case left.Kind() == KindDuration && (right.Kind() == KindInt || right.Kind() == KindFloat):
		secs, err := valueToInt64(right)
		if err != nil {
			return NewNil(), fmt.Errorf("unsupported division operands")
		}
		if secs == 0 {
			return NewNil(), errors.New("division by zero")
		}
		quotient, ok := divInt64Checked(left.Duration().Seconds(), secs)
		if !ok {
			return NewNil(), int64RangeError("duration division")
		}
		return NewDuration(durationFromSeconds(quotient)), nil
	case left.Kind() == KindMoney && right.Kind() == KindInt:
		res, err := left.Money().DivInt(right.Int())
		if err != nil {
			return NewNil(), err
		}
		return NewMoney(res), nil
	default:
		return NewNil(), fmt.Errorf("unsupported division operands")
	}
}

func moduloValues(left, right Value) (Value, error) {
	if left.Kind() == KindInt && right.Kind() == KindInt {
		if right.Int() == 0 {
			return NewNil(), errors.New("modulo by zero")
		}
		return NewInt(floorModInt(left.Int(), right.Int())), nil
	}
	if left.Kind() == KindDuration && right.Kind() == KindDuration {
		if right.Duration().Seconds() == 0 {
			return NewNil(), errors.New("modulo by zero")
		}
		return NewDuration(durationFromSeconds(left.Duration().Seconds() % right.Duration().Seconds())), nil
	}
	return NewNil(), fmt.Errorf("unsupported modulo operands")
}

func floorDivInt(left, right int64) int64 {
	quotient := left / right
	remainder := left % right
	if remainder != 0 && ((remainder < 0) != (right < 0)) {
		quotient--
	}
	return quotient
}

func floorModInt(left, right int64) int64 {
	remainder := left % right
	if remainder != 0 && ((remainder < 0) != (right < 0)) {
		remainder += right
	}
	return remainder
}

func compareValues(left, right Value, cmp func(int) bool) (Value, error) {
	order, err := compareValueOrder(left, right)
	if err != nil {
		return NewNil(), err
	}
	return NewBool(cmp(order)), nil
}

func compareValueOrder(left, right Value) (int, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		switch {
		case left.Int() < right.Int():
			return -1, nil
		case left.Int() > right.Int():
			return 1, nil
		default:
			return 0, nil
		}
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		lf, rf := left.Float(), right.Float()
		switch {
		case lf < rf:
			return -1, nil
		case lf > rf:
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindString && right.Kind() == KindString:
		switch {
		case left.String() < right.String():
			return -1, nil
		case left.String() > right.String():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindMoney && right.Kind() == KindMoney:
		if left.Money().Currency() != right.Money().Currency() {
			return 0, fmt.Errorf("money currency mismatch for comparison")
		}
		switch {
		case left.Money().Cents() < right.Money().Cents():
			return -1, nil
		case left.Money().Cents() > right.Money().Cents():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindDuration && right.Kind() == KindDuration:
		diff := left.Duration().Seconds() - right.Duration().Seconds()
		switch {
		case diff < 0:
			return -1, nil
		case diff > 0:
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindTime && right.Kind() == KindTime:
		switch {
		case left.Time().Before(right.Time()):
			return -1, nil
		case left.Time().After(right.Time()):
			return 1, nil
		default:
			return 0, nil
		}
	default:
		return 0, fmt.Errorf("unsupported comparison operands")
	}
}
