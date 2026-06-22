package runtime

type scalarValueSetKey struct {
	kind     ValueKind
	boolVal  bool
	intVal   int64
	floatVal float64
	textVal  string
	moneyVal Money
	durVal   Duration
	rangeVal Range
}

func scalarValueKey(v Value) (scalarValueSetKey, bool) {
	key := scalarValueSetKey{kind: v.Kind()}
	switch v.Kind() {
	case KindNil:
	case KindBool:
		key.boolVal = v.Bool()
	case KindInt:
		key.intVal = v.Int()
	case KindFloat:
		key.floatVal = v.Float()
	case KindString, KindSymbol:
		key.textVal = v.String()
	case KindMoney:
		key.moneyVal = v.Money()
	case KindDuration:
		key.durVal = v.Duration()
	case KindRange:
		key.rangeVal = v.Range()
	default:
		return scalarValueSetKey{}, false
	}
	return key, true
}

func uniqueValues(values []Value) []Value {
	var seenScalars map[scalarValueSetKey]struct{}
	seenComposite := make([]Value, 0)
	unique := make([]Value, 0, len(values))

	for _, item := range values {
		if key, ok := scalarValueKey(item); ok {
			if seenScalars == nil {
				seenScalars = make(map[scalarValueSetKey]struct{}, len(values))
			}
			if _, found := seenScalars[key]; found {
				continue
			}
			seenScalars[key] = struct{}{}
			unique = append(unique, item)
			continue
		}

		if containsEqualValue(seenComposite, item) {
			continue
		}
		seenComposite = append(seenComposite, item)
		unique = append(unique, item)
	}

	return unique
}

// unionArrayValues concatenates left with every array in others and removes
// duplicates while preserving first-seen order, mirroring Ruby's
// Array#union(*others). The receiver's own duplicates are collapsed too, so the
// result is always free of repeats.
func unionArrayValues(left []Value, others [][]Value) []Value {
	total := len(left)
	for _, other := range others {
		total += len(other)
	}
	combined := make([]Value, 0, total)
	combined = append(combined, left...)
	for _, other := range others {
		combined = append(combined, other...)
	}
	return uniqueValues(combined)
}

// differenceArrayValues returns the elements of left that do not appear in any
// of the others, mirroring Ruby's Array#difference(*others). Unlike union it
// preserves the receiver's own duplicates: only elements equal to something in
// the others are dropped.
func differenceArrayValues(left []Value, others [][]Value) []Value {
	if len(others) == 0 {
		out := make([]Value, len(left))
		copy(out, left)
		return out
	}
	removalTotal := 0
	for _, other := range others {
		removalTotal += len(other)
	}
	removal := make([]Value, 0, removalTotal)
	for _, other := range others {
		removal = append(removal, other...)
	}
	return subtractArrayValues(left, removal)
}

func subtractArrayValues(left, right []Value) []Value {
	var rightScalars map[scalarValueSetKey]struct{}
	rightComposite := make([]Value, 0)
	for _, item := range right {
		if key, ok := scalarValueKey(item); ok {
			if rightScalars == nil {
				rightScalars = make(map[scalarValueSetKey]struct{}, len(right))
			}
			rightScalars[key] = struct{}{}
			continue
		}
		rightComposite = append(rightComposite, item)
	}

	out := make([]Value, 0, len(left))
	for _, item := range left {
		if key, ok := scalarValueKey(item); ok {
			if _, found := rightScalars[key]; found {
				continue
			}
			out = append(out, item)
			continue
		}
		if containsEqualValue(rightComposite, item) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func containsEqualValue(values []Value, target Value) bool {
	for _, candidate := range values {
		if target.Equal(candidate) {
			return true
		}
	}
	return false
}
