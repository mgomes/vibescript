package runtime

import (
	"context"
	"errors"
	"reflect"
)

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

// arrayComparePair identifies a pair of arrays being compared.
type arrayComparePair struct {
	leftPtr  uintptr
	rightPtr uintptr
	leftLen  int
	rightLen int
}

// arrayCompareState carries the sandbox hooks and the memo through a
// recursive comparison.
//
// Both exist for the same reason. Deleting a pair from the on-stack set as
// soon as it returned meant two sibling branches re-walked the same subtree,
// so comparing shared DAGs -- each level holding the previous child twice on
// both sides -- did 2^d work. Measured before the fix: 152ms, 551ms, and
// 2.1s at depths 20, 22, and 24, with the step quota never firing, because
// nothing in the walk charged a step. A script could monopolize the runtime
// from inside <=> regardless of its limits.
//
// Memoizing each completed pair collapses the shared subtrees to one
// comparison apiece, and charging a step per compared element makes the walk
// observable to the step quota and to cancellation.
type arrayCompareState struct {
	exec *Execution
	// onStack holds the pairs currently being compared, so a cyclic structure
	// terminates.
	onStack map[arrayComparePair]struct{}
	// done caches the outcome of pairs already compared, so a subtree shared
	// between siblings is walked once rather than once per path.
	done map[arrayComparePair]arrayCompareResult
	// cycleShortcuts counts how many times a comparison answered "equal"
	// because the pair was already on the stack. A result computed while such
	// a shortcut fired depends on the enclosing cycle and is not reusable
	// elsewhere, so only results whose subtree took none are memoized.
	cycleShortcuts int
}

// arrayCompareResult is a completed comparison, memoized.
type arrayCompareResult struct {
	order   int
	ordered bool
	err     error
}

// step charges the execution for one compared element and honors
// cancellation. A nil execution (host-side comparison outside a script) is
// unmetered, as it is elsewhere.
func (state *arrayCompareState) step() error {
	if state == nil || state.exec == nil {
		return nil
	}
	return state.exec.step()
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
func compareArrayOrder(left, right []Value, state *arrayCompareState) (order int, ordered bool, err error) {
	if state == nil {
		state = &arrayCompareState{}
	}
	leftPtr, rightPtr := sliceAddress(left), sliceAddress(right)
	if leftPtr != 0 && leftPtr == rightPtr && len(left) == len(right) {
		return 0, true, nil
	}
	pair := arrayComparePair{leftPtr: leftPtr, rightPtr: rightPtr, leftLen: len(left), rightLen: len(right)}
	tracked := pair.leftPtr != 0 || pair.rightPtr != 0
	if tracked {
		if cached, ok := state.done[pair]; ok {
			return cached.order, cached.ordered, cached.err
		}
		if state.onStack == nil {
			state.onStack = map[arrayComparePair]struct{}{}
		}
		if _, onStack := state.onStack[pair]; onStack {
			// A pair already being compared counts as equal, which terminates
			// on cyclic structures and matches Ruby, where two distinct
			// self-referential arrays answer 0.
			state.cycleShortcuts++
			return 0, true, nil
		}
		state.onStack[pair] = struct{}{}
		shortcutsBefore := state.cycleShortcuts
		defer func() {
			delete(state.onStack, pair)
			// Memoize only a result whose whole subtree was computed without
			// the equal-on-cycle shortcut. One that used it depends on the
			// enclosing cycle and would be wrong to reuse elsewhere.
			if state.cycleShortcuts != shortcutsBefore {
				return
			}
			if state.done == nil {
				state.done = map[arrayComparePair]arrayCompareResult{}
			}
			state.done[pair] = arrayCompareResult{order: order, ordered: ordered, err: err}
		}()
	}
	for i := range min(len(left), len(right)) {
		// One step per compared element, so the walk is bounded by the step
		// quota and observes cancellation.
		if stepErr := state.step(); stepErr != nil {
			return 0, false, stepErr
		}
		order, ordered, err = compareOrderForSort(left[i], right[i], state)
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
func compareSpaceshipOrder(exec *Execution, left, right Value) (order int, ordered bool, err error) {
	if left.Kind() == KindArray && right.Kind() == KindArray {
		return compareArrayOrder(left.Array(), right.Array(), &arrayCompareState{exec: exec})
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
func compareOrderForSort(left, right Value, state *arrayCompareState) (order int, ordered bool, err error) {
	switch {
	case left.Kind() == KindArray && right.Kind() == KindArray:
		return compareArrayOrder(left.Array(), right.Array(), state)
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

// sortComparisonError classifies an error from a metered element comparison.
//
// The ordering members replace a comparison failure with their own "values are
// not comparable" message, which is right for genuine incomparability and
// wrong for everything else: now that the comparison charges steps and
// observes cancellation, that replacement would relabel a quota exhaustion as
// an ordinary runtime error and hide a canceled context entirely. Sandbox and
// context errors pass through unchanged so they keep their classification and
// stay outside rescue's reach.
func sortComparisonError(err error, incomparable string) error {
	if err == nil {
		return nil
	}
	if classifyRuntimeErrorType(err) == runtimeErrorTypeLimit || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return errors.New(incomparable)
}
