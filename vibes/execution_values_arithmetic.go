package vibes

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

func addValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		return NewInt(left.Int() + right.Int()), nil
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		return NewFloat(left.Float() + right.Float()), nil
	case left.Kind() == KindTime && right.Kind() == KindDuration:
		return NewTime(left.Time().Add(time.Duration(right.Duration().Seconds()) * time.Second)), nil
	case right.Kind() == KindTime && left.Kind() == KindDuration:
		return NewTime(right.Time().Add(time.Duration(left.Duration().Seconds()) * time.Second)), nil
	case left.Kind() == KindDuration && right.Kind() == KindDuration:
		return NewDuration(Duration{seconds: left.Duration().Seconds() + right.Duration().Seconds()}), nil
	case left.Kind() == KindDuration && (right.Kind() == KindInt || right.Kind() == KindFloat):
		secs, err := valueToInt64(right)
		if err != nil {
			return NewNil(), fmt.Errorf("unsupported addition operands")
		}
		return NewDuration(Duration{seconds: left.Duration().Seconds() + secs}), nil
	case right.Kind() == KindDuration && (left.Kind() == KindInt || left.Kind() == KindFloat):
		secs, err := valueToInt64(left)
		if err != nil {
			return NewNil(), fmt.Errorf("unsupported addition operands")
		}
		return NewDuration(Duration{seconds: right.Duration().Seconds() + secs}), nil
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
		sum, err := left.Money().add(right.Money())
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
		return NewInt(left.Int() - right.Int()), nil
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		return NewFloat(left.Float() - right.Float()), nil
	case left.Kind() == KindTime && right.Kind() == KindDuration:
		return NewTime(left.Time().Add(-time.Duration(right.Duration().Seconds()) * time.Second)), nil
	case left.Kind() == KindTime && right.Kind() == KindTime:
		diff := left.Time().Sub(right.Time())
		return NewDuration(Duration{seconds: int64(diff / time.Second)}), nil
	case left.Kind() == KindDuration && right.Kind() == KindDuration:
		return NewDuration(Duration{seconds: left.Duration().Seconds() - right.Duration().Seconds()}), nil
	case left.Kind() == KindDuration && (right.Kind() == KindInt || right.Kind() == KindFloat):
		secs, err := valueToInt64(right)
		if err != nil {
			return NewNil(), fmt.Errorf("unsupported subtraction operands")
		}
		return NewDuration(Duration{seconds: left.Duration().Seconds() - secs}), nil
	case left.Kind() == KindArray && right.Kind() == KindArray:
		lArr := left.Array()
		rArr := right.Array()
		out := make([]Value, 0, len(lArr))
		for _, item := range lArr {
			found := slices.ContainsFunc(rArr, item.Equal)
			if !found {
				out = append(out, item)
			}
		}
		return NewArray(out), nil
	case left.Kind() == KindMoney && right.Kind() == KindMoney:
		diff, err := left.Money().sub(right.Money())
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
		return NewInt(left.Int() * right.Int()), nil
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		return NewFloat(left.Float() * right.Float()), nil
	case left.Kind() == KindDuration && (right.Kind() == KindInt || right.Kind() == KindFloat):
		secs, err := valueToInt64(right)
		if err != nil {
			return NewNil(), fmt.Errorf("unsupported multiplication operands")
		}
		return NewDuration(Duration{seconds: left.Duration().Seconds() * secs}), nil
	case right.Kind() == KindDuration && (left.Kind() == KindInt || left.Kind() == KindFloat):
		secs, err := valueToInt64(left)
		if err != nil {
			return NewNil(), fmt.Errorf("unsupported multiplication operands")
		}
		return NewDuration(Duration{seconds: right.Duration().Seconds() * secs}), nil
	case left.Kind() == KindMoney && right.Kind() == KindInt:
		return NewMoney(left.Money().mulInt(right.Int())), nil
	case left.Kind() == KindInt && right.Kind() == KindMoney:
		return NewMoney(right.Money().mulInt(left.Int())), nil
	default:
		return NewNil(), fmt.Errorf("unsupported multiplication operands")
	}
}

func divideValues(left, right Value) (Value, error) {
	switch {
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
		return NewDuration(Duration{seconds: left.Duration().Seconds() / secs}), nil
	case left.Kind() == KindMoney && right.Kind() == KindInt:
		res, err := left.Money().divInt(right.Int())
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
		return NewInt(left.Int() % right.Int()), nil
	}
	if left.Kind() == KindDuration && right.Kind() == KindDuration {
		if right.Duration().Seconds() == 0 {
			return NewNil(), errors.New("modulo by zero")
		}
		return NewDuration(Duration{seconds: left.Duration().Seconds() % right.Duration().Seconds()}), nil
	}
	return NewNil(), fmt.Errorf("unsupported modulo operands")
}

func compareValues(expr *BinaryExpr, left, right Value, cmp func(int) bool) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		diff := left.Int() - right.Int()
		switch {
		case diff < 0:
			return NewBool(cmp(-1)), nil
		case diff > 0:
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
		diff := left.Money().Cents() - right.Money().Cents()
		switch {
		case diff < 0:
			return NewBool(cmp(-1)), nil
		case diff > 0:
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
