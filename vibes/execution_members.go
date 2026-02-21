package vibes

import (
	"fmt"
	"math"
)

func (exec *Execution) getMember(obj Value, property string, pos Position) (Value, error) {
	switch obj.Kind() {
	case KindHash, KindObject:
		if val, ok := obj.Hash()[property]; ok {
			return val, nil
		}
		member, err := hashMember(obj, property)
		if err != nil {
			return NewNil(), err
		}
		return member, nil
	case KindMoney:
		return moneyMember(obj.Money(), property)
	case KindDuration:
		return durationMember(obj.Duration(), property, pos)
	case KindTime:
		return timeMember(obj.Time(), property)
	case KindArray:
		return arrayMember(obj, property)
	case KindString:
		return stringMember(obj, property)
	case KindClass:
		cl := obj.Class()
		if property == "new" {
			return NewAutoBuiltin(cl.Name+".new", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				inst := &Instance{Class: cl, Ivars: make(map[string]Value)}
				instVal := NewInstance(inst)
				if initFn, ok := cl.Methods["initialize"]; ok {
					if _, err := exec.callFunction(initFn, instVal, args, kwargs, block, pos); err != nil {
						return NewNil(), err
					}
				}
				return instVal, nil
			}), nil
		}
		if fn, ok := cl.ClassMethods[property]; ok {
			if fn.Private && !exec.isCurrentReceiver(obj) {
				return NewNil(), exec.errorAt(pos, "private method %s", property)
			}
			return NewAutoBuiltin(cl.Name+"."+property, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return exec.callFunction(fn, obj, args, kwargs, block, pos)
			}), nil
		}
		if val, ok := cl.ClassVars[property]; ok {
			return val, nil
		}
		return NewNil(), exec.errorAt(pos, "unknown class member %s", property)
	case KindInstance:
		inst := obj.Instance()
		if property == "class" {
			return NewClass(inst.Class), nil
		}
		if fn, ok := inst.Class.Methods[property]; ok {
			if fn.Private && !exec.isCurrentReceiver(obj) {
				return NewNil(), exec.errorAt(pos, "private method %s", property)
			}
			return NewAutoBuiltin(inst.Class.Name+"#"+property, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return exec.callFunction(fn, obj, args, kwargs, block, pos)
			}), nil
		}
		if val, ok := inst.Ivars[property]; ok {
			return val, nil
		}
		return NewNil(), exec.errorAt(pos, "unknown member %s", property)
	case KindInt:
		switch property {
		case "seconds", "second", "minutes", "minute", "hours", "hour", "days", "day":
			return NewDuration(secondsDuration(obj.Int(), property)), nil
		case "weeks", "week":
			return NewDuration(secondsDuration(obj.Int(), property)), nil
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
				if block.Block() == nil {
					return NewNil(), fmt.Errorf("int.times requires a block")
				}
				count := receiver.Int()
				if count <= 0 {
					return receiver, nil
				}
				if count > int64(math.MaxInt) {
					return NewNil(), fmt.Errorf("int.times value too large")
				}
				for i := range int(count) {
					if _, err := exec.CallBlock(block, []Value{NewInt(int64(i))}); err != nil {
						return NewNil(), err
					}
				}
				return receiver, nil
			}), nil
		default:
			return NewNil(), exec.errorAt(pos, "unknown int member %s", property)
		}
	case KindFloat:
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
		default:
			return NewNil(), exec.errorAt(pos, "unknown float member %s", property)
		}
	default:
		return NewNil(), exec.errorAt(pos, "unsupported member access on %s", obj.Kind())
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
	case "format":
		return NewAutoBuiltin("money.format", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return NewString(m.String()), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown money member %s", property)
	}
}
