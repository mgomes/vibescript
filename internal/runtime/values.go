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

func compareValues(expr *BinaryExpr, left, right Value, cmp func(int) bool) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		switch {
		case left.Int() < right.Int():
			return NewBool(cmp(-1)), nil
		case left.Int() > right.Int():
			return NewBool(cmp(1)), nil
		default:
			return NewBool(cmp(0)), nil
		}
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		lf, rf := left.Float(), right.Float()
		switch {
		case lf < rf:
			return NewBool(cmp(-1)), nil
		case lf > rf:
			return NewBool(cmp(1)), nil
		default:
			return NewBool(cmp(0)), nil
		}
	case left.Kind() == KindString && right.Kind() == KindString:
		switch {
		case left.String() < right.String():
			return NewBool(cmp(-1)), nil
		case left.String() > right.String():
			return NewBool(cmp(1)), nil
		default:
			return NewBool(cmp(0)), nil
		}
	case left.Kind() == KindMoney && right.Kind() == KindMoney:
		if left.Money().Currency() != right.Money().Currency() {
			return NewNil(), fmt.Errorf("money currency mismatch for comparison")
		}
		switch {
		case left.Money().Cents() < right.Money().Cents():
			return NewBool(cmp(-1)), nil
		case left.Money().Cents() > right.Money().Cents():
			return NewBool(cmp(1)), nil
		default:
			return NewBool(cmp(0)), nil
		}
	case left.Kind() == KindDuration && right.Kind() == KindDuration:
		diff := left.Duration().Seconds() - right.Duration().Seconds()
		switch {
		case diff < 0:
			return NewBool(cmp(-1)), nil
		case diff > 0:
			return NewBool(cmp(1)), nil
		default:
			return NewBool(cmp(0)), nil
		}
	case left.Kind() == KindTime && right.Kind() == KindTime:
		switch {
		case left.Time().Before(right.Time()):
			return NewBool(cmp(-1)), nil
		case left.Time().After(right.Time()):
			return NewBool(cmp(1)), nil
		default:
			return NewBool(cmp(0)), nil
		}
	default:
		return NewNil(), fmt.Errorf("unsupported comparison operands")
	}
}
