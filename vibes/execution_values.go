package vibes

import (
	"fmt"
	"math"
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

func valueToInt64(val Value) (int64, error) {
	switch val.Kind() {
	case KindInt:
		return val.Int(), nil
	case KindFloat:
		return int64(val.Float()), nil
	default:
		return 0, fmt.Errorf("expected integer value")
	}
}

func valueToInt(val Value) (int, error) {
	switch val.Kind() {
	case KindInt:
		return int(val.Int()), nil
	case KindFloat:
		return int(val.Float()), nil
	default:
		return 0, fmt.Errorf("expected integer index")
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
			return 0, fmt.Errorf("money values with different currencies")
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
func flattenValues(values []Value, depth int) []Value {
	out := make([]Value, 0, len(values))
	for _, v := range values {
		if v.Kind() == KindArray && depth != 0 {
			nextDepth := depth
			if nextDepth > 0 {
				nextDepth--
			}
			out = append(out, flattenValues(v.Array(), nextDepth)...)
		} else {
			out = append(out, v)
		}
	}
	return out
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
