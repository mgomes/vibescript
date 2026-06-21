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
		"round", "floor", "ceil",
		"div", "divmod", "fdiv", "remainder", "modulo",
	}
	floatMemberNames = []string{
		"abs", "clamp", "round", "floor", "ceil",
		"zero?", "positive?", "negative?", "nonzero?",
		"nan?", "infinite?", "finite?",
		"div", "divmod", "fdiv", "remainder", "modulo",
	}
	moneyMemberNames = []string{"currency", "cents", "amount", "format"}
)

var (
	intBuiltinMemberNames = []string{
		"abs", "clamp", "even?", "odd?", "times",
		"zero?", "positive?", "negative?", "nonzero?", "next", "succ", "pred",
		"round", "floor", "ceil",
		"div", "divmod", "fdiv", "remainder", "modulo",
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
	case "round", "floor", "ceil":
		mode := roundModeFor(property)
		name := "int." + property
		return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			ndigits, err := roundDigitsArg(name, args)
			if err != nil {
				return NewNil(), err
			}
			result, err := intRound(receiver.Int(), ndigits, mode, name)
			if err != nil {
				return NewNil(), err
			}
			return NewInt(result), nil
		}), nil
	case "div":
		return NewAutoBuiltin("int.div", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			divisor, err := singleNumericArg("int.div", args)
			if err != nil {
				return NewNil(), err
			}
			return numericDiv("int.div", receiver, divisor)
		}), nil
	case "divmod":
		return NewAutoBuiltin("int.divmod", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			divisor, err := singleNumericArg("int.divmod", args)
			if err != nil {
				return NewNil(), err
			}
			return numericDivmod("int.divmod", receiver, divisor)
		}), nil
	case "fdiv":
		return NewAutoBuiltin("int.fdiv", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			divisor, err := singleNumericArg("int.fdiv", args)
			if err != nil {
				return NewNil(), err
			}
			return numericFdiv(receiver, divisor)
		}), nil
	case "remainder":
		return NewAutoBuiltin("int.remainder", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			divisor, err := singleNumericArg("int.remainder", args)
			if err != nil {
				return NewNil(), err
			}
			return numericRemainder("int.remainder", receiver, divisor)
		}), nil
	case "modulo":
		return NewAutoBuiltin("int.modulo", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			divisor, err := singleNumericArg("int.modulo", args)
			if err != nil {
				return NewNil(), err
			}
			return numericModulo("int.modulo", receiver, divisor)
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
	case "round", "floor", "ceil":
		mode := roundModeFor(property)
		name := "float." + property
		return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			ndigits, err := roundDigitsArg(name, args)
			if err != nil {
				return NewNil(), err
			}
			return floatRound(receiver.Float(), ndigits, mode, name)
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
	case "nan?":
		return NewAutoBuiltin("float.nan?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("float.nan? does not take arguments")
			}
			return NewBool(math.IsNaN(receiver.Float())), nil
		}), nil
	case "infinite?":
		// Ruby returns 1 for +Infinity, -1 for -Infinity, and nil otherwise
		// (including NaN and finite values), so the result is truthy exactly
		// when the receiver is infinite and carries the sign of the infinity.
		return NewAutoBuiltin("float.infinite?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("float.infinite? does not take arguments")
			}
			switch {
			case math.IsInf(receiver.Float(), 1):
				return NewInt(1), nil
			case math.IsInf(receiver.Float(), -1):
				return NewInt(-1), nil
			default:
				return NewNil(), nil
			}
		}), nil
	case "finite?":
		return NewAutoBuiltin("float.finite?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("float.finite? does not take arguments")
			}
			f := receiver.Float()
			return NewBool(!math.IsNaN(f) && !math.IsInf(f, 0)), nil
		}), nil
	case "div":
		return NewAutoBuiltin("float.div", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			divisor, err := singleNumericArg("float.div", args)
			if err != nil {
				return NewNil(), err
			}
			return numericDiv("float.div", receiver, divisor)
		}), nil
	case "divmod":
		return NewAutoBuiltin("float.divmod", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			divisor, err := singleNumericArg("float.divmod", args)
			if err != nil {
				return NewNil(), err
			}
			return numericDivmod("float.divmod", receiver, divisor)
		}), nil
	case "fdiv":
		return NewAutoBuiltin("float.fdiv", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			divisor, err := singleNumericArg("float.fdiv", args)
			if err != nil {
				return NewNil(), err
			}
			return numericFdiv(receiver, divisor)
		}), nil
	case "remainder":
		return NewAutoBuiltin("float.remainder", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			divisor, err := singleNumericArg("float.remainder", args)
			if err != nil {
				return NewNil(), err
			}
			return numericRemainder("float.remainder", receiver, divisor)
		}), nil
	case "modulo":
		return NewAutoBuiltin("float.modulo", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			divisor, err := singleNumericArg("float.modulo", args)
			if err != nil {
				return NewNil(), err
			}
			return numericModulo("float.modulo", receiver, divisor)
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown float method %s", property)
	}
}

// singleNumericArg validates that a numeric division helper received exactly
// one int or float argument and returns it.
func singleNumericArg(method string, args []Value) (Value, error) {
	if len(args) != 1 {
		return NewNil(), fmt.Errorf("%s expects one numeric argument", method)
	}
	if !isNumericValue(args[0]) {
		return NewNil(), fmt.Errorf("%s expects a numeric argument", method)
	}
	return args[0], nil
}

// numericFdiv implements Ruby's Numeric#fdiv, returning floating division. Like
// Vibescript's `/` operator, a zero divisor follows IEEE 754 rather than
// raising: a finite nonzero receiver yields +/-Infinity and a zero receiver
// yields NaN. This differs from div/divmod/modulo/remainder, which return
// integers or floored results and still raise on a zero divisor like Ruby.
func numericFdiv(receiver, divisor Value) (Value, error) {
	return NewFloat(receiver.Float() / divisor.Float()), nil
}

// numericDiv implements Ruby's Numeric#div: floored division returning an
// integer. Integer operands stay in exact 64-bit arithmetic; any float operand
// promotes to floating division before flooring. A zero divisor is an error,
// matching Ruby's ZeroDivisionError rather than yielding infinity.
func numericDiv(method string, receiver, divisor Value) (Value, error) {
	if receiver.Kind() == KindInt && divisor.Kind() == KindInt {
		if divisor.Int() == 0 {
			return NewNil(), fmt.Errorf("%s by zero", method)
		}
		quotient, ok := floorDivIntChecked(receiver.Int(), divisor.Int())
		if !ok {
			return NewNil(), int64RangeError(method)
		}
		return NewInt(quotient), nil
	}
	if divisor.Float() == 0 {
		return NewNil(), fmt.Errorf("%s by zero", method)
	}
	quotient, err := floatToInt64Checked(math.Floor(receiver.Float()/divisor.Float()), method)
	if err != nil {
		return NewNil(), err
	}
	return NewInt(quotient), nil
}

// numericDivmod implements Ruby's Numeric#divmod, returning a two-element array
// of the floored quotient and the modulo whose sign follows the divisor. With
// integer operands both results are integers; any float operand makes the
// modulo a float computed as `self - quotient * divisor`.
func numericDivmod(method string, receiver, divisor Value) (Value, error) {
	if receiver.Kind() == KindInt && divisor.Kind() == KindInt {
		if divisor.Int() == 0 {
			return NewNil(), fmt.Errorf("%s by zero", method)
		}
		quotient, ok := floorDivIntChecked(receiver.Int(), divisor.Int())
		if !ok {
			return NewNil(), int64RangeError(method)
		}
		modulo := floorModInt(receiver.Int(), divisor.Int())
		return NewArray([]Value{NewInt(quotient), NewInt(modulo)}), nil
	}
	d := divisor.Float()
	if d == 0 {
		return NewNil(), fmt.Errorf("%s by zero", method)
	}
	// Derive the modulo with the same floored math.Mod path as Numeric#modulo
	// and %, then recover the quotient from it, so divmod's modulo matches the
	// standalone modulo even for divisors that are not exactly representable.
	modulo := flooredFloatMod(receiver.Float(), d)
	quotient, err := floatToInt64Checked(math.Round((receiver.Float()-modulo)/d), method)
	if err != nil {
		return NewNil(), err
	}
	return NewArray([]Value{NewInt(quotient), NewFloat(modulo)}), nil
}

// numericRemainder implements Ruby's Numeric#remainder, whose sign follows the
// dividend. It uses truncated division (`self - divisor * (self / divisor).truncate`),
// which differs from `%` for operands of opposite sign. A zero divisor errors.
func numericRemainder(method string, receiver, divisor Value) (Value, error) {
	if receiver.Kind() == KindInt && divisor.Kind() == KindInt {
		if divisor.Int() == 0 {
			return NewNil(), fmt.Errorf("%s by zero", method)
		}
		return NewInt(receiver.Int() % divisor.Int()), nil
	}
	if divisor.Float() == 0 {
		return NewNil(), fmt.Errorf("%s by zero", method)
	}
	return NewFloat(math.Mod(receiver.Float(), divisor.Float())), nil
}

// numericModulo implements Ruby's modulo (the % operator): the result takes
// the sign of the divisor (floored division), unlike remainder which takes the
// sign of the dividend. Integer operands yield an integer; any float operand
// yields a float.
func numericModulo(method string, receiver, divisor Value) (Value, error) {
	if receiver.Kind() == KindInt && divisor.Kind() == KindInt {
		if divisor.Int() == 0 {
			return NewNil(), fmt.Errorf("%s by zero", method)
		}
		return NewInt(floorModInt(receiver.Int(), divisor.Int())), nil
	}
	d := divisor.Float()
	if d == 0 {
		return NewNil(), fmt.Errorf("%s by zero", method)
	}
	return NewFloat(flooredFloatMod(receiver.Float(), d)), nil
}

// flooredFloatMod returns num mod den with the result taking the sign of den
// (floored division), the float counterpart of Ruby's % operator. It is the
// shared basis for Numeric#modulo and Numeric#divmod so they stay consistent.
func flooredFloatMod(num, den float64) float64 {
	m := math.Mod(num, den)
	if m != 0 && (m < 0) != (den < 0) {
		m += den
	}
	return m
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
