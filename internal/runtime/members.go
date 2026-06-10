package runtime

import (
	"maps"
	"slices"
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
	case KindEnumValue:
		return exec.enumValueMember(obj, property, pos)
	case KindClass:
		return exec.classMember(obj, property, pos)
	case KindInstance:
		return exec.instanceMember(obj, property, pos)
	case KindInt:
		return exec.intMember(obj, property, pos)
	case KindFloat:
		return exec.floatMember(obj, property, pos)
	default:
		return NewNil(), exec.errorAt(pos, "unsupported member access on %s", obj.Kind())
	}
}

func (exec *Execution) classMember(obj Value, property string, pos Position) (Value, error) {
	cl := valueClass(obj)
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
	candidates := make([]string, 0, len(cl.ClassMethods)+len(cl.ClassVars)+1)
	candidates = append(candidates, "new")
	candidates = slices.AppendSeq(candidates, maps.Keys(cl.ClassMethods))
	candidates = slices.AppendSeq(candidates, maps.Keys(cl.ClassVars))
	return NewNil(), exec.errorAt(pos, "unknown class member %s%s", property, didYouMean(property, candidates))
}

func (exec *Execution) instanceMember(obj Value, property string, pos Position) (Value, error) {
	inst := valueInstance(obj)
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
	candidates := make([]string, 0, len(inst.Class.Methods)+len(inst.Ivars)+1)
	candidates = append(candidates, "class")
	candidates = slices.AppendSeq(candidates, maps.Keys(inst.Class.Methods))
	candidates = slices.AppendSeq(candidates, maps.Keys(inst.Ivars))
	return NewNil(), exec.errorAt(pos, "unknown member %s%s", property, didYouMean(property, candidates))
}

func (exec *Execution) getScopedMember(obj Value, property string, pos Position) (Value, error) {
	if obj.Kind() != KindEnum {
		return NewNil(), exec.errorAt(pos, "scoped member access is only supported on enums")
	}
	enumDef := valueEnum(obj)
	if enumDef == nil {
		return NewNil(), exec.errorAt(pos, "unknown enum %s", property)
	}
	member, ok := enumDef.Members[property]
	if !ok {
		candidates := slices.Collect(maps.Keys(enumDef.Members))
		return NewNil(), exec.errorAt(pos, "unknown enum member %s::%s%s", enumDef.Name, property, didYouMean(property, candidates))
	}
	return NewEnumValue(member), nil
}

func (exec *Execution) enumValueMember(obj Value, property string, pos Position) (Value, error) {
	member := valueEnumValue(obj)
	if member == nil {
		return NewNil(), exec.errorAt(pos, "unknown enum member")
	}
	switch property {
	case "name":
		return NewString(member.Name), nil
	case "symbol":
		return NewSymbol(member.Symbol), nil
	case "enum":
		return NewEnum(member.Enum), nil
	default:
		return NewNil(), exec.errorAt(pos, "unknown enum member property %s%s", property, didYouMean(property, []string{"name", "symbol", "enum"}))
	}
}
