package runtime

import "reflect"

// Arrays compare lexicographically, as in Ruby: the first element pair that
// differs decides the result, and when every compared pair is equal the
// shorter array sorts first. Without this, an array of arrays could not be
// sorted at all -- which mattered more than it looks, because a pair is the
// language's own representation of a hash entry, so `hash.to_a.sort` was the
// documented route to an ordered hash and it failed.
//
// Only `<=>` and the ordering members (sort, min, max, and their _by forms)
// use this. The relational operators keep rejecting arrays, matching Ruby,
// where Array defines `<=>` but does not include Comparable, so `[1] < [2]`
// raises rather than answering.

// arrayComparePair identifies a pair of arrays already on the comparison
// stack. A self-referential array would otherwise recurse forever.
type arrayComparePair struct {
	leftPtr  uintptr
	rightPtr uintptr
	leftLen  int
	rightLen int
}

// compareArrayOrder compares two arrays lexicographically.
//
// ordered is false when some element pair is unordered (a NaN), which makes
// the whole comparison unordered exactly as a bare NaN comparison is. A
// non-nil error means an element pair cannot be ordered at all; callers treat
// it as they treat any incomparable pair, so `[1,2] <=> [1,"a"]` yields nil
// while `sort` reports that the values are not comparable.
//
// A pair of arrays already being compared counts as equal, which terminates
// on cyclic structures and matches Ruby, where comparing two distinct
// self-referential arrays answers 0 rather than exhausting the stack.
func compareArrayOrder(left, right []Value, seen map[arrayComparePair]struct{}) (order int, ordered bool, err error) {
	leftPtr, rightPtr := sliceAddress(left), sliceAddress(right)
	if leftPtr != 0 && leftPtr == rightPtr && len(left) == len(right) {
		return 0, true, nil
	}
	pair := arrayComparePair{leftPtr: leftPtr, rightPtr: rightPtr, leftLen: len(left), rightLen: len(right)}
	if pair.leftPtr != 0 || pair.rightPtr != 0 {
		if seen == nil {
			seen = map[arrayComparePair]struct{}{}
		}
		if _, onStack := seen[pair]; onStack {
			return 0, true, nil
		}
		// Scoped to this comparison's own stack, not marked permanently: two
		// sibling subtrees may legitimately compare the same pair of arrays.
		seen[pair] = struct{}{}
		defer delete(seen, pair)
	}
	for i := range min(len(left), len(right)) {
		order, ordered, err = compareOrderForSort(left[i], right[i], seen)
		if err != nil || !ordered {
			return 0, false, err
		}
		if order != 0 {
			return order, true, nil
		}
	}
	// Every compared element was equal, so the shorter array sorts first.
	switch {
	case len(left) < len(right):
		return -1, true, nil
	case len(left) > len(right):
		return 1, true, nil
	default:
		return 0, true, nil
	}
}

// compareSpaceshipOrder orders two values for the <=> operator: arrays
// compare lexicographically, and every other operand pair keeps exactly the
// ordering it has today.
//
// Operand coverage follows Ruby per kind rather than the element coverage
// below: symbols order under both <=> and the relational operators (Symbol
// includes Comparable), nil orders under <=> only, and booleans order under
// neither even though sort accepts them.
func compareSpaceshipOrder(left, right Value) (order int, ordered bool, err error) {
	if left.Kind() == KindArray && right.Kind() == KindArray {
		return compareArrayOrder(left.Array(), right.Array(), nil)
	}
	// nil answers <=> against itself but has no relational operators, so this
	// case belongs to the spaceship alone: Ruby's `nil <=> nil` is 0 while
	// `nil < nil` raises. Booleans get neither, there as here.
	if left.Kind() == KindNil && right.Kind() == KindNil {
		return 0, true, nil
	}
	return compareValueOrder(left, right)
}

// compareOrderForSort orders two values for the ordering members and for
// elements nested inside a compared array. It is compareValueOrder widened
// with the kinds that
// order but that the relational operators do not accept -- arrays, nil,
// booleans, and symbols -- and it is deliberately not what `<` and friends
// call, so those keep rejecting the same operands they reject today.
//
// Symbols and nil matter here beyond arrays themselves: a hash entry is a
// [symbol, value] pair, so hash.to_a.sort orders symbol-headed pairs, which
// is the case that made array comparison worth having.
func compareOrderForSort(left, right Value, seen map[arrayComparePair]struct{}) (order int, ordered bool, err error) {
	switch {
	case left.Kind() == KindArray && right.Kind() == KindArray:
		return compareArrayOrder(left.Array(), right.Array(), seen)
	case left.Kind() == KindNil && right.Kind() == KindNil:
		return 0, true, nil
	case left.Kind() == KindBool && right.Kind() == KindBool:
		switch {
		case !left.Bool() && right.Bool():
			return -1, true, nil
		case left.Bool() && !right.Bool():
			return 1, true, nil
		default:
			return 0, true, nil
		}
	case left.Kind() == KindSymbol && right.Kind() == KindSymbol:
		return compareOrderedStrings(left.String(), right.String()), true, nil
	default:
		return compareValueOrder(left, right)
	}
}

// compareOrderedStrings reports the relative order of two strings as -1, 0,
// or 1.
func compareOrderedStrings(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

// sliceAddress returns the backing-array address of a slice, or 0 when the
// slice has none, so two arrays can be recognized as the same object.
func sliceAddress(values []Value) uintptr {
	if values == nil {
		return 0
	}
	return reflect.ValueOf(values).Pointer()
}
