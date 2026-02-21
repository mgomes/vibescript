package vibes

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
