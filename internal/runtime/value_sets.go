package runtime

// setOpInitialCap bounds the capacity reserved up front by the array set
// helpers (union, difference, and uniq). The result and the membership set are
// at most as large as the inputs, but for heavily overlapping inputs they can
// be far smaller. Reserving the full input length would peak at roughly the same
// memory as the temporary slices these helpers were written to avoid, and that
// allocation escapes the post-call memory check. Capping the reservation and
// letting append and map growth take over keeps the peak proportional to the
// data actually retained.
const setOpInitialCap = 4096

// boundedSetCap caps a desired capacity at setOpInitialCap so a huge input
// length never drives an oversized up-front allocation.
func boundedSetCap(n int) int {
	if n > setOpInitialCap {
		return setOpInitialCap
	}
	return n
}

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

// valueSet tracks membership of Values using value equality, collapsing
// duplicates as values are added. Scalar values are indexed in a map keyed by
// their content, while composite values (arrays, hashes, and other non-scalar
// kinds) fall back to a linear scan with Value.Equal. union and uniq build on it
// because they need duplicate collapsing; difference and subtract use the
// non-deduping membershipSet instead.
type valueSet struct {
	scalars   map[scalarValueSetKey]struct{}
	composite []Value
}

// add inserts v into the set if absent and reports whether it was newly added.
// hint sizes the scalar map on first use; it is capped by boundedSetCap so a
// huge input length never drives an oversized map allocation, letting the map
// grow to the number of distinct scalars actually inserted. Composite values are
// deduplicated via a linear Value.Equal scan, so add is suited to the
// duplicate-collapsing helpers (union, uniq) but not to membership-only callers
// where that scan would make insertion quadratic.
func (s *valueSet) add(v Value, hint int) bool {
	if key, ok := scalarValueKey(v); ok {
		if s.scalars == nil {
			s.scalars = make(map[scalarValueSetKey]struct{}, boundedSetCap(hint))
		}
		if _, found := s.scalars[key]; found {
			return false
		}
		s.scalars[key] = struct{}{}
		return true
	}
	if containsEqualValue(s.composite, v) {
		return false
	}
	s.composite = append(s.composite, v)
	return true
}

// membershipSet answers contains queries with value equality but, unlike
// valueSet, never deduplicates on insertion. Scalars still go in a map for O(1)
// membership, while composites are simply appended; the linear Value.Equal scan
// happens only when contains is called. difference and subtract use it because
// they only need to know whether the removal side holds a value, never how many
// times. Skipping the scan-on-insert keeps building the removal side linear in
// the argument length even when those arguments are full of distinct composites.
type membershipSet struct {
	scalars   map[scalarValueSetKey]struct{}
	composite []Value
}

// contains reports whether the set holds a value equal to v.
func (s *membershipSet) contains(v Value) bool {
	if key, ok := scalarValueKey(v); ok {
		_, found := s.scalars[key]
		return found
	}
	return containsEqualValue(s.composite, v)
}

// add records v for later membership tests. hint sizes the scalar map on first
// use, capped by boundedSetCap. Scalars are deduplicated by the map key for
// free; composites are appended without scanning, so insertion stays O(1).
func (s *membershipSet) add(v Value, hint int) {
	if key, ok := scalarValueKey(v); ok {
		if s.scalars == nil {
			s.scalars = make(map[scalarValueSetKey]struct{}, boundedSetCap(hint))
		}
		s.scalars[key] = struct{}{}
		return
	}
	s.composite = append(s.composite, v)
}

func uniqueValues(values []Value) []Value {
	var seen valueSet
	unique := make([]Value, 0, boundedSetCap(len(values)))
	for _, item := range values {
		if seen.add(item, len(values)) {
			unique = append(unique, item)
		}
	}
	return unique
}

// unionArrayValues returns the receiver concatenated with every array in others,
// duplicates removed while preserving first-seen order, mirroring Ruby's
// Array#union(*others). The receiver's own duplicates are collapsed too, so the
// result is always free of repeats. The unique result is built directly while
// iterating the inputs, so no intermediate concatenated slice is materialized.
func unionArrayValues(left []Value, others [][]Value) []Value {
	total := len(left)
	for _, other := range others {
		total += len(other)
	}
	var seen valueSet
	unique := make([]Value, 0, boundedSetCap(total))
	for _, item := range left {
		if seen.add(item, total) {
			unique = append(unique, item)
		}
	}
	for _, other := range others {
		for _, item := range other {
			if seen.add(item, total) {
				unique = append(unique, item)
			}
		}
	}
	return unique
}

// differenceArrayValues returns the elements of left that do not appear in any
// of the others, mirroring Ruby's Array#difference(*others). Unlike union it
// preserves the receiver's own duplicates: only elements equal to something in
// the others are dropped. The removal side is built incrementally into a
// membershipSet so no intermediate flattened slice is materialized and building
// it stays linear in the argument length.
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
	var removal membershipSet
	for _, other := range others {
		for _, item := range other {
			removal.add(item, removalTotal)
		}
	}
	out := make([]Value, 0, boundedSetCap(len(left)))
	for _, item := range left {
		if removal.contains(item) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func subtractArrayValues(left, right []Value) []Value {
	var removal membershipSet
	for _, item := range right {
		removal.add(item, len(right))
	}
	out := make([]Value, 0, boundedSetCap(len(left)))
	for _, item := range left {
		if removal.contains(item) {
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
