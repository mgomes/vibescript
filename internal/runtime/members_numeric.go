package runtime

import (
	"fmt"
	"math"
)

// The *MemberNames lists below mirror the names dispatched by the member
// functions next to them and feed "did you mean" suggestions on the error
// path. Keep each list in sync with its switch;
// TestMemberSuggestionCandidatesResolve enforces that every listed name
// resolves.
var (
	intMemberNames = []string{
		"seconds", "second", "minutes", "minute", "hours", "hour", "days", "day", "weeks", "week",
		"abs", "clamp", "even?", "odd?", "times",
		"zero?", "positive?", "negative?", "nonzero?", "next", "succ", "pred",
	}
	floatMemberNames = []string{
		"abs", "clamp", "round", "floor", "ceil",
		"zero?", "positive?", "negative?", "nonzero?",
	}
	moneyMemberNames = []string{"currency", "cents", "amount", "format"}
)

var (
	intBuiltinMemberNames = []string{
		"abs", "clamp", "even?", "odd?", "times",
		"zero?", "positive?", "negative?", "nonzero?", "next", "succ", "pred",
	}
	intBuiltinMembers       = newMemberTable(intBuiltinMemberNames)
	floatBuiltinMembers     = newMemberTable(floatMemberNames)
	moneyBuiltinMemberNames = []string{"format"}
	moneyBuiltinMembers     = newMemberTable(moneyBuiltinMemberNames)
)

func (exec *Execution) intMember(obj Value, property string, pos Position) (Value, error) {
	switch property {
	case "seconds", "second", "minutes", "minute", "hours", "hour", "days", "day":
		return NewDuration(secondsDuration(obj.Int(), property)), nil
	case "weeks", "week":
		return NewDuration(secondsDuration(obj.Int(), property)), nil
	default:
		if member, ok := intBuiltinMembers.lookup(property, intMemberBuiltin); ok {
			return member, nil
		}
		return NewNil(), exec.errorAt(pos, "unknown int method %s%s", property, didYouMean(property, intMemberNames))
	}
}

func intMemberBuiltin(property string) (Value, error) {
	switch property {
	case "abs":
		return NewAutoBuiltin("int.abs", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("int.abs does not take arguments")
			}
			n := receiver.Int()
			if n == math.MinInt64 {
				return NewNil(), fmt.Errorf("int.abs overflow")
			}
			if n < 0 {
				return NewInt(-n), nil
			}
			return receiver, nil
		}), nil
	case "clamp":
		return NewAutoBuiltin("int.clamp", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 2 {
				return NewNil(), fmt.Errorf("int.clamp expects min and max")
			}
			if args[0].Kind() != KindInt || args[1].Kind() != KindInt {
				return NewNil(), fmt.Errorf("int.clamp expects integer min and max")
			}
			minVal := args[0].Int()
			maxVal := args[1].Int()
			if minVal > maxVal {
				return NewNil(), fmt.Errorf("int.clamp min must be <= max")
			}
			n := receiver.Int()
			if n < minVal {
				return NewInt(minVal), nil
			}
			if n > maxVal {
				return NewInt(maxVal), nil
			}
			return receiver, nil
		}), nil
	case "even?":
		return NewAutoBuiltin("int.even?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("int.even? does not take arguments")
			}
			return NewBool(receiver.Int()%2 == 0), nil
		}), nil
	case "odd?":
		return NewAutoBuiltin("int.odd?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("int.odd? does not take arguments")
			}
			return NewBool(receiver.Int()%2 != 0), nil
		}), nil
	case "times":
		return NewAutoBuiltin("int.times", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("int.times does not take arguments")
			}
			if valueBlock(block) == nil {
				return NewNil(), fmt.Errorf("int.times requires a block")
			}
			count := receiver.Int()
			if count <= 0 {
				return receiver, nil
			}
			if count > int64(math.MaxInt) {
				return NewNil(), fmt.Errorf("int.times value too large")
			}
			runner, err := newBlockCallRunner(exec, block, "int.times")
			if err != nil {
				return NewNil(), err
			}
			var blockArg [1]Value
			for i := range int(count) {
				blockArg[0] = NewInt(int64(i))
				if _, err := runner.call(blockArg[:]); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "zero?":
		return NewAutoBuiltin("int.zero?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("int.zero? does not take arguments")
			}
			return NewBool(receiver.Int() == 0), nil
		}), nil
	case "positive?":
		return NewAutoBuiltin("int.positive?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("int.positive? does not take arguments")
			}
			return NewBool(receiver.Int() > 0), nil
		}), nil
	case "negative?":
		return NewAutoBuiltin("int.negative?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("int.negative? does not take arguments")
			}
			return NewBool(receiver.Int() < 0), nil
		}), nil
	case "nonzero?":
		// Ruby returns the receiver when nonzero and nil when zero, so the
		// result is truthy exactly when the number is nonzero.
		return NewAutoBuiltin("int.nonzero?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("int.nonzero? does not take arguments")
			}
			if receiver.Int() == 0 {
				return NewNil(), nil
			}
			return receiver, nil
		}), nil
	case "next", "succ":
		name := "int." + property
		return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("%s does not take arguments", name)
			}
			n := receiver.Int()
			if n == math.MaxInt64 {
				return NewNil(), fmt.Errorf("%s overflow", name)
			}
			return NewInt(n + 1), nil
		}), nil
	case "pred":
		return NewAutoBuiltin("int.pred", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("int.pred does not take arguments")
			}
			n := receiver.Int()
			if n == math.MinInt64 {
				return NewNil(), fmt.Errorf("int.pred overflow")
			}
			return NewInt(n - 1), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown int method %s", property)
	}
}

func (exec *Execution) floatMember(obj Value, property string, pos Position) (Value, error) {
	if member, ok := floatBuiltinMembers.lookup(property, floatMemberBuiltin); ok {
		return member, nil
	}
	return NewNil(), exec.errorAt(pos, "unknown float method %s%s", property, didYouMean(property, floatMemberNames))
}

func floatMemberBuiltin(property string) (Value, error) {
	switch property {
	case "abs":
		return NewAutoBuiltin("float.abs", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("float.abs does not take arguments")
			}
			return NewFloat(math.Abs(receiver.Float())), nil
		}), nil
	case "clamp":
		return NewAutoBuiltin("float.clamp", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 2 {
				return NewNil(), fmt.Errorf("float.clamp expects min and max")
			}
			if (args[0].Kind() != KindInt && args[0].Kind() != KindFloat) || (args[1].Kind() != KindInt && args[1].Kind() != KindFloat) {
				return NewNil(), fmt.Errorf("float.clamp expects numeric min and max")
			}
			minVal := args[0].Float()
			maxVal := args[1].Float()
			if minVal > maxVal {
				return NewNil(), fmt.Errorf("float.clamp min must be <= max")
			}
			n := receiver.Float()
			if n < minVal {
				return NewFloat(minVal), nil
			}
			if n > maxVal {
				return NewFloat(maxVal), nil
			}
			return receiver, nil
		}), nil
	case "round":
		return NewAutoBuiltin("float.round", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("float.round does not take arguments")
			}
			rounded := math.Round(receiver.Float())
			asInt, err := floatToInt64Checked(rounded, "float.round")
			if err != nil {
				return NewNil(), err
			}
			return NewInt(asInt), nil
		}), nil
	case "floor":
		return NewAutoBuiltin("float.floor", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("float.floor does not take arguments")
			}
			floored := math.Floor(receiver.Float())
			asInt, err := floatToInt64Checked(floored, "float.floor")
			if err != nil {
				return NewNil(), err
			}
			return NewInt(asInt), nil
		}), nil
	case "ceil":
		return NewAutoBuiltin("float.ceil", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("float.ceil does not take arguments")
			}
			ceiled := math.Ceil(receiver.Float())
			asInt, err := floatToInt64Checked(ceiled, "float.ceil")
			if err != nil {
				return NewNil(), err
			}
			return NewInt(asInt), nil
		}), nil
	case "zero?":
		return NewAutoBuiltin("float.zero?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("float.zero? does not take arguments")
			}
			return NewBool(receiver.Float() == 0), nil
		}), nil
	case "positive?":
		return NewAutoBuiltin("float.positive?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("float.positive? does not take arguments")
			}
			return NewBool(receiver.Float() > 0), nil
		}), nil
	case "negative?":
		return NewAutoBuiltin("float.negative?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("float.negative? does not take arguments")
			}
			return NewBool(receiver.Float() < 0), nil
		}), nil
	case "nonzero?":
		// Ruby returns the receiver when nonzero and nil when zero, so the
		// result is truthy exactly when the number is nonzero.
		return NewAutoBuiltin("float.nonzero?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("float.nonzero? does not take arguments")
			}
			if receiver.Float() == 0 {
				return NewNil(), nil
			}
			return receiver, nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown float method %s", property)
	}
}

func moneyMember(m Money, property string) (Value, error) {
	switch property {
	case "currency":
		return NewString(m.Currency()), nil
	case "cents":
		return NewInt(m.Cents()), nil
	case "amount":
		return NewString(m.String()), nil
	default:
		if member, ok := moneyBuiltinMembers.lookup(property, moneyMemberBuiltin); ok {
			return member, nil
		}
		return NewNil(), fmt.Errorf("unknown money member %s%s", property, didYouMean(property, moneyMemberNames))
	}
}

func moneyMemberBuiltin(property string) (Value, error) {
	switch property {
	case "format":
		return NewAutoBuiltin("money.format", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return NewString(receiver.Money().String()), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown money member %s", property)
	}
}
