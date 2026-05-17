package vibes

func enumDefsEqual(left, right *EnumDef) bool {
	if left == right {
		return true
	}
	if left == nil || right == nil {
		return false
	}
	if left.Name != right.Name {
		return false
	}
	if left.owner == nil || right.owner == nil {
		return false
	}
	return left.owner == right.owner
}

func enumValueDefsEqual(left, right *EnumValueDef) bool {
	if left == right {
		return true
	}
	if left == nil || right == nil {
		return false
	}
	return left.Name == right.Name &&
		left.Symbol == right.Symbol &&
		left.Index == right.Index &&
		enumDefsEqual(left.Enum, right.Enum)
}
